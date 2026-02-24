package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/url"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/google/uuid"

	"clisimplehub/internal/codex/auth/mailprovider"
)

// FingerprintBlockedError 表示浏览器指纹被阻止，需要重试
type FingerprintBlockedError struct {
	StatusCode  int
	ContentType string
	FinalURL    string
	BodyPreview string
}

func (e *FingerprintBlockedError) Error() string {
	return fmt.Sprintf("fingerprint blocked (HTTP %d): Content-Type: %s, final URL: %s, body preview: %s",
		e.StatusCode, e.ContentType, e.FinalURL, e.BodyPreview)
}

type SignupState int

const (
	SignupIdle SignupState = iota
	SignupCreatingEmail
	SignupVisitingHomepage
	SignupGettingCSRF
	SignupSigningIn
	SignupAuthorizing
	SignupRegistering
	SignupSendingOTP
	SignupWaitingOTP
	SignupNeedOTP
	SignupValidatingOTP
	SignupCreatingProfile
	SignupCallback
	SignupOAuthLogin
	SignupSuccess
	SignupError
)

const chatgptBase = "https://chatgpt.com"

type SignupRequest struct {
	EmailProvider  string
	ProviderParams map[string]string
	Email          string
	Password       string
	ClientID       string
	ProxyURL       string
	OnStep         func(msg string)
}

type SignupResult struct {
	*CodexLoginResult
	Password string `json:"password"`
}

type SignupSession struct {
	state          SignupState
	client         tls_client.HttpClient // follow-redirect client (chatgpt.com needs it)
	noRedirClient  tls_client.HttpClient // no-redirect client (for auth.openai.com steps)
	deviceID       string
	authLoggingID  string
	userAgent      string
	secChUA        string
	chromeVer      string
	acceptLanguage string
	email          string
	password       string
	clientID       string
	proxyURL       string
	onStep         func(msg string)
	provider       mailprovider.EmailProvider
	providerParams map[string]string
	callbackURL    string
	skipProfile    bool // 已存在账号，跳过 profile 创建
	err            error
	result         *SignupResult

	// 用于重试时保存原始请求信息
	originalReq    *SignupRequest
}

func (s *SignupSession) State() SignupState     { return s.state }
func (s *SignupSession) Result() *SignupResult   { return s.result }
func (s *SignupSession) Error() error            { return s.err }

func (s *SignupSession) emitStep(msg string) {
	log.Printf("[Signup] %s", msg)
	if s.onStep != nil {
		s.onStep(msg)
	}
}

