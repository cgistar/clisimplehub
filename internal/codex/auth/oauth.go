package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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
)

const (
	AuthURL     = "https://auth.openai.com/oauth/authorize"
	RedirectURI = "http://localhost:1455/auth/callback"
	OAuthPort   = 1455
)

type PKCECodes struct {
	CodeVerifier  string
	CodeChallenge string
}

type OAuthResult struct {
	Code  string
	State string
	Error string
}

type CodexLoginResult struct {
	RefreshToken string `json:"refreshToken"`
	AccessToken  string `json:"accessToken"`
	IDToken      string `json:"idToken"`
	AccountID    string `json:"accountId"`
	Email        string `json:"email"`
	PlanType     string `json:"planType"`
	ExpiresAt    string `json:"expiresAt"`
}

func GeneratePKCECodes() (*PKCECodes, error) {
	b := make([]byte, 96)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	verifier := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b)
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(hash[:])
	return &PKCECodes{CodeVerifier: verifier, CodeChallenge: challenge}, nil
}

func GenerateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b), nil
}

func StartCodexLogin(ctx context.Context, proxyURL string) (*CodexLoginResult, error) {
	pkce, err := GeneratePKCECodes()
	if err != nil {
		return nil, fmt.Errorf("PKCE generation: %w", err)
	}

	state, err := GenerateState()
	if err != nil {
		return nil, fmt.Errorf("state generation: %w", err)
	}

	resultCh := make(chan *OAuthResult, 1)
	server, err := startOAuthServer(resultCh)
	if err != nil {
		return nil, fmt.Errorf("start callback server: %w", err)
	}
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutCtx)
	}()

	authURL := BuildAuthURL(state, pkce)

	// Return URL to caller; in desktop mode, the caller opens the browser
	fmt.Printf("Open this URL to login:\n%s\n", authURL)

	// Wait for callback
	var oauthResult *OAuthResult
	select {
	case oauthResult = <-resultCh:
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("login timeout (5 minutes)")
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if oauthResult.Error != "" {
		return nil, fmt.Errorf("OAuth error: %s", oauthResult.Error)
	}
	if oauthResult.State != state {
		return nil, fmt.Errorf("state mismatch")
	}

	return ExchangeCodeForTokens(ctx, oauthResult.Code, pkce, proxyURL)
}

func StartCodexLoginWithURL(ctx context.Context, proxyURL string) (authURL string, waitFn func() (*CodexLoginResult, error), cleanup func(), err error) {
	pkce, err := GeneratePKCECodes()
	if err != nil {
		return "", nil, nil, fmt.Errorf("PKCE generation: %w", err)
	}

	state, err := GenerateState()
	if err != nil {
		return "", nil, nil, fmt.Errorf("state generation: %w", err)
	}

	resultCh := make(chan *OAuthResult, 1)
	server, err := startOAuthServer(resultCh)
	if err != nil {
		return "", nil, nil, fmt.Errorf("start callback server: %w", err)
	}

	cleanupFn := func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutCtx)
	}

	authURL = BuildAuthURL(state, pkce)

	waitFn = func() (*CodexLoginResult, error) {
		var oauthResult *OAuthResult
		select {
		case oauthResult = <-resultCh:
		case <-time.After(5 * time.Minute):
			return nil, fmt.Errorf("login timeout")
		case <-ctx.Done():
			return nil, ctx.Err()
		}

		if oauthResult.Error != "" {
			return nil, fmt.Errorf("OAuth error: %s", oauthResult.Error)
		}
		if oauthResult.State != state {
			return nil, fmt.Errorf("state mismatch")
		}
		return ExchangeCodeForTokens(ctx, oauthResult.Code, pkce, proxyURL)
	}

	return authURL, waitFn, cleanupFn, nil
}

func BuildAuthURL(state string, pkce *PKCECodes) string {
	params := url.Values{
		"client_id":                  {ClientID},
		"response_type":              {"code"},
		"redirect_uri":               {RedirectURI},
		"scope":                      {"openid email profile offline_access"},
		"state":                      {state},
		"code_challenge":             {pkce.CodeChallenge},
		"code_challenge_method":      {"S256"},
		"prompt":                     {"login"},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
	}
	return fmt.Sprintf("%s?%s", AuthURL, params.Encode())
}

func ExchangeCodeForTokens(ctx context.Context, code string, pkce *PKCECodes, proxyURL string) (*CodexLoginResult, error) {
	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 30*time.Second)

	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {ClientID},
		"code":          {code},
		"redirect_uri":  {RedirectURI},
		"code_verifier": {pkce.CodeVerifier},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (HTTP %d): %s", resp.StatusCode, string(body))
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

// --- OAuth callback server ---

func startOAuthServer(resultCh chan<- *OAuthResult) (*http.Server, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", OAuthPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("port %d in use: %w", OAuthPort, err)
	}

	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		result := &OAuthResult{
			Code:  q.Get("code"),
			State: q.Get("state"),
			Error: q.Get("error"),
		}
		once.Do(func() {
			select {
			case resultCh <- result:
			default:
			}
		})
		http.Redirect(w, r, "/success", http.StatusFound)
	})
	mux.HandleFunc("/success", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(successHTML))
	})

	server := &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		_ = server.Serve(ln)
	}()
	return server, nil
}

const successHTML = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Login Successful</title>
<style>body{font-family:system-ui;display:flex;justify-content:center;align-items:center;min-height:100vh;margin:0;background:#f5f5f5}
.card{text-align:center;padding:40px;background:#fff;border-radius:12px;box-shadow:0 2px 10px rgba(0,0,0,0.1)}
h1{color:#10a37f;margin-bottom:16px}p{color:#666}</style></head>
<body><div class="card"><h1>&#10003; Login Successful</h1><p>You can close this window now.</p>
<script>setTimeout(()=>window.close(),3000)</script></div></body></html>`
