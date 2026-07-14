package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"clisimplehub/internal/executor"

	"golang.org/x/sync/singleflight"
)

// XAIAuth performs xAI OAuth discovery, token exchange, and refresh.
type XAIAuth struct {
	httpClient *http.Client
}

var xaiRefreshGroup singleflight.Group

func NewXAIAuth(proxyURL string) *XAIAuth {
	return &XAIAuth{httpClient: executor.NewHTTPClientForcedProxyURL(proxyURL, 30*time.Second)}
}

func ValidateOAuthEndpoint(rawURL string, field string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("xai discovery %s is empty", field)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("xai discovery %s is invalid: %w", field, err)
	}
	if parsed.Scheme != "https" {
		return "", fmt.Errorf("xai discovery %s must use https: %q", field, rawURL)
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host != "x.ai" && !strings.HasSuffix(host, ".x.ai") {
		return "", fmt.Errorf("xai discovery %s host %q is not on x.ai", field, host)
	}
	return rawURL, nil
}

func BuildAuthorizeURL(params AuthorizeURLParams) (string, error) {
	endpoint, err := ValidateOAuthEndpoint(params.AuthorizationEndpoint, "authorization_endpoint")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(params.RedirectURI) == "" {
		return "", fmt.Errorf("xai authorize URL: redirect URI is required")
	}
	if strings.TrimSpace(params.CodeChallenge) == "" {
		return "", fmt.Errorf("xai authorize URL: code challenge is required")
	}
	if strings.TrimSpace(params.State) == "" {
		return "", fmt.Errorf("xai authorize URL: state is required")
	}
	if strings.TrimSpace(params.Nonce) == "" {
		return "", fmt.Errorf("xai authorize URL: nonce is required")
	}
	values := url.Values{
		"response_type":         {"code"},
		"client_id":             {ClientID},
		"redirect_uri":          {strings.TrimSpace(params.RedirectURI)},
		"scope":                 {Scope},
		"code_challenge":        {strings.TrimSpace(params.CodeChallenge)},
		"code_challenge_method": {"S256"},
		"state":                 {strings.TrimSpace(params.State)},
		"nonce":                 {strings.TrimSpace(params.Nonce)},
		"plan":                  {"generic"},
		"referrer":              {"cli-simple-hub"},
	}
	return endpoint + "?" + values.Encode(), nil
}

func (a *XAIAuth) Discover(ctx context.Context) (*Discovery, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, DiscoveryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("xai discovery: create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xai discovery: request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("xai discovery: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xai discovery failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		AuthorizationEndpoint       string `json:"authorization_endpoint"`
		TokenEndpoint               string `json:"token_endpoint"`
		DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
		UserInfoEndpoint            string `json:"userinfo_endpoint"`
	}
	if err = json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("xai discovery: parse response: %w", err)
	}
	authorizationEndpoint, err := ValidateOAuthEndpoint(payload.AuthorizationEndpoint, "authorization_endpoint")
	if err != nil {
		return nil, err
	}
	tokenEndpoint, err := ValidateOAuthEndpoint(payload.TokenEndpoint, "token_endpoint")
	if err != nil {
		return nil, err
	}
	deviceAuthEndpoint := ""
	if strings.TrimSpace(payload.DeviceAuthorizationEndpoint) != "" {
		deviceAuthEndpoint, err = ValidateOAuthEndpoint(payload.DeviceAuthorizationEndpoint, "device_authorization_endpoint")
		if err != nil {
			return nil, err
		}
	}
	userInfoEndpoint := ""
	if strings.TrimSpace(payload.UserInfoEndpoint) != "" {
		userInfoEndpoint, err = ValidateOAuthEndpoint(payload.UserInfoEndpoint, "userinfo_endpoint")
		if err != nil {
			return nil, err
		}
	}
	return &Discovery{
		AuthorizationEndpoint:       authorizationEndpoint,
		TokenEndpoint:               tokenEndpoint,
		DeviceAuthorizationEndpoint: deviceAuthEndpoint,
		UserInfoEndpoint:            userInfoEndpoint,
	}, nil
}

