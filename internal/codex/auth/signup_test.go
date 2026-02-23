package auth

import (
	"context"
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"

	"clisimplehub/internal/codex/auth/mailprovider"
)

// TestCodexSignup is a DuckMail auto-signup integration test.
// It creates a temporary email via DuckMail, registers a new OpenAI account,
// auto-fetches the OTP from the mailbox, and obtains a refreshToken via HeadlessLogin.
//
// Run:
//
//	TEST_DUCKMAIL_API_BASE=https://api.duckmail.sbs \
//	  go test ./internal/codex/auth/ -run TestCodexSignup -v -count=1
func TestCodexSignup(t *testing.T) {
	apiBase := os.Getenv("TEST_DUCKMAIL_API_BASE")
	if apiBase == "" {
		t.Skip("TEST_DUCKMAIL_API_BASE not set")
	}

	proxyURL := os.Getenv("TEST_PROXY")
	clientID := os.Getenv("TEST_CLIENT_ID")

	session, err := StartSignup(context.Background(), &SignupRequest{
		EmailProvider: "duckmail",
		ProviderParams: map[string]string{
			"duckmail_api_base": apiBase,
		},
		ClientID: clientID,
		ProxyURL: proxyURL,
		OnStep: func(msg string) {
			t.Logf("[step] %s", msg)
		},
	})
	if err != nil {
		t.Fatalf("StartSignup failed: %v", err)
	}

	if session.State() == SignupError {
		t.Fatalf("Signup error: %v", session.Error())
	}

	// DuckMail mode may still fall back to manual OTP
	if session.State() == SignupNeedOTP {
		t.Log("Auto OTP failed, falling back to manual input")
		code, err := readOTPCode(t, 1)
		if err != nil {
			t.Fatalf("read OTP failed: %v", err)
		}
		if err := session.SubmitOTP(context.Background(), code); err != nil {
			t.Fatalf("SubmitOTP failed: %v", err)
		}
	}

	if session.State() != SignupSuccess {
		t.Fatalf("unexpected state: %d, error: %v", session.State(), session.Error())
	}

	result := session.Result()
	if result == nil || result.CodexLoginResult == nil {
		t.Fatal("result is nil")
	}
	if result.RefreshToken == "" {
		t.Fatal("refreshToken is empty")
	}
	if result.Email == "" {
		t.Fatal("email is empty")
	}
	if result.Password == "" {
		t.Fatal("password is empty")
	}

	t.Logf("Email: %s", result.Email)
	t.Logf("Password: %s", result.Password)
	t.Logf("AccessToken: %s", result.AccessToken)
	t.Logf("RefreshToken: %s", result.RefreshToken)
	t.Logf("AccountID: %s", result.AccountID)
	t.Logf("PlanType: %s", result.PlanType)
}