func StartSignup(ctx context.Context, req *SignupRequest) (*SignupSession, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}

	clientID := req.ClientID
	if clientID == "" {
		clientID = ClientID
	}

	chromeProfile, chromeVer, userAgent := randomChromeVersion()
	log.Printf("[Signup] Using Chrome %s (TLS profile: v%d)", chromeVer, chromeProfile.Major)

	// Random Accept-Language (mirrors Python's random choice)
	acceptLanguages := []string{
		"en-US,en;q=0.9",
		"en-US,en;q=0.9,zh-CN;q=0.8",
		"en,en-US;q=0.9",
		"en-US,en;q=0.8",
	}
	acceptLanguage := acceptLanguages[rand.Intn(len(acceptLanguages))]

	// Create shared cookie jar for both clients (critical for session continuity)
	sharedJar := tls_client.NewCookieJar()

	followClient, err := newTLSClientWithJar(req.ProxyURL, true, chromeProfile.TLSProfile, sharedJar)
	if err != nil {
		return nil, fmt.Errorf("create TLS client: %w", err)
	}
	noRedirClient, err := newTLSClientWithJar(req.ProxyURL, false, chromeProfile.TLSProfile, sharedJar)
	if err != nil {
		return nil, fmt.Errorf("create no-redirect TLS client: %w", err)
	}

	deviceID := uuid.New().String()

	// Set oai-did cookie on chatgpt.com domain
	chatgptURL, _ := url.Parse(chatgptBase)
	didCookie := []*fhttp.Cookie{{Name: "oai-did", Value: deviceID, Domain: "chatgpt.com"}}
	followClient.SetCookies(chatgptURL, didCookie)
	noRedirClient.SetCookies(chatgptURL, didCookie)

	s := &SignupSession{
		state:          SignupIdle,
		client:         followClient,
		noRedirClient:  noRedirClient,
		deviceID:       deviceID,
		authLoggingID:  uuid.New().String(),
		userAgent:      userAgent,
		secChUA:        chromeProfile.SecChUA,
		chromeVer:      chromeVer,
		acceptLanguage: acceptLanguage,
		clientID:       clientID,
		proxyURL:       req.ProxyURL,
		onStep:         req.OnStep,
		providerParams: req.ProviderParams,
		originalReq:    req, // 保存原始请求用于重试
	}

	// Resolve email provider
	if req.EmailProvider != "" {
		p, err := mailprovider.NewProvider(req.EmailProvider)
		if err != nil {
			return nil, err
		}
		s.provider = p
	}

	// 处理邮箱：必须由前端提供（手动输入或随机生成后回填）
	if req.Email == "" {
		return nil, fmt.Errorf("email is required")
	}

	if s.provider != nil {
		if mailprovider.HasProviderState(req.ProviderParams) {
			// 邮箱已通过"随机生成"开通，验证 providerState 中的邮箱与请求邮箱一致
			stateEmail := req.ProviderParams["_email"]
			if stateEmail != "" && stateEmail != req.Email {
				return nil, fmt.Errorf("email mismatch: request=%s, providerState=%s", req.Email, stateEmail)
			}
			s.emitStep("1/10 Restoring email session...")
			s.provider.RestoreState(req.ProviderParams)
			s.email = req.Email
			log.Printf("[Signup] Restored provider state for email: %s", req.Email)
		} else {
			// 用户手动输入邮箱，需要调用 CreateEmail 开通
			s.state = SignupCreatingEmail
			s.emitStep("1/10 Setting up email mailbox...")
			req.ProviderParams["_email"] = req.Email
			createdEmail, _, err := s.provider.CreateEmail(req.ProviderParams)
			if err != nil {
				return nil, fmt.Errorf("setup email: %w", err)
			}
			// 使用 CreateEmail 返回的邮箱（可能被 provider 重写）
			s.email = createdEmail
			log.Printf("[Signup] Created email via provider: %s", createdEmail)
		}
	} else {
		s.email = req.Email
	}

	// Generate account password (separate from mail password)
	pwd := req.Password
	if pwd == "" {
		pwd = mailprovider.GeneratePassword(14)
	}
	s.password = pwd

	// Run the registration flow
	const maxRetries = 3
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			log.Printf("[Signup] Retry attempt %d/%d with new fingerprint", attempt, maxRetries)
			s.emitStep(fmt.Sprintf("Retrying with new browser fingerprint (attempt %d/%d)...", attempt, maxRetries))

			// 重新生成浏览器指纹
			if err := s.refreshFingerprint(ctx); err != nil {
				lastErr = fmt.Errorf("refresh fingerprint: %w", err)
				continue
			}
			randomDelay()
		}

		if err := s.runRegister(ctx); err != nil {
			// 检查是否是指纹被阻止的错误
			var fpErr *FingerprintBlockedError
			if errors.As(err, &fpErr) {
				log.Printf("[Signup] Fingerprint blocked (403), will retry with new fingerprint")
				lastErr = err
				continue
			}
			// 其他错误直接返回
			s.state = SignupError
			s.err = err
			return s, nil
		}

		// 成功，跳出重试循环
		break
	}

	// 如果所有重试都失败了
	if lastErr != nil {
		s.state = SignupError
		s.err = fmt.Errorf("failed after %d attempts: %w", maxRetries, lastErr)
		return s, nil
	}

	if s.state == SignupNeedOTP {
		return s, nil
	}

	// Registration complete, get Codex OAuth tokens
	if err := s.doOAuthLogin(ctx); err != nil {
		s.state = SignupError
		s.err = err
		return s, nil
	}

	return s, nil
}

