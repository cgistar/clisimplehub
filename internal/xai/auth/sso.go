package auth

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

const (
	ssoAccountsURL     = "https://accounts.x.ai/"
	ssoVerifyURL       = Issuer + "/oauth2/device/verify"
	ssoApproveURL      = Issuer + "/oauth2/device/approve"
	ssoOperationLimit  = 3 * time.Minute
	ssoRequestTimeout  = 30
	ssoMaxAttempts     = 4
	ssoBackoffBase     = 15 * time.Second
	ssoBackoffMax      = 60 * time.Second
	ssoBodyPreviewSize = 512
)

type SSOExchangeResult struct {
	TokenData TokenData
	Warning   string
}

type ssoBrowser interface {
	SetSSOCookie(string) error
	Request(context.Context, string, string, url.Values) (int, string, string, error)
}

type ssoExchangeDeps struct {
	newBrowser  func(string) (ssoBrowser, error)
	startDevice func(context.Context) (*DeviceCodeResponse, error)
	pollToken   func(context.Context, *DeviceCodeResponse) (*TokenData, error)
	userinfo    func(context.Context, string) (string, string, error)
	sleep       func(context.Context, time.Duration) error
	maxAttempts int
}

// ExchangeSSOForTokens 使用已有 SSO Cookie 自动确认 xAI Device Flow。
func ExchangeSSOForTokens(ctx context.Context, sso, proxyURL string) (*SSOExchangeResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, ssoOperationLimit)
	defer cancel()

	auth := NewXAIAuth(proxyURL)
	return exchangeSSOForTokens(ctx, strings.TrimSpace(sso), proxyURL, ssoExchangeDeps{
		newBrowser: newSSOBrowser,
		startDevice: func(ctx context.Context) (*DeviceCodeResponse, error) {
			return auth.StartDeviceFlowWithScope(ctx, SSOScope)
		},
		pollToken:   auth.PollForToken,
		userinfo:    auth.fetchUserInfo,
		sleep:       sleepContext,
		maxAttempts: ssoMaxAttempts,
	})
}

func exchangeSSOForTokens(ctx context.Context, sso, proxyURL string, deps ssoExchangeDeps) (*SSOExchangeResult, error) {
	if strings.TrimSpace(sso) == "" {
		return nil, fmt.Errorf("sso cookie is required")
	}
	if deps.newBrowser == nil || deps.startDevice == nil || deps.pollToken == nil || deps.sleep == nil {
		return nil, fmt.Errorf("sso2auth dependencies are incomplete")
	}
	if deps.maxAttempts <= 0 {
		deps.maxAttempts = 1
	}

	browser, err := deps.newBrowser(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("sso2auth create browser client: %w", err)
	}
	if err := browser.SetSSOCookie(sso); err != nil {
		return nil, fmt.Errorf("sso2auth set cookie: %w", err)
	}
	status, finalURL, _, err := browser.Request(ctx, fhttp.MethodGet, ssoAccountsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("sso2auth validate sso: %w", err)
	}
	if status < 200 || status >= 400 {
		return nil, fmt.Errorf("sso2auth validate sso failed (HTTP %d)", status)
	}
	if isSignInURL(finalURL) {
		return nil, fmt.Errorf("sso cookie is invalid or expired")
	}

	var body string
	for attempt := 1; attempt <= deps.maxAttempts; attempt++ {
		deviceCode, startErr := deps.startDevice(ctx)
		if startErr != nil {
			return nil, startErr
		}
		verificationURL := strings.TrimSpace(deviceCode.VerificationURIComplete)
		if verificationURL == "" {
			verificationURL = strings.TrimSpace(deviceCode.VerificationURI)
		}
		if verificationURL == "" {
			return nil, fmt.Errorf("sso2auth device response missing verification URI")
		}
		if _, _, _, err = browser.Request(ctx, fhttp.MethodGet, verificationURL, nil); err != nil {
			return nil, fmt.Errorf("sso2auth open verification page: %w", err)
		}

		status, finalURL, body, err = browser.Request(ctx, fhttp.MethodPost, ssoVerifyURL, url.Values{
			"user_code": {deviceCode.UserCode},
		})
		if err != nil {
			return nil, fmt.Errorf("sso2auth verify request: %w", err)
		}
		if isSSORateLimited(status, finalURL, body) {
			if err := waitSSORetry(ctx, deps, attempt); err != nil {
				return nil, err
			}
			continue
		}
		if status < 200 || status >= 400 || !strings.Contains(strings.ToLower(finalURL), "consent") {
			return nil, fmt.Errorf("sso2auth verify failed (HTTP %d, redirect=%s)", status, safeURL(finalURL))
		}

		status, finalURL, body, err = browser.Request(ctx, fhttp.MethodPost, ssoApproveURL, url.Values{
			"user_code":      {deviceCode.UserCode},
			"action":         {"allow"},
			"principal_type": {"User"},
			"principal_id":   {""},
		})
		if err != nil {
			return nil, fmt.Errorf("sso2auth approve request: %w", err)
		}
		if isSSORateLimited(status, finalURL, body) {
			if err := waitSSORetry(ctx, deps, attempt); err != nil {
				return nil, err
			}
			continue
		}
		if status < 200 || status >= 400 || !strings.Contains(strings.ToLower(finalURL), "done") {
			return nil, fmt.Errorf("sso2auth approve failed (HTTP %d, redirect=%s)", status, safeURL(finalURL))
		}

		tokenData, pollErr := deps.pollToken(ctx, deviceCode)
		if pollErr != nil {
			return nil, pollErr
		}
		if strings.TrimSpace(tokenData.AccessToken) == "" {
			return nil, fmt.Errorf("sso2auth returned empty access token")
		}
		if email, subject := tokenIdentity(tokenData); email != "" || subject != "" {
			tokenData.Email = email
			tokenData.Subject = subject
		}

		result := &SSOExchangeResult{TokenData: *tokenData}
		if deps.userinfo != nil {
			email, subject, infoErr := deps.userinfo(ctx, tokenData.AccessToken)
			if infoErr != nil {
				result.Warning = infoErr.Error()
			} else {
				if strings.TrimSpace(email) != "" {
					result.TokenData.Email = strings.TrimSpace(email)
				}
				if strings.TrimSpace(subject) != "" {
					result.TokenData.Subject = strings.TrimSpace(subject)
				}
			}
		}
		return result, nil
	}
	return nil, fmt.Errorf("sso2auth rate limit retry exhausted")
}