// TestCodexSignupManual is a manual-mode signup test.
// The user provides their own email; OTP must be entered manually via terminal.
//
// Run:
//
//	TEST_SIGNUP_EMAIL=your@email.com \
//	  go test ./internal/codex/auth/ -run TestCodexSignupManual -v -count=1
func TestCodexSignupManual(t *testing.T) {
	email := os.Getenv("TEST_SIGNUP_EMAIL")
	if email == "" {
		t.Skip("TEST_SIGNUP_EMAIL not set")
	}

	password := os.Getenv("TEST_SIGNUP_PASSWORD")
	proxyURL := os.Getenv("TEST_PROXY")
	clientID := os.Getenv("TEST_CLIENT_ID")

	session, err := StartSignup(context.Background(), &SignupRequest{
		Email:    email,
		Password: password,
		ClientID: clientID,
		ProxyURL: proxyURL,
		OnStep: func(msg string) {
			t.Logf("[step] %s", msg)
		},
	})
	if err != nil {
		t.Fatalf("StartSignup failed: %v", err)
	}

	if session.State() == SignupError {
		t.Fatalf("Signup error: %v", session.Error())
	}

	// Manual mode always requires OTP
	if session.State() == SignupNeedOTP {
		const maxAttempts = 3
		for attempt := 1; attempt <= maxAttempts && session.State() == SignupNeedOTP; attempt++ {
			code, err := readOTPCode(t, attempt)
			if err != nil {
				t.Fatalf("read OTP failed: %v", err)
			}
			if err := session.SubmitOTP(context.Background(), code); err != nil {
				if session.State() == SignupNeedOTP && attempt < maxAttempts {
					t.Logf("SubmitOTP attempt %d/%d failed: %v", attempt, maxAttempts, err)
					continue
				}
				t.Fatalf("SubmitOTP failed: %v", err)
			}
		}
		if session.State() == SignupNeedOTP {
			t.Fatalf("OTP still required after %d attempts", maxAttempts)
		}
	}

	if session.State() != SignupSuccess {
		t.Fatalf("unexpected state: %d, error: %v", session.State(), session.Error())
	}

	result := session.Result()
	if result == nil || result.CodexLoginResult == nil {
		t.Fatal("result is nil")
	}

	t.Logf("Email: %s", result.Email)
	t.Logf("Password: %s", result.Password)
	t.Logf("AccessToken: %s", result.AccessToken)
	t.Logf("RefreshToken: %s", result.RefreshToken)
	t.Logf("AccountID: %s", result.AccountID)
	t.Logf("PlanType: %s", result.PlanType)
}

// TestDuckMailProvider tests the DuckMail email provider in isolation.
//
// Run:
//
//	TEST_DUCKMAIL_API_BASE=https://api.duckmail.sbs \
//	  go test ./internal/codex/auth/ -run TestDuckMailProvider -v -count=1
func TestDuckMailProvider(t *testing.T) {
	apiBase := os.Getenv("TEST_DUCKMAIL_API_BASE")
	if apiBase == "" {
		t.Skip("TEST_DUCKMAIL_API_BASE not set")
	}

	p, err := mailprovider.NewProvider("duckmail")
	if err != nil {
		t.Fatalf("NewProvider failed: %v", err)
	}

	params := map[string]string{
		"duckmail_api_base": apiBase,
	}

	email, mailPwd, err := p.CreateEmail(params)
	if err != nil {
		t.Fatalf("CreateEmail failed: %v", err)
	}
	if email == "" {
		t.Fatal("email is empty")
	}
	if mailPwd == "" {
		t.Fatal("mailPwd is empty")
	}

	t.Logf("Created email: %s", email)
	t.Logf("Mail password: %s", mailPwd)
	t.Log("DuckMail provider works. Skipping FetchVerificationCode (no OTP to fetch).")
}

