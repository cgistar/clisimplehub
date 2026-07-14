package codexplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	codex "clisimplehub/internal/codex"
	codexAuth "clisimplehub/internal/codex/auth"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/plugin"
)

type oauthSession struct {
	PKCE      *codexAuth.PKCECodes
	ProxyURL  string
	ResultCh  chan *codexAuth.OAuthResult
	CreatedAt time.Time
}

var (
	oauthSessions   = make(map[string]*oauthSession)
	oauthSessionsMu sync.Mutex
	sessionTTL      = 10 * time.Minute

	webUILoginMu      sync.Mutex
	webUILoginCancel  context.CancelFunc
	webUILoginCleanup func()
	webUILoginGen     uint64
	saveAccountMu     sync.Mutex
)

func storeSession(state string, s *oauthSession) {
	oauthSessionsMu.Lock()
	defer oauthSessionsMu.Unlock()
	now := time.Now()
	for k, v := range oauthSessions {
		if now.Sub(v.CreatedAt) > sessionTTL {
			delete(oauthSessions, k)
		}
	}
	oauthSessions[state] = s
}

func popSession(state string) *oauthSession {
	oauthSessionsMu.Lock()
	defer oauthSessionsMu.Unlock()
	s, ok := oauthSessions[state]
	if !ok {
		return nil
	}
	delete(oauthSessions, state)
	if time.Since(s.CreatedAt) > sessionTTL {
		return nil
	}
	return s
}

func getSession(state string) *oauthSession {
	oauthSessionsMu.Lock()
	defer oauthSessionsMu.Unlock()
	s, ok := oauthSessions[state]
	if !ok {
		return nil
	}
	if time.Since(s.CreatedAt) > sessionTTL {
		delete(oauthSessions, state)
		return nil
	}
	return s
}

func (p *CodexPlugin) handleAuthURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	p.mu.RLock()
	codexJsonPath := p.codexJsonPath
	p.mu.RUnlock()

	proxyURL := resolveProxyURL("", codexJsonPath)

	pkce, err := codexAuth.GeneratePKCECodes()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "PKCE generation failed"})
		return
	}

	state, err := codexAuth.GenerateState()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "state generation failed"})
		return
	}

	resultCh := make(chan *codexAuth.OAuthResult, 1)
	storeSession(state, &oauthSession{
		PKCE:      pkce,
		ProxyURL:  proxyURL,
		ResultCh:  resultCh,
		CreatedAt: time.Now(),
	})

	authURL := codexAuth.BuildAuthURL(state, pkce)
	go p.waitForOAuthCallback(state, resultCh)

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"url":    authURL,
		"state":  state,
	})
}