func (a *XAIAuth) ExchangeCodeForTokens(ctx context.Context, code, redirectURI string, pkceCodes *PKCECodes, tokenEndpoint string) (*AuthBundle, error) {
	if pkceCodes == nil {
		return nil, fmt.Errorf("xai token exchange: PKCE codes are required")
	}
	if strings.TrimSpace(code) == "" {
		return nil, fmt.Errorf("xai token exchange: authorization code is required")
	}
	if strings.TrimSpace(redirectURI) == "" {
		return nil, fmt.Errorf("xai token exchange: redirect URI is required")
	}
	if strings.TrimSpace(tokenEndpoint) == "" {
		discovery, errDiscover := a.Discover(ctx)
		if errDiscover != nil {
			return nil, errDiscover
		}
		tokenEndpoint = discovery.TokenEndpoint
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {strings.TrimSpace(code)},
		"redirect_uri":  {strings.TrimSpace(redirectURI)},
		"client_id":     {ClientID},
		"code_verifier": {pkceCodes.CodeVerifier},
	}
	tokenData, err := a.postTokenForm(ctx, tokenEndpoint, form)
	if err != nil {
		return nil, err
	}
	return &AuthBundle{
		TokenData:     *tokenData,
		LastRefresh:   time.Now().UTC().Format(time.RFC3339),
		BaseURL:       DefaultAPIBaseURL,
		RedirectURI:   strings.TrimSpace(redirectURI),
		TokenEndpoint: strings.TrimSpace(tokenEndpoint),
	}, nil
}

func (a *XAIAuth) RefreshTokens(ctx context.Context, refreshToken, tokenEndpoint string) (*TokenData, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, fmt.Errorf("xai token refresh: refresh token is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	refreshToken = strings.TrimSpace(refreshToken)
	if strings.TrimSpace(tokenEndpoint) == "" {
		discovery, errDiscover := a.Discover(ctx)
		if errDiscover != nil {
			return nil, errDiscover
		}
		tokenEndpoint = discovery.TokenEndpoint
	}
	tokenEndpoint = strings.TrimSpace(tokenEndpoint)

	result, err, _ := xaiRefreshGroup.Do(refreshToken, func() (interface{}, error) {
		return a.refreshTokensSingleFlight(ctx, refreshToken, tokenEndpoint)
	})
	if err != nil {
		return nil, err
	}
	tokenData, ok := result.(*TokenData)
	if !ok || tokenData == nil {
		return nil, fmt.Errorf("xai token refresh failed: invalid single-flight result")
	}
	return tokenData, nil
}

func (a *XAIAuth) refreshTokensSingleFlight(ctx context.Context, refreshToken, tokenEndpoint string) (*TokenData, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {ClientID},
		"refresh_token": {refreshToken},
	}
	return a.postTokenForm(ctx, tokenEndpoint, form)
}

func (a *XAIAuth) postTokenForm(ctx context.Context, tokenEndpoint string, form url.Values) (*TokenData, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(tokenEndpoint), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("xai token request: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xai token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("xai token response: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xai token request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err = json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("xai token response: parse body: %w", err)
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return nil, fmt.Errorf("xai token response missing access_token")
	}
	email, subject := parseJWTIdentity(payload.IDToken)
	return &TokenData{
		AccessToken:  strings.TrimSpace(payload.AccessToken),
		RefreshToken: strings.TrimSpace(payload.RefreshToken),
		IDToken:      strings.TrimSpace(payload.IDToken),
		TokenType:    strings.TrimSpace(payload.TokenType),
		ExpiresIn:    payload.ExpiresIn,
		Expire:       time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second).UTC().Format(time.RFC3339),
		Email:        email,
		Subject:      subject,
	}, nil
}

func parseJWTIdentity(token string) (email string, subject string) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", ""
	}
	payload := parts[1]
	payload += strings.Repeat("=", (4-len(payload)%4)%4)
	raw, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return "", ""
	}
	var claims map[string]any
	if err = json.Unmarshal(raw, &claims); err != nil {
		return "", ""
	}
	if v, ok := claims["email"].(string); ok {
		email = strings.TrimSpace(v)
	}
	if v, ok := claims["sub"].(string); ok {
		subject = strings.TrimSpace(v)
	}
	return email, subject
}

type oauthResult struct {
	Code  string
	State string
	Error string
}