// refreshFingerprint 重新生成浏览器指纹和 HTTP 客户端
func (s *SignupSession) refreshFingerprint(_ context.Context) error {
	// 生成新的浏览器指纹
	chromeProfile, chromeVer, userAgent := randomChromeVersion()
	log.Printf("[Signup] Refreshing fingerprint: Chrome %s (TLS profile: v%d)", chromeVer, chromeProfile.Major)

	// 随机选择新的 Accept-Language
	acceptLanguages := []string{
		"en-US,en;q=0.9",
		"en-US,en;q=0.9,zh-CN;q=0.8",
		"en,en-US;q=0.9",
		"en-US,en;q=0.8",
	}
	acceptLanguage := acceptLanguages[rand.Intn(len(acceptLanguages))]

	// 创建新的 cookie jar
	sharedJar := tls_client.NewCookieJar()

	// 创建新的 HTTP 客户端
	followClient, err := newTLSClientWithJar(s.proxyURL, true, chromeProfile.TLSProfile, sharedJar)
	if err != nil {
		return fmt.Errorf("create TLS client: %w", err)
	}
	noRedirClient, err := newTLSClientWithJar(s.proxyURL, false, chromeProfile.TLSProfile, sharedJar)
	if err != nil {
		return fmt.Errorf("create no-redirect TLS client: %w", err)
	}

	// 生成新的 device ID
	deviceID := uuid.New().String()

	// 设置 oai-did cookie
	chatgptURL, _ := url.Parse(chatgptBase)
	didCookie := []*fhttp.Cookie{{Name: "oai-did", Value: deviceID, Domain: "chatgpt.com"}}
	followClient.SetCookies(chatgptURL, didCookie)
	noRedirClient.SetCookies(chatgptURL, didCookie)

	// 更新 session 的指纹信息
	s.client = followClient
	s.noRedirClient = noRedirClient
	s.deviceID = deviceID
	s.authLoggingID = uuid.New().String()
	s.userAgent = userAgent
	s.secChUA = chromeProfile.SecChUA
	s.chromeVer = chromeVer
	s.acceptLanguage = acceptLanguage

	return nil
}

func (s *SignupSession) SubmitOTP(ctx context.Context, code string) error {
	code = strings.TrimSpace(code)
	if s.state != SignupNeedOTP {
		return fmt.Errorf("session not in NeedOTP state (current: %d)", s.state)
	}
	s.state = SignupValidatingOTP
	s.emitStep("7/10 Validating OTP...")
	if err := s.validateOTP(ctx, code); err != nil {
		s.state = SignupError
		s.err = err
		return err
	}

	s.emitStep("8/10 Creating profile...")
	if err := s.createProfile(ctx); err != nil {
		s.state = SignupError
		s.err = err
		return err
	}

	s.emitStep("9/10 Following callback...")
	if err := s.followCallback(ctx); err != nil {
		s.state = SignupError
		s.err = err
		return err
	}

	if err := s.doOAuthLogin(ctx); err != nil {
		s.state = SignupError
		s.err = err
		return err
	}
	return nil
}

// --- Registration flow steps ---

