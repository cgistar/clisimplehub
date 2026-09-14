package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/executor"
	"clisimplehub/internal/plugin"
)

const (
	TokenURL              = "https://auth.openai.com/oauth/token"
	ClientID              = "app_EMoamEEZ73f0CkXaXp7hrann"
	TokenRefreshThreshold = 5 * time.Minute
	MaxRetries            = 3
	BaseRetryDelay        = 1 * time.Second
)

type CodexAuthManager struct {
	localID      string
	refreshToken string
	accessToken  string
	idToken      string
	accountID    string
	email        string
	planType     string
	expiresAt    time.Time
	proxyURL     string
	store        codexShared.CodexAccountStore
	mu           sync.Mutex
}

func NewCodexAuthManager(account *codexShared.CodexAccount, store codexShared.CodexAccountStore) *CodexAuthManager {
	m := &CodexAuthManager{
		refreshToken: account.RefreshToken,
		localID:      account.ID,
		accessToken:  account.AccessToken,
		idToken:      account.IDToken,
		accountID:    account.AccountID,
		email:        account.Email,
		planType:     account.PlanType,
		expiresAt:    account.ExpiresAt,
		proxyURL:     account.ProxyUrl,
		store:        store,
	}
	return m
}

func (m *CodexAuthManager) GetAccessToken() (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.accessToken != "" && time.Now().Add(TokenRefreshThreshold).Before(m.expiresAt) {
		return m.accessToken, m.accountID, nil
	}

	if err := m.refreshTokenLocked(); err != nil {
		return "", "", err
	}
	return m.accessToken, m.accountID, nil
}

func (m *CodexAuthManager) ForceRefresh() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refreshTokenLocked()
}

func (m *CodexAuthManager) SetProxyURL(proxyURL string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.proxyURL = proxyURL
}

func (m *CodexAuthManager) resolveProxyURL() string {
	if proxyURL := plugin.GetAppProxyURL(); proxyURL != "" {
		return proxyURL
	}
	return m.proxyURL
}

func (m *CodexAuthManager) refreshTokenLocked() error {
	if m.refreshToken == "" {
		if m.accessToken == "" {
			return fmt.Errorf("neither refresh token nor access token is set")
		}
		return nil
	}

	proxyURL := m.resolveProxyURL()
	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 30*time.Second)

	data := url.Values{
		"client_id":     {ClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {m.refreshToken},
		"scope":         {"openid profile email"},
	}

	var lastErr error
	for attempt := 0; attempt < MaxRetries; attempt++ {
		if attempt > 0 {
			delay := BaseRetryDelay * time.Duration(1<<uint(attempt-1))
			if delay > 8*time.Second {
				delay = 8 * time.Second
			}
			time.Sleep(delay)
		}

		req, err := http.NewRequestWithContext(context.Background(), "POST", TokenURL, strings.NewReader(data.Encode()))
		if err != nil {
			return fmt.Errorf("create refresh request: %w", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return m.handleRefreshResponse(body)
		}

		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			continue
		}

		bodyStr := strings.TrimSpace(string(body))
		if resp.StatusCode == http.StatusUnauthorized && strings.Contains(bodyStr, "refresh_token_reused") {
			if m.store != nil && m.localID != "" {
				_ = m.store.UpdateStatus(context.Background(), m.localID, codexShared.CodexStatusReused)
			}
		}

		return fmt.Errorf("token refresh failed (HTTP %d): %s", resp.StatusCode, bodyStr)
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("unknown error")
	}
	return fmt.Errorf("token refresh failed after %d attempts: %w", MaxRetries, lastErr)
}

type RefreshTestResult struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	AccountID    string
	Email        string
	PlanType     string
	ExpiresAt    time.Time
}

func parseTokenRefreshResponse(body []byte, fallbackRefreshToken string) (*RefreshTestResult, error) {
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("decode refresh response: %w", err)
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return nil, fmt.Errorf("response missing access_token")
	}

	result := &RefreshTestResult{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: strings.TrimSpace(tokenResp.RefreshToken),
		IDToken:      tokenResp.IDToken,
	}
	if result.RefreshToken == "" {
		result.RefreshToken = fallbackRefreshToken
	}

	expiresIn := tokenResp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	result.ExpiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)

	if tokenResp.IDToken != "" {
		if claims, parseErr := ParseJWTToken(tokenResp.IDToken); parseErr == nil {
			result.Email = claims.Email
			result.AccountID = claims.CodexAuth.ChatgptAccountID
			result.PlanType = claims.CodexAuth.ChatgptPlanType
		}
	}
	return result, nil
}

func (m *CodexAuthManager) handleRefreshResponse(body []byte) error {
	result, err := parseTokenRefreshResponse(body, m.refreshToken)
	if err != nil {
		return err
	}

	m.accessToken = result.AccessToken
	m.refreshToken = result.RefreshToken
	if result.IDToken != "" {
		m.idToken = result.IDToken
	}
	if result.Email != "" {
		m.email = result.Email
	}
	if result.AccountID != "" {
		m.accountID = result.AccountID
	}
	if result.PlanType != "" {
		m.planType = result.PlanType
	}
	m.expiresAt = result.ExpiresAt

	m.persistToStore()
	return nil
}

func (m *CodexAuthManager) persistToStore() {
	if m.store == nil || m.localID == "" {
		return
	}
	_ = m.store.UpdateTokens(context.Background(),
		m.localID, m.accessToken, m.idToken, m.refreshToken, m.expiresAt)
}

func RefreshAndTest(refreshToken, proxyURL, configPath string) (*RefreshTestResult, error) {
	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 30*time.Second)

	data := url.Values{
		"client_id":     {ClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"scope":         {"openid profile email"},
	}

	req, err := http.NewRequestWithContext(context.Background(), "POST", TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyStr := strings.TrimSpace(string(body))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, bodyStr)
	}

	return parseTokenRefreshResponse(body, refreshToken)
}