type tlsSSOBrowser struct {
	client tls_client.HttpClient
}

func newSSOBrowser(proxyURL string) (ssoBrowser, error) {
	options := []tls_client.HttpClientOption{
		tls_client.WithClientProfile(profiles.Chrome_146),
		tls_client.WithTimeoutSeconds(ssoRequestTimeout),
		tls_client.WithCookieJar(tls_client.NewCookieJar()),
	}
	if proxyURL = strings.TrimSpace(proxyURL); proxyURL != "" {
		options = append(options, tls_client.WithProxyUrl(proxyURL))
	}
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		return nil, err
	}
	return &tlsSSOBrowser{client: client}, nil
}

func (b *tlsSSOBrowser) SetSSOCookie(sso string) error {
	cookieURL, err := url.Parse(Issuer)
	if err != nil {
		return err
	}
	b.client.SetCookies(cookieURL, []*fhttp.Cookie{{
		Name:     "sso",
		Value:    strings.TrimSpace(sso),
		Domain:   ".x.ai",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
	}})
	return nil
}

func (b *tlsSSOBrowser) Request(ctx context.Context, method, targetURL string, form url.Values) (int, string, string, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := fhttp.NewRequestWithContext(ctx, method, targetURL, body)
	if err != nil {
		return 0, "", "", err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", Issuer)
		req.Header.Set("Referer", Issuer+"/oauth2/device")
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return 0, "", "", err
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, ssoBodyPreviewSize))
	if readErr != nil {
		return resp.StatusCode, responseURL(resp, targetURL), "", readErr
	}
	return resp.StatusCode, responseURL(resp, targetURL), string(raw), nil
}

func (a *XAIAuth) fetchUserInfo(ctx context.Context, accessToken string) (string, string, error) {
	discovery, err := a.Discover(ctx)
	if err != nil {
		return "", "", err
	}
	endpoint := strings.TrimSpace(discovery.UserInfoEndpoint)
	if endpoint == "" {
		endpoint = Issuer + "/oauth2/userinfo"
	}
	return a.fetchUserInfoHTTP(ctx, endpoint, accessToken)
}

func (a *XAIAuth) fetchUserInfoHTTP(ctx context.Context, endpoint, accessToken string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("Accept", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("userinfo request failed with status %d", resp.StatusCode)
	}
	var payload struct {
		Email   string `json:"email"`
		Subject string `json:"sub"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", "", fmt.Errorf("parse userinfo: %w", err)
	}
	return strings.TrimSpace(payload.Email), strings.TrimSpace(payload.Subject), nil
}

func waitSSORetry(ctx context.Context, deps ssoExchangeDeps, attempt int) error {
	if attempt >= deps.maxAttempts {
		return fmt.Errorf("sso2auth rate limit retry exhausted")
	}
	delay := ssoBackoffBase << min(attempt-1, 2)
	if delay > ssoBackoffMax {
		delay = ssoBackoffMax
	}
	delay += randomDuration(5 * time.Second)
	return deps.sleep(ctx, delay)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("sso2auth cancelled: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

func randomDuration(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	var b [1]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0
	}
	return time.Duration(int64(b[0]) * int64(max) / 256)
}

func isSSORateLimited(status int, finalURL, body string) bool {
	if status == 429 {
		return true
	}
	blob := strings.ToLower(finalURL + "\n" + body)
	return strings.Contains(blob, "rate_limited") ||
		strings.Contains(blob, "rate-limited") ||
		strings.Contains(blob, "too_many_requests") ||
		strings.Contains(blob, "ratelimit")
}

func isSignInURL(raw string) bool {
	lower := strings.ToLower(raw)
	return strings.Contains(lower, "sign-in") || strings.Contains(lower, "sign-up")
}

func safeURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "invalid-url"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func responseURL(resp *fhttp.Response, fallback string) string {
	if resp != nil && resp.Request != nil && resp.Request.URL != nil {
		return resp.Request.URL.String()
	}
	return fallback
}

func tokenIdentity(token *TokenData) (string, string) {
	if token == nil {
		return "", ""
	}
	email, subject := parseJWTIdentity(token.IDToken)
	if email == "" || subject == "" {
		accessEmail, accessSubject := parseJWTIdentity(token.AccessToken)
		if email == "" {
			email = accessEmail
		}
		if subject == "" {
			subject = accessSubject
		}
	}
	return email, subject
}