func (s *SignupSession) runRegister(ctx context.Context) error {
	s.state = SignupVisitingHomepage
	s.emitStep("2/10 Visiting chatgpt.com...")
	if err := s.visitHomepage(ctx); err != nil {
		return err
	}
	randomDelay()

	s.state = SignupGettingCSRF
	s.emitStep("3/10 Getting CSRF token...")
	csrf, err := s.getCSRF(ctx)
	if err != nil {
		return err
	}
	randomDelay()

	s.state = SignupSigningIn
	s.emitStep("4/10 Signing in...")
	authURL, err := s.signin(ctx, csrf)
	if err != nil {
		return err
	}
	randomDelay()

	s.state = SignupAuthorizing
	s.emitStep("5/10 Authorizing...")
	finalURL, err := s.authorize(ctx, authURL)
	if err != nil {
		return err
	}
	randomDelay()

	parsed, _ := url.Parse(finalURL)
	finalPath := ""
	if parsed != nil {
		finalPath = parsed.Path
	}
	log.Printf("[Signup] Authorize → %s", finalPath)

	switch {
	case strings.Contains(finalPath, "create-account/password"):
		s.state = SignupRegistering
		s.emitStep("5/10 Registering account...")
		if err := s.register(ctx); err != nil {
			return err
		}
		randomDelay()
		s.state = SignupSendingOTP
		s.emitStep("6/10 Sending OTP...")
		if err := s.sendOTP(ctx); err != nil {
			return err
		}
		return s.handleOTP(ctx)

	case strings.Contains(finalPath, "email-verification") || strings.Contains(finalPath, "email-otp"):
		log.Printf("[Signup] Redirected to OTP verification (server already sent OTP)")
		return s.handleOTP(ctx)

	case strings.Contains(finalPath, "about-you"):
		s.emitStep("8/10 Creating profile...")
		if err := s.createProfile(ctx); err != nil {
			return err
		}
		s.emitStep("9/10 Following callback...")
		if err := s.followCallback(ctx); err != nil {
			return err
		}
		return nil

	case strings.Contains(finalPath, "callback") || strings.Contains(finalURL, "chatgpt.com"):
		log.Printf("[Signup] Account already registered")
		return nil

	default:
		log.Printf("[Signup] Unknown redirect: %s, attempting register+OTP", finalURL)
		if err := s.register(ctx); err != nil {
			return err
		}
		if err := s.sendOTP(ctx); err != nil {
			return err
		}
		return s.handleOTP(ctx)
	}
}

func randomDelay() {
	time.Sleep(time.Duration(300+rand.Intn(500)) * time.Millisecond)
}

func (s *SignupSession) doGet(ctx context.Context, targetURL string, headers map[string]string) (*fhttp.Response, error) {
	req, err := fhttp.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return s.client.Do(req)
}

