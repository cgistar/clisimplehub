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

func (p *CodexPlugin) handleAuthURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	p.mu.RLock()
	codexJsonPath := p.codexJsonPath
	p.mu.RUnlock()

	proxyURL := resolveProxyURL("", codexJsonPath)

	if r.URL.Query().Get("is_webui") == "true" {
		webUILoginMu.Lock()
		if webUILoginCancel != nil {
			webUILoginCancel()
		}
		if webUILoginCleanup != nil {
			webUILoginCleanup()
		}
		webUILoginMu.Unlock()

		bgCtx, cancel := context.WithCancel(context.Background())
		authURL, waitFn, cleanupFn, err := codexAuth.StartCodexLoginWithURL(bgCtx, proxyURL)
		if err != nil {
			cancel()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		webUILoginMu.Lock()
		webUILoginGen++
		gen := webUILoginGen
		webUILoginCancel = cancel
		webUILoginCleanup = cleanupFn
		webUILoginMu.Unlock()

		go func() {
			defer func() {
				webUILoginMu.Lock()
				if webUILoginGen == gen {
					webUILoginCancel = nil
					webUILoginCleanup = nil
				}
				webUILoginMu.Unlock()
				cleanupFn()
				cancel()
			}()
			result, err := waitFn()
			if err != nil {
				log.Printf("[codex-auth] webui login failed: %v", err)
				return
			}
			if err := p.saveOAuthAccount(result); err != nil {
				log.Printf("[codex-auth] save account failed: %v", err)
			}
		}()

		writeJSON(w, http.StatusOK, map[string]string{
			"status": "ok",
			"url":    authURL,
		})
		return
	}

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

	storeSession(state, &oauthSession{
		PKCE:      pkce,
		ProxyURL:  proxyURL,
		CreatedAt: time.Now(),
	})

	authURL := codexAuth.BuildAuthURL(state, pkce)
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
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	code := body.Code
	state := body.State

	if body.RedirectURL != "" {
		if u, err := url.Parse(body.RedirectURL); err == nil {
			if c := u.Query().Get("code"); c != "" {
				code = c
			}
			if s := u.Query().Get("state"); s != "" {
				state = s
			}
		}
	}

	if strings.TrimSpace(state) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing state"})
		return
	}
	if strings.TrimSpace(code) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing authorization code"})
		return
	}

	sess := popSession(state)
	if sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found or expired"})
		return
	}

	result, err := codexAuth.ExchangeCodeForTokens(r.Context(), code, sess.PKCE, sess.ProxyURL)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "token exchange failed: " + err.Error()})
		return
	}

	if err := p.saveOAuthAccount(result); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"account": map[string]any{
			"email":     result.Email,
			"planType":  result.PlanType,
			"accountId": result.AccountID,
		},
	})
}

func (p *CodexPlugin) saveOAuthAccount(result *codexAuth.CodexLoginResult) error {
	saveAccountMu.Lock()
	defer saveAccountMu.Unlock()

	if strings.TrimSpace(result.AccountID) == "" {
		return fmt.Errorf("accountId is required from OAuth login")
	}

	store := p.GetAccountStore()
	if store == nil {
		return fmt.Errorf("account store not initialized")
	}

	p.mu.RLock()
	codexJsonPath := p.codexJsonPath
	p.mu.RUnlock()

	ctx := context.Background()
	existing, _ := store.GetByID(ctx, result.AccountID)

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
			RefreshToken: result.RefreshToken,
			AccessToken:  result.AccessToken,
			IDToken:      result.IDToken,
			AccountID:    result.AccountID,
			Email:        result.Email,
			PlanType:     result.PlanType,
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
		mc.ActiveAccountID = result.AccountID
		_ = codexShared.SaveCodexMultiConfig(codexJsonPath, mc)
	}

	if svc := p.GetService(); svc != nil {
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