// StartXAILoginWithURL starts loopback OAuth and returns auth URL + wait/cleanup.
func StartXAILoginWithURL(ctx context.Context, proxyURL string) (authURL string, waitFn func() (*LoginResult, error), cleanup func(), err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	pkce, err := GeneratePKCECodes()
	if err != nil {
		return "", nil, nil, fmt.Errorf("xai pkce generation failed: %w", err)
	}
	state, err := GenerateState()
	if err != nil {
		return "", nil, nil, fmt.Errorf("xai state generation failed: %w", err)
	}
	nonce, err := GenerateState()
	if err != nil {
		return "", nil, nil, fmt.Errorf("xai nonce generation failed: %w", err)
	}

	authSvc := NewXAIAuth(proxyURL)
	discovery, err := authSvc.Discover(ctx)
	if err != nil {
		return "", nil, nil, err
	}

	srv, port, resultCh, errServer := startXAICallbackServer(CallbackPort)
	if errServer != nil {
		return "", nil, nil, fmt.Errorf("xai: failed to start callback server: %w", errServer)
	}

	waitCtx, waitCancel := context.WithCancel(ctx)
	var cleanupOnce sync.Once
	cleanupFn := func() {
		cleanupOnce.Do(func() {
			waitCancel()
			shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutCtx)
		})
	}

	redirectURI := fmt.Sprintf("http://%s:%d%s", RedirectHost, port, RedirectPath)
	authURL, err = BuildAuthorizeURL(AuthorizeURLParams{
		AuthorizationEndpoint: discovery.AuthorizationEndpoint,
		RedirectURI:           redirectURI,
		CodeChallenge:         pkce.CodeChallenge,
		State:                 state,
		Nonce:                 nonce,
	})
	if err != nil {
		cleanupFn()
		return "", nil, nil, err
	}

	waitFn = func() (*LoginResult, error) {
		var result oauthResult
		select {
		case result = <-resultCh:
		case <-time.After(5 * time.Minute):
			return nil, fmt.Errorf("xai: authentication timed out")
		case <-waitCtx.Done():
			return nil, waitCtx.Err()
		}

		if result.Error != "" {
			return nil, fmt.Errorf("xai: authentication failed: %s", result.Error)
		}
		if result.State != state {
			return nil, fmt.Errorf("xai: invalid state")
		}
		if result.Code == "" {
			return nil, fmt.Errorf("xai: missing authorization code")
		}

		bundle, errExchange := authSvc.ExchangeCodeForTokens(ctx, result.Code, redirectURI, pkce, discovery.TokenEndpoint)
		if errExchange != nil {
			return nil, fmt.Errorf("xai: token exchange failed: %w", errExchange)
		}
		return &LoginResult{
			AccessToken:   bundle.TokenData.AccessToken,
			RefreshToken:  bundle.TokenData.RefreshToken,
			IDToken:       bundle.TokenData.IDToken,
			Email:         bundle.TokenData.Email,
			Subject:       bundle.TokenData.Subject,
			ExpiresAt:     bundle.TokenData.Expire,
			BaseURL:       firstNonEmpty(bundle.BaseURL, DefaultAPIBaseURL),
			RedirectURI:   bundle.RedirectURI,
			TokenEndpoint: bundle.TokenEndpoint,
			LastRefresh:   bundle.LastRefresh,
		}, nil
	}

	return authURL, waitFn, cleanupFn, nil
}

// SubmitCallbackURL forwards a browser callback URL to the local OAuth server.
func SubmitCallbackURL(ctx context.Context, callbackURL string) error {
	callbackURL = strings.TrimSpace(callbackURL)
	if callbackURL == "" {
		return fmt.Errorf("callback URL is required")
	}
	u, err := url.Parse(callbackURL)
	if err != nil || u == nil {
		return fmt.Errorf("invalid callback URL")
	}
	if u.Scheme != "http" {
		return fmt.Errorf("callback URL must use http")
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" {
		return fmt.Errorf("callback URL must target localhost")
	}
	if u.Path != RedirectPath {
		return fmt.Errorf("callback URL must target %s", RedirectPath)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("submit callback URL: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("callback rejected (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// SubmitManualCallbackToken accepts a raw authorization code pasted by the user.
func SubmitManualCallbackToken(resultCh chan<- oauthResult, code, state string) {
	code = strings.TrimSpace(code)
	if code == "" {
		return
	}
	select {
	case resultCh <- oauthResult{Code: code, State: state}:
	default:
	}
}

func startXAICallbackServer(port int) (*http.Server, int, <-chan oauthResult, error) {
	if port <= 0 {
		port = CallbackPort
	}
	addr := fmt.Sprintf("%s:%d", RedirectHost, port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		// 端口占用时回退到系统分配端口
		listener, err = net.Listen("tcp", fmt.Sprintf("%s:0", RedirectHost))
		if err != nil {
			return nil, 0, nil, err
		}
	}
	port = listener.Addr().(*net.TCPAddr).Port
	resultCh := make(chan oauthResult, 1)

	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc(RedirectPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		result := oauthResult{
			Code:  strings.TrimSpace(q.Get("code")),
			Error: strings.TrimSpace(q.Get("error")),
			State: strings.TrimSpace(q.Get("state")),
		}
		once.Do(func() {
			resultCh <- result
		})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if result.Code != "" && result.Error == "" {
			_, _ = w.Write([]byte("<h1>Login successful</h1><p>You can close this window.</p>"))
			return
		}
		_, _ = w.Write([]byte("<h1>Login failed</h1><p>Please check the application output.</p>"))
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
	}
	go func() {
		_ = srv.Serve(listener)
	}()

	return srv, port, resultCh, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
