package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/google/uuid"
)

type HeadlessLoginState int

const (
	StateIdle HeadlessLoginState = iota
	StateBootstrapping
	StateSubmittingEmail
	StateVerifyingPassword
	StateNeedOTP
	StateValidatingOTP
	StateExtractingCode
	StateExchangingTokens
	StateSuccess
	StateError
)

const (
	oauthIssuer = "https://auth.openai.com"
)

// ChromeProfile represents a Chrome browser version configuration for TLS fingerprinting
type ChromeProfile struct {
	Major      int
	Build      int
	PatchMin   int
	PatchMax   int
	SecChUA    string
	TLSProfile profiles.ClientProfile
}

// chromeProfiles mirrors Python's _CHROME_PROFILES for randomized browser fingerprinting
var chromeProfiles = []ChromeProfile{
	{
		Major:      131,
		Build:      6778,
		PatchMin:   69,
		PatchMax:   205,
		SecChUA:    `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
		TLSProfile: profiles.Chrome_131,
	},
	{
		Major:      133,
		Build:      6943,
		PatchMin:   33,
		PatchMax:   153,
		SecChUA:    `"Not(A:Brand";v="99", "Google Chrome";v="133", "Chromium";v="133"`,
		TLSProfile: profiles.Chrome_133,
	},
	{
		Major:      144,
		Build:      7540,
		PatchMin:   30,
		PatchMax:   150,
		SecChUA:    `"Chromium";v="144", "Google Chrome";v="144", "Not_A Brand";v="99"`,
		TLSProfile: profiles.Chrome_144,
	},
	{
		Major:      146,
		Build:      7540,
		PatchMin:   30,
		PatchMax:   150,
		SecChUA:    `"Chromium";v="146", "Google Chrome";v="146", "Not_A Brand";v="99"`,
		TLSProfile: profiles.Chrome_146,
	},
}

// randomChromeVersion selects a random Chrome profile and generates a full version string
func randomChromeVersion() (profile ChromeProfile, fullVersion string, userAgent string) {
	profile = chromeProfiles[rand.Intn(len(chromeProfiles))]
	patch := rand.Intn(profile.PatchMax-profile.PatchMin+1) + profile.PatchMin
	fullVersion = fmt.Sprintf("%d.0.%d.%d", profile.Major, profile.Build, patch)
	userAgent = fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Safari/537.36", fullVersion)
	return
}

type HeadlessLoginRequest struct {
	Email    string
	Password string
	ClientID string
	ProxyURL string
	OnStep   func(msg string) // optional progress callback
}

type HeadlessLoginSession struct {
	state        HeadlessLoginState
	client       tls_client.HttpClient // no-redirect client
	followClient tls_client.HttpClient // follow-redirect client
	httpClient   *http.Client          // standard client for sentinel calls
	deviceID     string
	userAgent    string
	secChUA      string
	chromeVer    string // full Chrome version (e.g., "146.0.7540.87")
	pkce         *PKCECodes
	oauthState   string
	continueURL  string
	clientID     string
	email        string
	password     string
	proxyURL     string
	onStep       func(msg string)
	err          error
	result       *CodexLoginResult
}

func newTLSClient(proxyURL string, followRedirects bool, profile profiles.ClientProfile) (tls_client.HttpClient, error) {
	options := []tls_client.HttpClientOption{
		tls_client.WithClientProfile(profile),
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithCookieJar(tls_client.NewCookieJar()),
	}
	if !followRedirects {
		options = append(options, tls_client.WithNotFollowRedirects())
	}
	if proxyURL != "" {
		options = append(options, tls_client.WithProxyUrl(proxyURL))
	}
	return tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
}

func StartHeadlessLogin(ctx context.Context, req *HeadlessLoginRequest) (*HeadlessLoginSession, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}
	if req.Email == "" || req.Password == "" {
		return nil, fmt.Errorf("email and password are required")
	}

	clientID := req.ClientID
	if clientID == "" {
		clientID = ClientID
	}

	deviceID := uuid.New().String()

	// Randomize Chrome version for anti-detection
	chromeProfile, chromeVer, userAgent := randomChromeVersion()
	log.Printf("[HeadlessLogin] Using Chrome %s (TLS profile: v%d)", chromeVer, chromeProfile.Major)

	noRedirectClient, err := newTLSClient(req.ProxyURL, false, chromeProfile.TLSProfile)
	if err != nil {
		return nil, fmt.Errorf("create TLS client: %w", err)
	}
	followClient, err := newTLSClient(req.ProxyURL, true, chromeProfile.TLSProfile)
	if err != nil {
		return nil, fmt.Errorf("create follow-redirect TLS client: %w", err)
	}

	// Standard HTTP client for sentinel API calls
	stdTransport := &http.Transport{}
	if req.ProxyURL != "" {
		if proxyU, err := url.Parse(req.ProxyURL); err == nil {
			stdTransport.Proxy = http.ProxyURL(proxyU)
		}
	}
	stdClient := &http.Client{Transport: stdTransport, Timeout: 20 * time.Second}

	pkce, err := GeneratePKCECodes()
	if err != nil {
		return nil, fmt.Errorf("PKCE generation: %w", err)
	}

	state, err := GenerateState()
	if err != nil {
		return nil, fmt.Errorf("state generation: %w", err)
	}

	s := &HeadlessLoginSession{
		state:        StateIdle,
		client:       noRedirectClient,
		followClient: followClient,
		httpClient:   stdClient,
		deviceID:     deviceID,
		userAgent:    userAgent,
		secChUA:      chromeProfile.SecChUA,
		chromeVer:    chromeVer,
		pkce:         pkce,
		oauthState:   state,
		clientID:     clientID,
		email:        req.Email,
		password:     req.Password,
		proxyURL:     req.ProxyURL,
		onStep:       req.OnStep,
	}

	// Set oai-did cookie on auth domain for both clients
	cookieURL, _ := url.Parse(oauthIssuer)
	didCookie := []*fhttp.Cookie{
		{Name: "oai-did", Value: deviceID, Domain: ".auth.openai.com"},
	}
	noRedirectClient.SetCookies(cookieURL, didCookie)
	followClient.SetCookies(cookieURL, didCookie)

	// Step 1-3
	if err := s.bootstrapOAuthSession(ctx); err != nil {
		s.state = StateError
		s.err = err
		return s, nil
	}

	if err := s.submitEmail(ctx); err != nil {
		s.state = StateError
		s.err = err
		return s, nil
	}

	if err := s.verifyPassword(ctx); err != nil {
		s.state = StateError
		s.err = err
		return s, nil
	}

	if s.state == StateNeedOTP {
		return s, nil
	}

	if err := s.extractAndExchange(ctx); err != nil {
		s.state = StateError
		s.err = err
		return s, nil
	}

	return s, nil
}

func (s *HeadlessLoginSession) SubmitOTP(ctx context.Context, code string) error {
	code = strings.TrimSpace(code)
	if s.state != StateNeedOTP {
		return fmt.Errorf("session not in NeedOTP state (current: %d)", s.state)
	}

	s.state = StateValidatingOTP
	s.emitStep("4/7 Validating OTP code...")

	log.Printf("[HeadlessLogin] SubmitOTP code=%q len=%d", code, len(code))
	s.logCookieState("before OTP validate")

	headers := s.oauthJSONHeaders(oauthIssuer + "/email-verification")

	// Add sentinel token — OTP endpoint may require it like other auth endpoints
	sentinelToken, err := s.buildSentinelToken("email_otp_validate")
	if err != nil {
		log.Printf("[HeadlessLogin] sentinel for OTP failed: %v, trying without", err)
	} else {
		headers["openai-sentinel-token"] = sentinelToken
	}

	body := map[string]string{"code": code}
	bodyBytes, _ := json.Marshal(body)

	resp, err := s.doPost(ctx, oauthIssuer+"/api/accounts/email-otp/validate", string(bodyBytes), headers)
	if err != nil {
		s.state = StateError
		s.err = fmt.Errorf("OTP validate: %w", err)
		return s.err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("[HeadlessLogin] /email-otp/validate -> %d body=%s", resp.StatusCode, truncate(string(respBody), 300))

	if resp.StatusCode != 200 {
		s.state = StateNeedOTP
		return fmt.Errorf("OTP validation failed (HTTP %d): %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var data struct {
		ContinueURL string `json:"continue_url"`
	}
	if err := json.Unmarshal(respBody, &data); err == nil && data.ContinueURL != "" {
		s.continueURL = data.ContinueURL
	}

	if err := s.extractAndExchange(ctx); err != nil {
		s.state = StateError
		s.err = err
		return err
	}
	return nil
}

func (s *HeadlessLoginSession) State() HeadlessLoginState { return s.state }
func (s *HeadlessLoginSession) Result() *CodexLoginResult { return s.result }
func (s *HeadlessLoginSession) Error() error              { return s.err }

func (s *HeadlessLoginSession) emitStep(msg string) {
	log.Printf("[HeadlessLogin] %s", msg)
	if s.onStep != nil {
		s.onStep(msg)
	}
}

func (s *HeadlessLoginSession) logCookieState(label string) {
	cookieURL, _ := url.Parse(oauthIssuer)
	cookies := s.client.GetCookies(cookieURL)
	var names []string
	hasSession := false
	hasCF := false
	for _, c := range cookies {
		names = append(names, c.Name)
		if c.Name == "login_session" || c.Name == "oai-client-auth-session" {
			hasSession = true
		}
		if c.Name == "__cf_bm" || c.Name == "_cfuvid" {
			hasCF = true
		}
	}
	log.Printf("[HeadlessLogin] cookies(%s): count=%d hasSession=%v hasCF=%v names=%v",
		label, len(cookies), hasSession, hasCF, names)
}

// --- Internal steps ---

func (s *HeadlessLoginSession) bootstrapOAuthSession(ctx context.Context) error {
	s.state = StateBootstrapping

	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {s.clientID},
		"redirect_uri":          {RedirectURI},
		"scope":                 {"openid profile email offline_access"},
		"code_challenge":        {s.pkce.CodeChallenge},
		"code_challenge_method": {"S256"},
		"state":                 {s.oauthState},
	}
	authorizeURL := oauthIssuer + "/oauth/authorize?" + params.Encode()

	headers := map[string]string{
		"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		"Referer":                   "https://openai.com/",
		"Upgrade-Insecure-Requests": "1",
		"User-Agent":                s.userAgent,
	}

	log.Printf("[HeadlessLogin] 1/7 GET /oauth/authorize")
	s.emitStep("1/7 Bootstrapping OAuth session...")

	// Use follow-redirect client for bootstrap, then sync cookies
	resp, err := s.doGetFollow(ctx, authorizeURL, headers)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	s.syncCookies()
	log.Printf("[HeadlessLogin] /oauth/authorize -> %d", resp.StatusCode)
	return nil
}

func (s *HeadlessLoginSession) submitEmail(ctx context.Context) error {
	s.state = StateSubmittingEmail

	sentinelToken, err := s.buildSentinelToken("authorize_continue")
	if err != nil {
		return fmt.Errorf("sentinel for email: %w", err)
	}

	referer := oauthIssuer + "/log-in"
	headers := s.oauthJSONHeaders(referer)
	headers["openai-sentinel-token"] = sentinelToken

	body := map[string]any{
		"username": map[string]string{"kind": "email", "value": s.email},
	}
	bodyBytes, _ := json.Marshal(body)

	log.Printf("[HeadlessLogin] 2/7 POST /api/accounts/authorize/continue")
	s.emitStep("2/7 Submitting email...")
	resp, err := s.doPost(ctx, oauthIssuer+"/api/accounts/authorize/continue", string(bodyBytes), headers)
	if err != nil {
		return fmt.Errorf("submit email: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	log.Printf("[HeadlessLogin] /authorize/continue -> %d", resp.StatusCode)

	if resp.StatusCode == 400 && strings.Contains(string(respBody), "invalid_auth_step") {
		log.Printf("[HeadlessLogin] invalid_auth_step, re-bootstrap")
		if err := s.bootstrapOAuthSession(ctx); err != nil {
			return err
		}
		sentinelToken, err = s.buildSentinelToken("authorize_continue")
		if err != nil {
			return fmt.Errorf("sentinel retry: %w", err)
		}
		headers["openai-sentinel-token"] = sentinelToken
		resp2, err := s.doPost(ctx, oauthIssuer+"/api/accounts/authorize/continue", string(bodyBytes), headers)
		if err != nil {
			return fmt.Errorf("submit email retry: %w", err)
		}
		defer resp2.Body.Close()
		respBody, _ = io.ReadAll(resp2.Body)
		resp = resp2
		log.Printf("[HeadlessLogin] /authorize/continue(retry) -> %d", resp.StatusCode)
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("email submit failed (HTTP %d): %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var data struct {
		ContinueURL string `json:"continue_url"`
	}
	if err := json.Unmarshal(respBody, &data); err == nil {
		s.continueURL = data.ContinueURL
	}
	return nil
}

func (s *HeadlessLoginSession) verifyPassword(ctx context.Context) error {
	s.state = StateVerifyingPassword

	sentinelToken, err := s.buildSentinelToken("password_verify")
	if err != nil {
		return fmt.Errorf("sentinel for password: %w", err)
	}

	headers := s.oauthJSONHeaders(oauthIssuer + "/log-in/password")
	headers["openai-sentinel-token"] = sentinelToken

	body := map[string]string{"password": s.password}
	bodyBytes, _ := json.Marshal(body)

	log.Printf("[HeadlessLogin] 3/7 POST /api/accounts/password/verify")
	s.emitStep("3/7 Verifying password...")
	resp, err := s.doPost(ctx, oauthIssuer+"/api/accounts/password/verify", string(bodyBytes), headers)
	if err != nil {
		return fmt.Errorf("verify password: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	log.Printf("[HeadlessLogin] /password/verify -> %d", resp.StatusCode)

	if resp.StatusCode != 200 {
		return fmt.Errorf("password verify failed (HTTP %d): %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var data struct {
		ContinueURL string `json:"continue_url"`
		Page        struct {
			Type string `json:"type"`
		} `json:"page"`
	}
	if err := json.Unmarshal(respBody, &data); err == nil {
		if data.ContinueURL != "" {
			s.continueURL = data.ContinueURL
		}
	}

	pageType := data.Page.Type
	needOTP := pageType == "email_otp_verification" ||
		strings.Contains(s.continueURL, "email-verification") ||
		strings.Contains(s.continueURL, "email-otp")

	// Clear password from memory immediately after verification
	s.password = ""

	if needOTP {
		log.Printf("[HeadlessLogin] 4/7 OTP required")
		s.emitStep("4/7 Email OTP verification required")
		s.state = StateNeedOTP
	}
	return nil
}

func (s *HeadlessLoginSession) extractAndExchange(ctx context.Context) error {
	s.state = StateExtractingCode
	s.emitStep("5/7 Extracting authorization code...")

	code := s.extractAuthCode(ctx)
	if code == "" {
		return fmt.Errorf("failed to extract authorization code")
	}

	s.emitStep("7/7 Exchanging tokens...")
	log.Printf("[HeadlessLogin] 7/7 exchanging tokens")
	s.state = StateExchangingTokens
	result, err := s.exchangeTokens(ctx, code)
	if err != nil {
		return fmt.Errorf("token exchange: %w", err)
	}

	s.result = result
	s.state = StateSuccess
	return nil
}

func (s *HeadlessLoginSession) extractAuthCode(ctx context.Context) string {
	continueURL := s.continueURL
	if continueURL == "" {
		continueURL = oauthIssuer + "/sign-in-with-chatgpt/codex/consent"
	}
	if strings.HasPrefix(continueURL, "/") {
		continueURL = oauthIssuer + continueURL
	}

	log.Printf("[HeadlessLogin] extractAuthCode continue_url=%s", truncate(continueURL, 140))

	if code := extractCodeFromURL(continueURL); code != "" {
		return code
	}

	log.Printf("[HeadlessLogin] 5/7 following continue_url for code")
	code := s.followForCode(ctx, continueURL, oauthIssuer+"/log-in/password", 16)
	if code != "" {
		return code
	}

	// Fallback: allow-redirect extraction (follows all redirects, captures code from error or final URL)
	code = s.allowRedirectExtractCode(ctx, continueURL, oauthIssuer+"/log-in/password")
	if code != "" {
		return code
	}

	isConsentHint := strings.Contains(continueURL, "consent") ||
		strings.Contains(continueURL, "sign-in-with-chatgpt") ||
		strings.Contains(continueURL, "workspace") ||
		strings.Contains(continueURL, "organization")

	if isConsentHint {
		log.Printf("[HeadlessLogin] 6/7 workspace/org selection")
		code = s.submitWorkspaceAndOrg(ctx, continueURL)
		if code != "" {
			return code
		}
	}

	fallbackConsent := oauthIssuer + "/sign-in-with-chatgpt/codex/consent"
	log.Printf("[HeadlessLogin] 6/7 fallback consent path")
	code = s.submitWorkspaceAndOrg(ctx, fallbackConsent)
	if code != "" {
		return code
	}
	code = s.followForCode(ctx, fallbackConsent, oauthIssuer+"/log-in/password", 16)
	if code != "" {
		return code
	}
	return s.allowRedirectExtractCode(ctx, fallbackConsent, oauthIssuer+"/log-in/password")
}

func (s *HeadlessLoginSession) followForCode(ctx context.Context, startURL, referer string, maxHops int) string {
	headers := map[string]string{
		"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		"Upgrade-Insecure-Requests": "1",
		"User-Agent":                s.userAgent,
	}
	if referer != "" {
		headers["Referer"] = referer
	}

	currentURL := startURL
	for hop := range maxHops {
		resp, err := s.doGet(ctx, currentURL, headers)
		if err != nil {
			if code := extractCodeFromError(err); code != "" {
				log.Printf("[HeadlessLogin] follow[%d] extracted code from localhost redirect", hop+1)
				return code
			}
			log.Printf("[HeadlessLogin] follow[%d] request error: %v", hop+1, err)
			return ""
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()

		log.Printf("[HeadlessLogin] follow[%d] %d %s", hop+1, resp.StatusCode, truncate(currentURL, 140))

		if resp.StatusCode >= 301 && resp.StatusCode <= 308 {
			loc := resp.Header.Get("Location")
			if loc == "" {
				return ""
			}
			if strings.HasPrefix(loc, "/") {
				loc = oauthIssuer + loc
			}
			if code := extractCodeFromURL(loc); code != "" {
				return code
			}
			// Stop if redirecting to localhost (can't actually reach it)
			if strings.HasPrefix(loc, "http://localhost") {
				return ""
			}
			currentURL = loc
			headers["Referer"] = currentURL
			continue
		}
		log.Printf("[HeadlessLogin] follow[%d] non-redirect response, stopping", hop+1)
		return ""
	}
	return ""
}

func (s *HeadlessLoginSession) submitWorkspaceAndOrg(ctx context.Context, consentURL string) string {
	// Read session cookie directly (like Python) — do NOT GET the consent URL first
	sessionData := s.decodeOAuthSessionCookie()
	if sessionData == nil {
		// Log available cookies for debugging
		cookieURL, _ := url.Parse(oauthIssuer)
		var names []string
		for _, c := range s.client.GetCookies(cookieURL) {
			names = append(names, c.Name)
		}
		log.Printf("[HeadlessLogin] no oai-client-auth-session cookie, available: %v", names)
		return ""
	}

	workspaces, _ := sessionData["workspaces"].([]any)
	if len(workspaces) == 0 {
		log.Printf("[HeadlessLogin] no workspaces in session cookie")
		return ""
	}
	ws0, _ := workspaces[0].(map[string]any)
	workspaceID, _ := ws0["id"].(string)
	if workspaceID == "" {
		log.Printf("[HeadlessLogin] workspace_id is empty")
		return ""
	}

	log.Printf("[HeadlessLogin] selecting workspace: %s", workspaceID)
	h := s.oauthJSONHeaders(consentURL)
	body := map[string]string{"workspace_id": workspaceID}
	bodyJSON, _ := json.Marshal(body)

	wsResp, err := s.doPost(ctx, oauthIssuer+"/api/accounts/workspace/select", string(bodyJSON), h)
	if err != nil {
		log.Printf("[HeadlessLogin] workspace/select error: %v", err)
		return ""
	}
	wsBody, _ := io.ReadAll(wsResp.Body)
	wsResp.Body.Close()

	log.Printf("[HeadlessLogin] workspace/select -> %d", wsResp.StatusCode)

	if wsResp.StatusCode >= 301 && wsResp.StatusCode <= 308 {
		loc := wsResp.Header.Get("Location")
		if strings.HasPrefix(loc, "/") {
			loc = oauthIssuer + loc
		}
		if code := extractCodeFromURL(loc); code != "" {
			return code
		}
		if code := s.followForCode(ctx, loc, consentURL, 16); code != "" {
			return code
		}
		return s.allowRedirectExtractCode(ctx, loc, consentURL)
	}

	if wsResp.StatusCode != 200 {
		log.Printf("[HeadlessLogin] workspace/select unexpected status: %d body=%s", wsResp.StatusCode, truncate(string(wsBody), 200))
		return ""
	}

	var wsData struct {
		ContinueURL string `json:"continue_url"`
		Page        struct {
			Type string `json:"type"`
		} `json:"page"`
		Data struct {
			Orgs []struct {
				ID       string `json:"id"`
				Projects []struct {
					ID string `json:"id"`
				} `json:"projects"`
			} `json:"orgs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(wsBody, &wsData); err != nil {
		log.Printf("[HeadlessLogin] workspace/select JSON parse error: %v", err)
		return ""
	}

	log.Printf("[HeadlessLogin] workspace/select page=%s next=%s orgs=%d",
		wsData.Page.Type, truncate(wsData.ContinueURL, 140), len(wsData.Data.Orgs))

	if len(wsData.Data.Orgs) > 0 {
		orgID := wsData.Data.Orgs[0].ID
		orgBody := map[string]string{"org_id": orgID}
		if len(wsData.Data.Orgs[0].Projects) > 0 {
			orgBody["project_id"] = wsData.Data.Orgs[0].Projects[0].ID
		}

		orgReferer := wsData.ContinueURL
		if orgReferer != "" && strings.HasPrefix(orgReferer, "/") {
			orgReferer = oauthIssuer + orgReferer
		}
		if orgReferer == "" {
			orgReferer = consentURL
		}

		hOrg := s.oauthJSONHeaders(orgReferer)
		orgJSON, _ := json.Marshal(orgBody)

		orgResp, err := s.doPost(ctx, oauthIssuer+"/api/accounts/organization/select", string(orgJSON), hOrg)
		if err == nil {
			orgRespBody, _ := io.ReadAll(orgResp.Body)
			orgResp.Body.Close()
			log.Printf("[HeadlessLogin] organization/select -> %d", orgResp.StatusCode)

			if orgResp.StatusCode >= 301 && orgResp.StatusCode <= 308 {
				loc := orgResp.Header.Get("Location")
				if strings.HasPrefix(loc, "/") {
					loc = oauthIssuer + loc
				}
				if code := extractCodeFromURL(loc); code != "" {
					return code
				}
				if code := s.followForCode(ctx, loc, orgReferer, 16); code != "" {
					return code
				}
				return s.allowRedirectExtractCode(ctx, loc, orgReferer)
			}

			if orgResp.StatusCode == 200 {
				var orgData struct {
					ContinueURL string `json:"continue_url"`
				}
				if json.Unmarshal(orgRespBody, &orgData) == nil && orgData.ContinueURL != "" {
					nextURL := orgData.ContinueURL
					if strings.HasPrefix(nextURL, "/") {
						nextURL = oauthIssuer + nextURL
					}
					if code := s.followForCode(ctx, nextURL, orgReferer, 16); code != "" {
						return code
					}
					return s.allowRedirectExtractCode(ctx, nextURL, orgReferer)
				}
			}
		}
	}

	if wsData.ContinueURL != "" {
		next := wsData.ContinueURL
		if strings.HasPrefix(next, "/") {
			next = oauthIssuer + next
		}
		if code := s.followForCode(ctx, next, consentURL, 16); code != "" {
			return code
		}
		return s.allowRedirectExtractCode(ctx, next, consentURL)
	}

	return ""
}