func (p *CodexPlugin) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var body struct {
		Provider    string `json:"provider"`
		RedirectURL string `json:"redirect_url"`
		State       string `json:"state"`
		Code        string `json:"code"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	code := body.Code
	state := body.State
	errorMessage := body.Error

	if body.RedirectURL != "" {
		if u, err := url.Parse(body.RedirectURL); err == nil {
			if c := u.Query().Get("code"); c != "" {
				code = c
			}
			if s := u.Query().Get("state"); s != "" {
				state = s
			}
			if e := u.Query().Get("error"); e != "" {
				errorMessage = e
			} else if e := u.Query().Get("error_description"); e != "" {
				errorMessage = e
			}
		}
	}

	if provider := strings.TrimSpace(body.Provider); provider != "" && !strings.EqualFold(provider, "codex") && !strings.EqualFold(provider, "openai") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported provider"})
		return
	}
	if strings.TrimSpace(state) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing state"})
		return
	}
	if strings.TrimSpace(code) == "" && strings.TrimSpace(errorMessage) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing authorization code"})
		return
	}

	sess := getSession(state)
	if sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found or expired"})
		return
	}

	result := &codexAuth.OAuthResult{
		Code:  strings.TrimSpace(code),
		State: strings.TrimSpace(state),
		Error: strings.TrimSpace(errorMessage),
	}
	select {
	case sess.ResultCh <- result:
	default:
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (p *CodexPlugin) waitForOAuthCallback(state string, resultCh <-chan *codexAuth.OAuthResult) {
	var oauthResult *codexAuth.OAuthResult
	select {
	case oauthResult = <-resultCh:
	case <-time.After(5 * time.Minute):
		_ = popSession(state)
		log.Printf("[codex-auth] OAuth callback timeout for state %s", state)
		return
	}

	if oauthResult.Error != "" {
		_ = popSession(state)
		log.Printf("[codex-auth] OAuth callback error: %s", oauthResult.Error)
		return
	}

	if oauthResult.State != state {
		_ = popSession(state)
		log.Printf("[codex-auth] OAuth state mismatch")
		return
	}

	sess := popSession(state)
	if sess == nil {
		log.Printf("[codex-auth] OAuth session expired before token exchange")
		return
	}

	result, err := codexAuth.ExchangeCodeForTokens(context.Background(), oauthResult.Code, sess.PKCE, sess.ProxyURL)
	if err != nil {
		log.Printf("[codex-auth] token exchange failed: %v", err)
		return
	}
	if err := p.saveOAuthAccount(result); err != nil {
		log.Printf("[codex-auth] save account failed: %v", err)
	}
}

func (p *CodexPlugin) saveOAuthAccount(result *codexAuth.CodexLoginResult) error {
	saveAccountMu.Lock()
	defer saveAccountMu.Unlock()

	if strings.TrimSpace(result.AccountID) == "" {
		return fmt.Errorf("accountId is required from OAuth login")
	}
	if strings.TrimSpace(result.Email) == "" {
		return fmt.Errorf("email is required from OAuth login")
	}
	localID := codexShared.GenerateCodexLocalID(result.AccountID, result.Email)
	if localID == "" {
		return fmt.Errorf("account id is required from OAuth login")
	}

	store := p.GetAccountStore()
	if store == nil {
		return fmt.Errorf("account store not initialized")
	}

	p.mu.RLock()
	codexJsonPath := p.codexJsonPath
	p.mu.RUnlock()

	ctx := context.Background()
	existing, _ := store.GetByID(ctx, localID)

	var expiresAt time.Time
	if result.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, result.ExpiresAt); err == nil {
			expiresAt = t
		}
	}

	if existing != nil {
		existing.RefreshToken = result.RefreshToken
		existing.AccessToken = result.AccessToken
		existing.IDToken = result.IDToken
		existing.Email = result.Email
		existing.PlanType = result.PlanType
		existing.Status = codexShared.CodexStatusValid
		existing.ExpiresAt = expiresAt
		existing.CooldownUntil = time.Time{}
		existing.CooldownReason = ""
		if err := store.Update(ctx, existing); err != nil {
			return fmt.Errorf("update account: %w", err)
		}
	} else {
		now := time.Now()
		account := &codexShared.CodexAccount{
			ID:           localID,
			RefreshToken: result.RefreshToken,
			AccessToken:  result.AccessToken,
			IDToken:      result.IDToken,
			AccountID:    result.AccountID,
			Email:        result.Email,
			PlanType:     result.PlanType,
			Enabled:      true,
			Websockets:   true,
			Status:       codexShared.CodexStatusValid,
			ExpiresAt:    expiresAt,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := store.Insert(ctx, account); err != nil {
			return fmt.Errorf("insert account: %w", err)
		}
	}

	// Update active in codex.json if needed
	mc, _ := codexShared.LoadCodexMultiConfig(codexJsonPath)
	if mc == nil {
		mc = &codexShared.CodexMultiConfig{}
	}
	if mc.ActiveAccountID == "" {
		mc.ActiveAccountID = localID
		_ = codexShared.SaveCodexMultiConfig(codexJsonPath, mc)
	}

	if svc := p.GetService(); svc != nil {
		svc.RemoveAuthManager(localID)
		svc.ensureCodexEndpoint()
	}

	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}

	return nil
}

func resolveProxyURL(requested, codexJsonPath string) string {
	if u := strings.TrimSpace(requested); u != "" {
		return u
	}
	if u := plugin.GetAppProxyURL(); u != "" {
		return u
	}
	if codexJsonPath != "" {
		if mc, err := codexShared.LoadCodexMultiConfig(codexJsonPath); err == nil && mc != nil {
			if u := strings.TrimSpace(mc.ProxyUrl); u != "" {
				return u
			}
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