// TestCodexSignupGPTMail is a GPTMail auto-signup integration test.
//
// Run:
//
//	TEST_GPTMAIL_API_BASE=https://mail.chatgpt.org.uk TEST_GPTMAIL_API_KEY=gpt-test \
//	  go test ./internal/codex/auth/ -run TestCodexSignupGPTMail -v -count=1
func TestCodexSignupGPTMail(t *testing.T) {
	apiBase := os.Getenv("TEST_GPTMAIL_API_BASE")
	apiKey := os.Getenv("TEST_GPTMAIL_API_KEY")
	if apiBase == "" && apiKey == "" {
		t.Skip("TEST_GPTMAIL_API_BASE and TEST_GPTMAIL_API_KEY not set (both can use defaults, set at least one to run)")
	}

	proxyURL := os.Getenv("TEST_PROXY")
	clientID := os.Getenv("TEST_CLIENT_ID")

	params := map[string]string{}
	if apiBase != "" {
		params["gptmail_api_base"] = apiBase
	}
	if apiKey != "" {
		params["gptmail_api_key"] = apiKey
	}

	session, err := StartSignup(context.Background(), &SignupRequest{
		EmailProvider:  "gptmail",
		ProviderParams: params,
		ClientID:       clientID,
		ProxyURL:       proxyURL,
		OnStep: func(msg string) {
			t.Logf("[step] %s", msg)
		},
	})
	if err != nil {
		t.Fatalf("StartSignup failed: %v", err)
	}

	if session.State() == SignupError {
		t.Fatalf("Signup error: %v", session.Error())
	}

	if session.State() == SignupNeedOTP {
		t.Log("Auto OTP failed, falling back to manual input")
		code, err := readOTPCode(t, 1)
		if err != nil {
			t.Fatalf("read OTP failed: %v", err)
		}
		if err := session.SubmitOTP(context.Background(), code); err != nil {
			t.Fatalf("SubmitOTP failed: %v", err)
		}
	}

	if session.State() != SignupSuccess {
		t.Fatalf("unexpected state: %d, error: %v", session.State(), session.Error())
	}

	result := session.Result()
	if result == nil || result.CodexLoginResult == nil {
		t.Fatal("result is nil")
	}
	if result.RefreshToken == "" {
		t.Fatal("refreshToken is empty")
	}

	t.Logf("Email: %s", result.Email)
	t.Logf("Password: %s", result.Password)
	t.Logf("AccessToken: %s", result.AccessToken)
	t.Logf("RefreshToken: %s", result.RefreshToken)
	t.Logf("AccountID: %s", result.AccountID)
	t.Logf("PlanType: %s", result.PlanType)
}

// TestGPTMailProvider tests the GPTMail email provider in isolation.
//
// Run:
//
//	go test ./internal/codex/auth/ -run TestGPTMailProvider -v -count=1
func TestGPTMailProvider(t *testing.T) {
	p, err := mailprovider.NewProvider("gptmail")
	if err != nil {
		t.Fatalf("NewProvider failed: %v", err)
	}

	params := map[string]string{}
	apiBase := os.Getenv("TEST_GPTMAIL_API_BASE")
	apiKey := os.Getenv("TEST_GPTMAIL_API_KEY")
	if apiBase != "" {
		params["gptmail_api_base"] = apiBase
	}
	if apiKey != "" {
		params["gptmail_api_key"] = apiKey
	}

	email, _, err := p.CreateEmail(params)
	if err != nil {
		t.Fatalf("CreateEmail failed: %v", err)
	}
	if email == "" {
		t.Fatal("email is empty")
	}

	t.Logf("Created email: %s", email)
	t.Log("GPTMail provider works. Skipping FetchVerificationCode (no OTP to fetch).")
}