// allowRedirectExtractCode uses the auto-redirect client to follow all redirects.
// Extracts the code from either the final URL or the connection error to localhost.
// This mirrors Python's _oauth_allow_redirect_extract_code.
func (s *HeadlessLoginSession) allowRedirectExtractCode(ctx context.Context, targetURL, referer string) string {
	s.syncCookiesReverse()
	headers := map[string]string{
		"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		"Upgrade-Insecure-Requests": "1",
		"User-Agent":                s.userAgent,
	}
	if referer != "" {
		headers["Referer"] = referer
	}

	resp, err := s.doGetFollow(ctx, targetURL, headers)
	if err != nil {
		errStr := err.Error()
		log.Printf("[HeadlessLogin] allowRedirect error: %s", truncate(errStr, 300))
		if code := extractCodeFromError(err); code != "" {
			log.Printf("[HeadlessLogin] allowRedirect extracted code from error")
			return code
		}
		return ""
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	s.syncCookies()

	// Check final URL (after all redirects)
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL := resp.Request.URL.String()
		if code := extractCodeFromURL(finalURL); code != "" {
			log.Printf("[HeadlessLogin] allowRedirect extracted code from final URL")
			return code
		}
		log.Printf("[HeadlessLogin] allowRedirect no code found, final=%s status=%d", truncate(finalURL, 140), resp.StatusCode)
	} else {
		log.Printf("[HeadlessLogin] allowRedirect no code found, status=%d", resp.StatusCode)
	}
	return ""
}