func (s *SignupSession) doPost(ctx context.Context, targetURL, body string, headers map[string]string) (*fhttp.Response, error) {
	req, err := fhttp.NewRequestWithContext(ctx, "POST", targetURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return s.client.Do(req)
}

func (s *SignupSession) doPostNoRedir(ctx context.Context, targetURL, body string, headers map[string]string) (*fhttp.Response, error) {
	req, err := fhttp.NewRequestWithContext(ctx, "POST", targetURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return s.noRedirClient.Do(req)
}

// commonHeaders returns base headers that should be included in all requests
func (s *SignupSession) commonHeaders() map[string]string {
	return map[string]string{
		"User-Agent":                  s.userAgent,
		"Accept-Language":             s.acceptLanguage,
		"sec-ch-ua":                   s.secChUA,
		"sec-ch-ua-mobile":            "?0",
		"sec-ch-ua-platform":          `"Windows"`,
		"sec-ch-ua-arch":              `"x86"`,
		"sec-ch-ua-bitness":           `"64"`,
		"sec-ch-ua-full-version":      fmt.Sprintf(`"%s"`, s.chromeVer),
		"sec-ch-ua-platform-version":  `"10.0.0"`,
	}
}

func (s *SignupSession) authHeaders() map[string]string {
	h := s.commonHeaders()
	h["Content-Type"] = "application/json"
	h["Accept"] = "application/json"
	h["Origin"] = oauthIssuer
	for k, v := range makeTraceHeaders() {
		h[k] = v
	}
	return h
}

func (s *SignupSession) visitHomepage(ctx context.Context) error {
	headers := s.commonHeaders()
	headers["Accept"] = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8"
	headers["Upgrade-Insecure-Requests"] = "1"
	resp, err := s.doGet(ctx, chatgptBase+"/", headers)
	if err != nil {
		return fmt.Errorf("visit homepage: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	finalURL := resp.Request.URL.String()
	log.Printf("[Signup] Homepage -> %d (final URL: %s)", resp.StatusCode, truncate(finalURL, 100))

	if resp.StatusCode == 403 {
		// 403 错误，需要换指纹重试
		contentType := resp.Header.Get("Content-Type")
		bodyPreview := truncate(string(body), 300)
		return &FingerprintBlockedError{
			StatusCode:  resp.StatusCode,
			ContentType: contentType,
			FinalURL:    finalURL,
			BodyPreview: bodyPreview,
		}
	}

	if resp.StatusCode >= 400 {
		contentType := resp.Header.Get("Content-Type")
		bodyPreview := truncate(string(body), 300)
		return fmt.Errorf("homepage returned %d (Content-Type: %s, final URL: %s, body preview: %s)",
			resp.StatusCode, contentType, truncate(finalURL, 100), bodyPreview)
	}
	return nil
}

func (s *SignupSession) getCSRF(ctx context.Context) (string, error) {
	headers := s.commonHeaders()
	headers["Accept"] = "application/json"
	headers["Referer"] = chatgptBase + "/"
	resp, err := s.doGet(ctx, chatgptBase+"/api/auth/csrf", headers)
	if err != nil {
		return "", fmt.Errorf("get CSRF: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		contentType := resp.Header.Get("Content-Type")
		finalURL := resp.Request.URL.String()
		bodyPreview := truncate(string(body), 300)
		return "", fmt.Errorf("CSRF API returned %d (Content-Type: %s, final URL: %s, body preview: %s)",
			resp.StatusCode, contentType, truncate(finalURL, 100), bodyPreview)
	}

	var data struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("parse CSRF: %w (body: %s)", err, truncate(string(body), 200))
	}
	if data.CSRFToken == "" {
		return "", fmt.Errorf("empty CSRF token (body: %s)", truncate(string(body), 200))
	}
	return data.CSRFToken, nil
}

func (s *SignupSession) signin(ctx context.Context, csrf string) (string, error) {
	params := url.Values{
		"prompt":                      {"login"},
		"ext-oai-did":                 {s.deviceID},
		"auth_session_logging_id":     {s.authLoggingID},
		"screen_hint":                 {"login_or_signup"},
		"login_hint":                  {s.email},
	}
	formData := url.Values{
		"callbackUrl": {chatgptBase + "/"},
		"csrfToken":   {csrf},
		"json":        {"true"},
	}
	headers := s.commonHeaders()
	headers["Content-Type"] = "application/x-www-form-urlencoded"
	headers["Accept"] = "application/json"
	headers["Referer"] = chatgptBase + "/"
	headers["Origin"] = chatgptBase
	targetURL := chatgptBase + "/api/auth/signin/openai?" + params.Encode()
	resp, err := s.doPost(ctx, targetURL, formData.Encode(), headers)
	if err != nil {
		return "", fmt.Errorf("signin: %w", err)
	}
	defer resp.Body.Close()
	var data struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("parse signin: %w", err)
	}
	if data.URL == "" {
		return "", fmt.Errorf("empty authorize URL from signin")
	}
	log.Printf("[Signup] Signin -> authorize URL obtained")
	return data.URL, nil
}

func (s *SignupSession) authorize(ctx context.Context, authURL string) (string, error) {
	headers := s.commonHeaders()
	headers["Accept"] = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
	headers["Referer"] = chatgptBase + "/"
	headers["Upgrade-Insecure-Requests"] = "1"
	resp, err := s.doGet(ctx, authURL, headers)
	if err != nil {
		return "", fmt.Errorf("authorize: %w", err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	finalURL := resp.Request.URL.String()
	log.Printf("[Signup] Authorize -> %d final=%s", resp.StatusCode, truncate(finalURL, 140))
	return finalURL, nil
}

func (s *SignupSession) register(ctx context.Context) error {
	headers := s.authHeaders()
	headers["Referer"] = oauthIssuer + "/create-account/password"
	body, _ := json.Marshal(map[string]string{"username": s.email, "password": s.password})
	resp, err := s.doPostNoRedir(ctx, oauthIssuer+"/api/accounts/user/register", string(body), headers)
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("[Signup] Register %s[%s]-> %d body=%s", s.email, s.password, resp.StatusCode, string(respBody))
	if resp.StatusCode != 200 {
		return fmt.Errorf("register failed (HTTP %d): %s", resp.StatusCode, truncate(string(respBody), 200))
	}
	return nil
}

func (s *SignupSession) sendOTP(ctx context.Context) error {
	headers := s.commonHeaders()
	headers["Accept"] = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
	headers["Referer"] = oauthIssuer + "/create-account/password"
	headers["Upgrade-Insecure-Requests"] = "1"
	resp, err := s.doGet(ctx, oauthIssuer+"/api/accounts/email-otp/send", headers)
	if err != nil {
		return fmt.Errorf("sendOTP request: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	log.Printf("[Signup] sendOTP -> %d", resp.StatusCode)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("sendOTP failed (HTTP %d): %s", resp.StatusCode, truncate(string(body), 200))
	}
	return nil
}

func (s *SignupSession) handleOTP(ctx context.Context) error {
	s.state = SignupWaitingOTP

	// Provider mode: try auto-fetch first
	if s.provider != nil {
		s.emitStep("6/10 Waiting for verification code from email provider...")
		code, err := s.provider.FetchVerificationCode(ctx, s.providerParams, s.email, 120)
		if err == nil && code != "" {
			log.Printf("[Signup] Auto-fetched OTP: %s", code)
			s.state = SignupValidatingOTP
			s.emitStep("7/10 Validating OTP...")
			if err := s.validateOTP(ctx, code); err != nil {
				log.Printf("[Signup] Auto OTP failed: %v, falling back to manual", err)
			} else {
				return s.afterOTPSuccess(ctx)
			}
		} else {
			log.Printf("[Signup] Auto-fetch OTP failed: %v, falling back to manual", err)
		}
	}

	// Manual mode or provider fallback
	s.state = SignupNeedOTP
	s.emitStep("6/10 Waiting for manual OTP input...")
	return nil
}

func (s *SignupSession) afterOTPSuccess(ctx context.Context) error {
	// 如果账号已存在，直接跳过 profile 创建和 callback
	if s.skipProfile {
		log.Printf("[Signup] Skipping profile creation for existing account")
		s.emitStep("8/10 Account already exists, skipping profile creation...")
		if s.callbackURL != "" {
			s.emitStep("9/10 Following callback...")
			if err := s.followCallback(ctx); err != nil {
				return err
			}
		}
		return nil
	}

	s.emitStep("8/10 Creating profile...")
	if err := s.createProfile(ctx); err != nil {
		return err
	}
	s.emitStep("9/10 Following callback...")
	if err := s.followCallback(ctx); err != nil {
		return err
	}
	return nil
}

func (s *SignupSession) validateOTP(ctx context.Context, code string) error {
	headers := s.authHeaders()
	headers["Referer"] = oauthIssuer + "/email-verification"
	body, _ := json.Marshal(map[string]string{"code": code})
	resp, err := s.doPostNoRedir(ctx, oauthIssuer+"/api/accounts/email-otp/validate", string(body), headers)
	if err != nil {
		return fmt.Errorf("validate OTP: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("[Signup] validateOTP -> %d body=%s", resp.StatusCode, string(respBody))
	if resp.StatusCode != 200 {
		return fmt.Errorf("OTP validation failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	// 解析响应，判断是否需要跳过 profile 创建
	var data struct {
		ContinueURL string `json:"continue_url"`
	}
	if json.Unmarshal(respBody, &data) == nil && data.ContinueURL != "" {
		// 如果 continue_url 包含 /api/auth/callback/openai，说明账号已存在，可以直接跳过 profile 创建
		if strings.Contains(data.ContinueURL, "/api/auth/callback/openai") {
			log.Printf("[Signup] Account already exists, skipping profile creation")
			s.skipProfile = true
			s.callbackURL = data.ContinueURL
		}
	}

	return nil
}

func (s *SignupSession) createProfile(ctx context.Context) error {
	s.state = SignupCreatingProfile
	headers := s.authHeaders()
	headers["Referer"] = oauthIssuer + "/about-you"
	body, _ := json.Marshal(map[string]string{
		"name":      randomName(),
		"birthdate": randomBirthdate(),
	})
	resp, err := s.doPostNoRedir(ctx, oauthIssuer+"/api/accounts/create_account", string(body), headers)
	if err != nil {
		return fmt.Errorf("create profile: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("[Signup] createProfile -> %d", resp.StatusCode)
	if resp.StatusCode != 200 {
		return fmt.Errorf("create profile failed (HTTP %d): %s", resp.StatusCode, truncate(string(respBody), 200))
	}
	var data map[string]any
	if json.Unmarshal(respBody, &data) == nil {
		if cb, ok := data["continue_url"].(string); ok && cb != "" {
			s.callbackURL = cb
		}
	}
	return nil
}

func (s *SignupSession) followCallback(ctx context.Context) error {
	s.state = SignupCallback
	cbURL := s.callbackURL
	if cbURL == "" {
		log.Printf("[Signup] No callback URL, skipping")
		return nil
	}
	headers := s.commonHeaders()
	headers["Accept"] = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
	headers["Upgrade-Insecure-Requests"] = "1"
	resp, err := s.doGet(ctx, cbURL, headers)
	if err != nil {
		return fmt.Errorf("callback: %w", err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	log.Printf("[Signup] Callback -> %d", resp.StatusCode)
	return nil
}

func (s *SignupSession) doOAuthLogin(ctx context.Context) error {
	s.state = SignupOAuthLogin
	s.emitStep("10/10 Getting Codex OAuth tokens...")

	loginReq := &HeadlessLoginRequest{
		Email:    s.email,
		Password: s.password,
		ClientID: s.clientID,
		ProxyURL: s.proxyURL,
		OnStep: func(msg string) {
			s.emitStep("[OAuth] " + msg)
		},
		// Reuse signup session's HTTP clients and browser fingerprint
		ReuseNoRedirectClient: s.noRedirClient,
		ReuseFollowClient:     s.client,
		ReuseDeviceID:         s.deviceID,
		ReuseUserAgent:        s.userAgent,
		ReuseSecChUA:          s.secChUA,
		ReuseChromeVer:        s.chromeVer,
	}

	session, err := StartHeadlessLogin(ctx, loginReq)
	if err != nil {
		return fmt.Errorf("OAuth login init: %w", err)
	}

	if session.State() == StateError {
		return fmt.Errorf("OAuth login: %v", session.Error())
	}

	if session.State() == StateNeedOTP {
		// Try auto-fetch OTP if provider is available
		if s.provider != nil {
			s.emitStep("[OAuth] Waiting for verification code from email provider...")
			code, err := s.provider.FetchVerificationCode(ctx, s.providerParams, s.email, 120)
			if err == nil && code != "" {
				log.Printf("[Signup] OAuth auto-fetched OTP: %s", code)
				s.emitStep("[OAuth] Validating OTP...")
				if err := session.SubmitOTP(ctx, code); err != nil {
					return fmt.Errorf("OAuth OTP validation failed: %w", err)
				}
				// OTP validated, continue to get result
			} else {
				return fmt.Errorf("OAuth login requires OTP but auto-fetch failed: %w", err)
			}
		} else {
			return fmt.Errorf("OAuth login requires OTP but no email provider configured")
		}
	}

	if session.Result() == nil {
		return fmt.Errorf("OAuth login: no result")
	}

	s.result = &SignupResult{
		CodexLoginResult: session.Result(),
		Password:         s.password,
	}
	s.state = SignupSuccess
	return nil
}

// --- Helpers ---

var firstNames = []string{
	"James", "Emma", "Liam", "Olivia", "Noah", "Ava", "Ethan", "Sophia",
	"Lucas", "Mia", "Mason", "Isabella", "Logan", "Charlotte", "Alexander",
	"Amelia", "Benjamin", "Harper", "William", "Evelyn", "Henry", "Abigail",
	"Sebastian", "Emily", "Jack", "Elizabeth",
}

var lastNames = []string{
	"Smith", "Johnson", "Brown", "Davis", "Wilson", "Moore", "Taylor",
	"Clark", "Hall", "Young", "Anderson", "Thomas", "Jackson", "White",
	"Harris", "Martin", "Thompson", "Garcia", "Robinson", "Lewis",
	"Walker", "Allen", "King", "Wright", "Scott", "Green",
}

func randomName() string {
	return firstNames[rand.Intn(len(firstNames))] + " " + lastNames[rand.Intn(len(lastNames))]
}

func randomBirthdate() string {
	y := 1985 + rand.Intn(18)
	m := 1 + rand.Intn(12)
	d := 1 + rand.Intn(28)
	return fmt.Sprintf("%d-%02d-%02d", y, m, d)
}