// TestCodexSignupCloudflare is a Cloudflare临时邮箱 auto-signup integration test.
//
// Run:
//
//	TEST_CF_WORKER_DOMAIN=mail.example.com TEST_CF_EMAIL_DOMAIN=example.com TEST_CF_ADMIN_PASSWORD=xxx \
//	  go test ./internal/codex/auth/ -run TestCodexSignupCloudflare -v -count=1
func TestCodexSignupCloudflare(t *testing.T) {
	workerDomain := os.Getenv("TEST_CF_WORKER_DOMAIN")
	emailDomain := os.Getenv("TEST_CF_EMAIL_DOMAIN")
	adminPwd := os.Getenv("TEST_CF_ADMIN_PASSWORD")
	if workerDomain == "" || emailDomain == "" || adminPwd == "" {
		t.Skip("TEST_CF_WORKER_DOMAIN, TEST_CF_EMAIL_DOMAIN, TEST_CF_ADMIN_PASSWORD not all set")
	}

	proxyURL := os.Getenv("TEST_PROXY")
	clientID := os.Getenv("TEST_CLIENT_ID")

	session, err := StartSignup(context.Background(), &SignupRequest{
		EmailProvider: "cloudflare",
		ProviderParams: map[string]string{
			"cf_worker_domain":  workerDomain,
			"cf_email_domain":   emailDomain,
			"cf_admin_password": adminPwd,
		},
		ClientID: clientID,
		ProxyURL: proxyURL,
		OnStep: func(msg string) {
			t.Logf("[step] %s", msg)
		},
	})
	if err != nil {
		t.Fatalf("StartSignup failed: %v", err)
	}

	if session.State() == SignupError {
		t.Fatalf("Signup error: %v", session.Error())
	}

	if session.State() == SignupNeedOTP {
		t.Log("Auto OTP failed, falling back to manual input")
		code, err := readOTPCode(t, 1)
		if err != nil {
			t.Fatalf("read OTP failed: %v", err)
		}
		if err := session.SubmitOTP(context.Background(), code); err != nil {
			t.Fatalf("SubmitOTP failed: %v", err)
		}
	}

	if session.State() != SignupSuccess {
		t.Fatalf("unexpected state: %d, error: %v", session.State(), session.Error())
	}

	result := session.Result()
	if result == nil || result.CodexLoginResult == nil {
		t.Fatal("result is nil")
	}
	if result.RefreshToken == "" {
		t.Fatal("refreshToken is empty")
	}

	t.Logf("Email: %s", result.Email)
	t.Logf("Password: %s", result.Password)
	t.Logf("AccessToken: %s", result.AccessToken)
	t.Logf("RefreshToken: %s", result.RefreshToken)
	t.Logf("AccountID: %s", result.AccountID)
	t.Logf("PlanType: %s", result.PlanType)
}

// TestCloudflareProvider tests the Cloudflare email provider in isolation.
//
// Run:
//
//	TEST_CF_WORKER_DOMAIN=mail.example.com TEST_CF_EMAIL_DOMAIN=example.com TEST_CF_ADMIN_PASSWORD=xxx \
//	  go test ./internal/codex/auth/ -run TestCloudflareProvider -v -count=1
func TestCloudflareProvider(t *testing.T) {
	workerDomain := os.Getenv("TEST_CF_WORKER_DOMAIN")
	emailDomain := os.Getenv("TEST_CF_EMAIL_DOMAIN")
	adminPwd := os.Getenv("TEST_CF_ADMIN_PASSWORD")
	if workerDomain == "" || emailDomain == "" || adminPwd == "" {
		t.Skip("TEST_CF_WORKER_DOMAIN, TEST_CF_EMAIL_DOMAIN, TEST_CF_ADMIN_PASSWORD not all set")
	}

	p, err := mailprovider.NewProvider("cloudflare")
	if err != nil {
		t.Fatalf("NewProvider failed: %v", err)
	}

	params := map[string]string{
		"cf_worker_domain":  workerDomain,
		"cf_email_domain":   emailDomain,
		"cf_admin_password": adminPwd,
	}

	email, _, err := p.CreateEmail(params)
	if err != nil {
		t.Fatalf("CreateEmail failed: %v", err)
	}
	if email == "" {
		t.Fatal("email is empty")
	}

	t.Logf("Created email: %s", email)
	t.Log("Cloudflare provider works. Skipping FetchVerificationCode (no OTP to fetch).")
}