func (s *HeadlessLoginSession) decodeOAuthSessionCookie() map[string]any {
	cookieURL, _ := url.Parse(oauthIssuer)
	for _, c := range s.client.GetCookies(cookieURL) {
		if c.Name != "oai-client-auth-session" {
			continue
		}

		rawVal := c.Value
		if rawVal == "" {
			continue
		}

		// URL-decode the value (cookie may be percent-encoded)
		if unescaped, err := url.QueryUnescape(rawVal); err == nil && unescaped != rawVal {
			rawVal = unescaped
		}

		// Strip surrounding quotes
		if len(rawVal) >= 2 &&
			((rawVal[0] == '"' && rawVal[len(rawVal)-1] == '"') ||
				(rawVal[0] == '\'' && rawVal[len(rawVal)-1] == '\'')) {
			rawVal = rawVal[1 : len(rawVal)-1]
		}

		// JWT-like format: take the first segment before '.'
		part := rawVal
		if idx := strings.Index(rawVal, "."); idx > 0 {
			part = rawVal[:idx]
		}

		// Pad base64 to multiple of 4
		if pad := len(part) % 4; pad != 0 {
			part += strings.Repeat("=", 4-pad)
		}

		// Decode with URL-safe base64
		decoded, err := base64.URLEncoding.DecodeString(part)
		if err != nil {
			decoded, err = base64.RawURLEncoding.DecodeString(part)
			if err != nil {
				// Fallback: try standard base64
				decoded, err = base64.StdEncoding.DecodeString(part)
				if err != nil {
					log.Printf("[HeadlessLogin] decodeSessionCookie base64 failed: %v val_prefix=%s", err, truncate(part, 40))
					continue
				}
			}
		}

		var data map[string]any
		if json.Unmarshal(decoded, &data) == nil {
			return data
		}
		log.Printf("[HeadlessLogin] decodeSessionCookie JSON parse failed, decoded_prefix=%s", truncate(string(decoded), 80))
	}
	return nil
}

