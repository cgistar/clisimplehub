package auth

import (
	"context"
	"encoding/json"
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
	err            error
	result         *SignupResult
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
	}

	// Resolve email provider
	if req.EmailProvider != "" {
		p, err := mailprovider.NewProvider(req.EmailProvider)
		if err != nil {
			return nil, err
		}
		s.provider = p
	}

	// Create or use provided email
	if s.provider != nil && req.Email == "" {
		s.state = SignupCreatingEmail
		s.emitStep("1/10 Creating temporary email...")
		email, _, err := s.provider.CreateEmail(req.ProviderParams)
		if err != nil {
			return nil, fmt.Errorf("create email: %w", err)
		}
		s.email = email
		log.Printf("[Signup] Created email: %s", email)
	} else {
		if req.Email == "" {
			return nil, fmt.Errorf("email is required in manual mode")
		}
		s.email = req.Email
	}

	// Generate account password (separate from mail password)
	pwd := req.Password
	if pwd == "" {
		pwd = mailprovider.GeneratePassword(14)
	}
	s.password = pwd

	// Run the registration flow
	if err := s.runRegister(ctx); err != nil {
		s.state = SignupError
		s.err = err
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
		return s.followCallback(ctx)

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
	io.ReadAll(resp.Body)
	resp.Body.Close()
	log.Printf("[Signup] Homepage -> %d", resp.StatusCode)
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
	var data struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("parse CSRF: %w", err)
	}
	if data.CSRFToken == "" {
		return "", fmt.Errorf("empty CSRF token")
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
	log.Printf("[Signup] Register -> %d body=%s", resp.StatusCode, truncate(string(respBody), 300))
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
	s.emitStep("8/10 Creating profile...")
	if err := s.createProfile(ctx); err != nil {
		return err
	}
	s.emitStep("9/10 Following callback...")
	return s.followCallback(ctx)
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
	log.Printf("[Signup] validateOTP -> %d body=%s", resp.StatusCode, truncate(string(respBody), 300))
	if resp.StatusCode != 200 {
		return fmt.Errorf("OTP validation failed (HTTP %d): %s", resp.StatusCode, truncate(string(respBody), 200))
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
	}

	session, err := StartHeadlessLogin(ctx, loginReq)
	if err != nil {
		return fmt.Errorf("OAuth login init: %w", err)
	}

	if session.State() == StateError {
		return fmt.Errorf("OAuth login: %v", session.Error())
	}

	if session.State() == StateNeedOTP {
		return fmt.Errorf("OAuth login requires OTP (unexpected for new account)")
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