// TestOutlookProvider tests the Outlook email provider in isolation.
// Supports both IMAP and Graph API modes.
//
// Run (IMAP mode):
//
//	TEST_OUTLOOK_EMAIL=your@outlook.com TEST_OUTLOOK_CLIENT_ID=xxx TEST_OUTLOOK_REFRESH_TOKEN=xxx \
//	  go test ./internal/codex/auth/ -run TestOutlookProvider -v -count=1
//
// Run (Graph mode):
//
//	TEST_OUTLOOK_EMAIL=your@outlook.com TEST_OUTLOOK_CLIENT_ID=xxx TEST_OUTLOOK_REFRESH_TOKEN=xxx TEST_OUTLOOK_MODE=graph \
//	  go test ./internal/codex/auth/ -run TestOutlookProvider -v -count=1
//
// Optional:
//
//	TEST_OUTLOOK_FETCH_TIMEOUT=180 (seconds, default: 120)
func TestOutlookProvider(t *testing.T) {
	email := os.Getenv("TEST_OUTLOOK_EMAIL")
	clientID := os.Getenv("TEST_OUTLOOK_CLIENT_ID")
	refreshToken := os.Getenv("TEST_OUTLOOK_REFRESH_TOKEN")

	if email == "" || clientID == "" || refreshToken == "" {
		t.Skip("TEST_OUTLOOK_EMAIL, TEST_OUTLOOK_CLIENT_ID, TEST_OUTLOOK_REFRESH_TOKEN not set")
	}

	mode := os.Getenv("TEST_OUTLOOK_MODE")
	if mode == "" {
		mode = "imap"
	}

	p, err := mailprovider.NewProvider("outlook")
	if err != nil {
		t.Fatalf("NewProvider failed: %v", err)
	}

	params := map[string]string{
		"outlook_email":         email,
		"outlook_mode":          mode,
		"outlook_client_id":     clientID,
		"outlook_refresh_token": refreshToken,
	}

	createdEmail, _, err := p.CreateEmail(params)
	if err != nil {
		t.Fatalf("CreateEmail failed: %v", err)
	}

	if createdEmail != email {
		t.Errorf("Expected email %s, got %s", email, createdEmail)
	}

	timeoutSec := 120
	if timeoutStr := os.Getenv("TEST_OUTLOOK_FETCH_TIMEOUT"); timeoutStr != "" {
		parsedTimeout, parseErr := strconv.Atoi(timeoutStr)
		if parseErr != nil || parsedTimeout <= 0 {
			t.Fatalf("invalid TEST_OUTLOOK_FETCH_TIMEOUT=%q", timeoutStr)
		}
		timeoutSec = parsedTimeout
	}

	t.Logf("Created email: %s (mode: %s)", createdEmail, mode)
	t.Logf("Fetching verification code with timeout=%ds", timeoutSec)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec+10)*time.Second)
	defer cancel()

	code, err := p.FetchVerificationCode(ctx, params, createdEmail, timeoutSec)
	if err != nil {
		t.Fatalf("FetchVerificationCode failed: %v", err)
	}
	if code == "" {
		t.Fatal("FetchVerificationCode returned empty code")
	}
	if !regexp.MustCompile(`^\d{6}$`).MatchString(code) {
		t.Fatalf("FetchVerificationCode returned non-numeric code: %q", code)
	}

	t.Logf("Fetched verification code: %s", code)
}

func TestGeneratePassword(t *testing.T) {
	pwd := mailprovider.GeneratePassword(14)
	if len(pwd) != 14 {
		t.Errorf("expected length 14, got %d", len(pwd))
	}

	hasLower, hasUpper, hasDigit, hasSpecial := false, false, false, false
	for _, c := range pwd {
		switch {
		case c >= 'a' && c <= 'z':
			hasLower = true
		case c >= 'A' && c <= 'Z':
			hasUpper = true
		case c >= '0' && c <= '9':
			hasDigit = true
		default:
			hasSpecial = true
		}
	}
	if !hasLower || !hasUpper || !hasDigit || !hasSpecial {
		t.Errorf("password missing required char class: lower=%v upper=%v digit=%v special=%v, pwd=%s",
			hasLower, hasUpper, hasDigit, hasSpecial, pwd)
	}

	// Test minimum length guard
	short := mailprovider.GeneratePassword(2)
	if len(short) < 4 {
		t.Errorf("GeneratePassword(2) should produce at least 4 chars, got %d", len(short))
	}

	// Test uniqueness
	passwords := make(map[string]bool)
	for i := 0; i < 20; i++ {
		passwords[mailprovider.GeneratePassword(14)] = true
	}
	if len(passwords) < 10 {
		t.Error("GeneratePassword should produce diverse passwords")
	}

	t.Logf("Sample password: %s", pwd)
}