func (s *HeadlessLoginSession) exchangeTokens(ctx context.Context, code string) (*CodexLoginResult, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {s.clientID},
		"code":          {code},
		"redirect_uri":  {RedirectURI},
		"code_verifier": {s.pkce.CodeVerifier},
	}

	headers := map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
		"User-Agent":   s.userAgent,
	}

	resp, err := s.doPost(ctx, oauthIssuer+"/oauth/token", data.Encode(), headers)
	if err != nil {
		return nil, fmt.Errorf("token exchange request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	log.Printf("[HeadlessLogin] /oauth/token -> %d", resp.StatusCode)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token exchange failed (HTTP %d): %s", resp.StatusCode, truncate(string(body), 200))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("token response missing access_token")
	}

	result := &CodexLoginResult{
		RefreshToken: tokenResp.RefreshToken,
		AccessToken:  tokenResp.AccessToken,
		IDToken:      tokenResp.IDToken,
	}

	expiresIn := tokenResp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	result.ExpiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339)

	if tokenResp.IDToken != "" {
		if claims, parseErr := ParseJWTToken(tokenResp.IDToken); parseErr == nil {
			result.Email = claims.Email
			result.AccountID = claims.CodexAuth.ChatgptAccountID
			result.PlanType = claims.CodexAuth.ChatgptPlanType
		}
	}

	return result, nil
}

// --- Helpers ---

func (s *HeadlessLoginSession) buildSentinelToken(flow string) (string, error) {
	return BuildSentinelToken(s.httpClient, s.deviceID, flow, s.userAgent, s.secChUA)
}

func (s *HeadlessLoginSession) oauthJSONHeaders(referer string) map[string]string {
	h := map[string]string{
		"Accept":             "application/json",
		"Content-Type":       "application/json",
		"Origin":             oauthIssuer,
		"Referer":            referer,
		"User-Agent":         s.userAgent,
		"oai-device-id":      s.deviceID,
		"sec-ch-ua":          s.secChUA,
		"sec-ch-ua-mobile":   "?0",
		"sec-ch-ua-platform": `"Windows"`,
		"sec-fetch-dest":     "empty",
		"sec-fetch-mode":     "cors",
		"sec-fetch-site":     "same-origin",
	}
	for k, v := range makeTraceHeaders() {
		h[k] = v
	}
	return h
}

func makeTraceHeaders() map[string]string {
	traceID := rand.Int63n(900000000000000000) + 100000000000000000
	parentID := rand.Int63n(900000000000000000) + 100000000000000000
	// W3C trace context requires 32 hex chars (no hyphens)
	traceIDHex := strings.ReplaceAll(uuid.New().String(), "-", "")
	tp := fmt.Sprintf("00-%s-%016x-01", traceIDHex, parentID)
	return map[string]string{
		"traceparent":                 tp,
		"tracestate":                  "dd=s:1;o:rum",
		"x-datadog-origin":            "rum",
		"x-datadog-sampling-priority": "1",
		"x-datadog-trace-id":          fmt.Sprintf("%d", traceID),
		"x-datadog-parent-id":         fmt.Sprintf("%d", parentID),
	}
}

// doGet sends a GET request via the no-redirect TLS client.
func (s *HeadlessLoginSession) doGet(ctx context.Context, targetURL string, headers map[string]string) (*fhttp.Response, error) {
	req, err := fhttp.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return s.client.Do(req)
}

// doGetFollow sends a GET request via the follow-redirect TLS client.
func (s *HeadlessLoginSession) doGetFollow(ctx context.Context, targetURL string, headers map[string]string) (*fhttp.Response, error) {
	req, err := fhttp.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return s.followClient.Do(req)
}

// doPost sends a POST request via the no-redirect TLS client.
func (s *HeadlessLoginSession) doPost(ctx context.Context, targetURL, body string, headers map[string]string) (*fhttp.Response, error) {
	req, err := fhttp.NewRequestWithContext(ctx, "POST", targetURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return s.client.Do(req)
}

// syncCookies copies cookies from followClient to client.
func (s *HeadlessLoginSession) syncCookies() {
	cookieURL, _ := url.Parse(oauthIssuer)
	cookies := s.followClient.GetCookies(cookieURL)
	if len(cookies) > 0 {
		s.client.SetCookies(cookieURL, cookies)
	}
}

// syncCookiesReverse copies cookies from client to followClient.
func (s *HeadlessLoginSession) syncCookiesReverse() {
	cookieURL, _ := url.Parse(oauthIssuer)
	cookies := s.client.GetCookies(cookieURL)
	if len(cookies) > 0 {
		s.followClient.SetCookies(cookieURL, cookies)
	}
}

func extractCodeFromURL(u string) string {
	if u == "" || !strings.Contains(u, "code=") {
		return ""
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return parsed.Query().Get("code")
}

var localhostCodeRe = regexp.MustCompile(`https?://localhost[^\s'"]+`)

func extractCodeFromError(err error) string {
	if err == nil {
		return ""
	}
	match := localhostCodeRe.FindString(err.Error())
	if match != "" {
		return extractCodeFromURL(match)
	}
	return ""
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
