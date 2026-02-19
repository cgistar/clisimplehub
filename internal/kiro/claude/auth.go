package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	kiroapi "clisimplehub/internal/kiro"
	kiroClient "clisimplehub/internal/kiro/client"
	kiroShared "clisimplehub/internal/kiro/shared"
)

const (
	// TokenRefreshThreshold is the time before expiration to refresh token (5 minutes)
	TokenRefreshThreshold = 5 * time.Minute
	// MaxRetries is the maximum number of retry attempts for token refresh
	MaxRetries = 3
	// BaseRetryDelay is the initial delay for exponential backoff
	BaseRetryDelay = 1 * time.Second
)

// KiroAuthManager manages Kiro API authentication and token lifecycle
type KiroAuthManager struct {
	mu            sync.RWMutex
	creds         *kiroShared.KiroCredentials
	credsPath     string
	httpClient    *http.Client
	refreshURL    string
	clientVersion string
	proxyURL      string
}

// NewKiroAuthManager creates a new authentication manager
func NewKiroAuthManager(creds *kiroShared.KiroCredentials, credsPath string, proxyURL string, version string) *KiroAuthManager {
	if creds == nil {
		creds = &kiroShared.KiroCredentials{}
	}
	region := kiroapi.ResolveRegion(creds.Region)

	proxyURL = strings.TrimSpace(proxyURL)
	// Create HTTP client with optional proxy support using unified client factory
	httpClient := kiroClient.NewHTTPClientWithProxy(proxyURL, 30*time.Second)

	return &KiroAuthManager{
		creds:         creds,
		credsPath:     credsPath,
		httpClient:    httpClient,
		refreshURL:    kiroapi.KiroRefreshURL(region),
		clientVersion: kiroShared.KiroVersionOrDefault(version),
		proxyURL:      proxyURL,
	}
}

// SetProxyURL updates the proxy used for token refresh requests.
func (m *KiroAuthManager) SetProxyURL(proxyURL string) {
	if m == nil {
		return
	}
	proxyURL = strings.TrimSpace(proxyURL)
	m.mu.Lock()
	defer m.mu.Unlock()
	if proxyURL == m.proxyURL {
		return
	}
	m.proxyURL = proxyURL
	m.httpClient = kiroClient.NewHTTPClientWithProxy(proxyURL, 30*time.Second)
}

// GetAccessToken returns a valid access token, refreshing if necessary
func (m *KiroAuthManager) GetAccessToken() (string, error) {
	if m == nil {
		return "", errors.New("auth manager is nil")
	}

	now := time.Now()

	// Fast path: check if we have a valid token without taking write lock
	m.mu.RLock()
	if m.creds != nil && m.creds.AccessToken != "" && !m.isTokenExpiringLocked(now) {
		token := m.creds.AccessToken // Copy token while holding lock
		m.mu.RUnlock()
		return token, nil
	}
	m.mu.RUnlock()

	// Slow path: need to refresh token
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.creds == nil {
		return "", errors.New("credentials not loaded")
	}

	// Double-check after acquiring write lock (another goroutine might have refreshed)
	if m.creds.AccessToken != "" && !m.isTokenExpiringLocked(now) {
		return m.creds.AccessToken, nil
	}

	// Perform token refresh
	if err := m.refreshTokenLocked(); err != nil {
		return "", err
	}

	if m.creds.AccessToken == "" {
		return "", errors.New("failed to obtain access token")
	}

	return m.creds.AccessToken, nil
}

// ForceRefresh forces a token refresh
func (m *KiroAuthManager) ForceRefresh() (string, error) {
	if m == nil {
		return "", errors.New("auth manager is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.creds == nil {
		return "", errors.New("credentials not loaded")
	}

	if err := m.refreshTokenLocked(); err != nil {
		return "", err
	}

	return m.creds.AccessToken, nil
}

// GetProfileArn returns the profile ARN
func (m *KiroAuthManager) GetProfileArn() string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.creds == nil {
		return ""
	}
	authMethod := strings.ToLower(strings.TrimSpace(m.creds.AuthMethod))
	if authMethod == "idc" {
		return ""
	}
	return m.creds.ProfileArn
}

// GetRegion returns the region
func (m *KiroAuthManager) GetRegion() string {
	if m == nil {
		return "us-east-1"
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.creds == nil {
		return "us-east-1"
	}
	if m.creds.Region == "" {
		return "us-east-1"
	}
	return m.creds.Region
}

// isTokenExpiringLocked checks if the token is expiring soon. Caller must hold at least RLock.
func (m *KiroAuthManager) isTokenExpiringLocked(now time.Time) bool {
	if m == nil || m.creds == nil {
		return true
	}
	if m.creds.ExpiresAt.IsZero() {
		return true
	}
	return now.Add(TokenRefreshThreshold).After(m.creds.ExpiresAt)
}

// retryDelay calculates retry delay with jitter and Retry-After header support
func (m *KiroAuthManager) retryDelay(attempt int, resp *http.Response) time.Duration {
	delay := BaseRetryDelay * time.Duration(1<<attempt)
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}

	if resp != nil && resp.StatusCode == 429 {
		if ra := strings.TrimSpace(resp.Header.Get("Retry-After")); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
				return time.Duration(secs) * time.Second
			}
			if t, err := http.ParseTime(ra); err == nil {
				d := time.Until(t)
				if d > 0 {
					return d
				}
			}
		}
	}

	jitterMax := delay / 2
	if jitterMax > 0 {
		delay += time.Duration(time.Now().UnixNano() % int64(jitterMax+1))
	}
	return delay
}

// userAgent generates the User-Agent string
func (m *KiroAuthManager) userAgent() string {
	if m == nil {
		return kiroShared.DefaultKiroVersion + "-unknown"
	}
	version := kiroShared.KiroVersionOrDefault(m.clientVersion)
	fp := ""
	if m.creds != nil {
		fp = strings.TrimSpace(m.creds.MachineID)
	}
	if fp == "" && m.creds != nil {
		fp = kiroapi.ComputeMachineID(m.creds.RefreshToken)
	}
	fp = kiroShared.TruncateFingerprint(fp, 64)
	return version + "-" + fp
}

// refreshTokenLocked performs the token refresh request with exponential backoff. Caller must hold Lock.
func (m *KiroAuthManager) refreshTokenLocked() error {
	if m.creds == nil {
		return errors.New("credentials not loaded")
	}
	if m.creds.RefreshToken == "" {
		return errors.New("refresh token is not set")
	}

	// Determine auth method and build appropriate request
	authMethod := strings.ToLower(strings.TrimSpace(m.creds.AuthMethod))
	if authMethod == "" {
		// Auto-detect: if clientId and clientSecret exist, assume IdC
		if m.creds.ClientId != "" && m.creds.ClientSecret != "" {
			authMethod = "idc"
		} else {
			authMethod = "social"
		}
	}

	var payloadBytes []byte
	var refreshURL string
	var err error

	if authMethod == "idc" {
		// IdC authentication
		if m.creds.ClientId == "" || m.creds.ClientSecret == "" {
			return errors.New("IdC auth method requires clientId and clientSecret")
		}

		payload := map[string]string{
			"clientId":     m.creds.ClientId,
			"clientSecret": m.creds.ClientSecret,
			"refreshToken": m.creds.RefreshToken,
			"grantType":    "refresh_token",
		}
		payloadBytes, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal IdC refresh payload: %w", err)
		}

		region := kiroapi.ResolveRegion(m.creds.Region)
		refreshURL = kiroapi.KiroIdcRefreshURL(region)
	} else {
		// Social authentication (default)
		payload := map[string]string{
			"refreshToken": m.creds.RefreshToken,
		}
		payloadBytes, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal refresh payload: %w", err)
		}
		refreshURL = kiroapi.KiroRefreshURL(m.creds.Region)
	}

	var lastErr error
	for attempt := 0; attempt < MaxRetries; attempt++ {
		req, err := http.NewRequest("POST", refreshURL, bytes.NewReader(payloadBytes))
		if err != nil {
			return fmt.Errorf("failed to create refresh request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		if authMethod == "idc" {
			kiroapi.ApplyIdcOidc(req)
			req.Header.Set("Host", req.URL.Host)
			req.Header.Set("Accept", "*/*")
			req.Header.Set("Accept-Language", "*")
			req.Header.Set("sec-fetch-mode", "cors")
		} else {
			req.Header.Set("Accept", "application/json, text/plain, */*")
			req.Header.Set("User-Agent", m.userAgent())
			req.Header.Set("Host", req.URL.Host)
			req.Header.Set("Connection", "close")
		}

		resp, err := m.httpClient.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(m.retryDelay(attempt, nil))
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			defer resp.Body.Close()
			return m.handleRefreshResponse(resp)
		}

		bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		// Check if error is retryable
		if isRetryableStatus(resp.StatusCode) {
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(bodySnippet)))
			time.Sleep(m.retryDelay(attempt, resp))
			continue
		}

		// Non-retryable error (4xx except 429)
		return fmt.Errorf("token refresh failed with HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(bodySnippet)))
	}

	if lastErr == nil {
		lastErr = errors.New("unknown error")
	}
	return fmt.Errorf("token refresh failed after %d attempts: %w", MaxRetries, lastErr)
}

// handleRefreshResponse processes the refresh response
func (m *KiroAuthManager) handleRefreshResponse(resp *http.Response) error {
	prevRefreshToken := ""
	if m != nil && m.creds != nil {
		prevRefreshToken = strings.TrimSpace(m.creds.RefreshToken)
	}

	var data struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int    `json:"expiresIn"`
		ProfileArn   string `json:"profileArn"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("failed to decode refresh response: %w", err)
	}

	if data.AccessToken == "" {
		return errors.New("response does not contain accessToken")
	}

	// Update credentials
	m.creds.AccessToken = data.AccessToken
	if data.RefreshToken != "" {
		m.creds.RefreshToken = data.RefreshToken
	}
	if data.ProfileArn != "" {
		m.creds.ProfileArn = data.ProfileArn
	}

	// Calculate expiration time with 60 second buffer
	expiresIn := data.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600 // Default 1 hour
	}
	now := time.Now()
	if expiresIn <= 60 {
		m.creds.ExpiresAt = now.Add(time.Duration(expiresIn) * time.Second)
	} else {
		m.creds.ExpiresAt = now.Add(time.Duration(expiresIn-60) * time.Second)
	}

	m.persistRefreshAuthFields(prevRefreshToken)

	return nil
}

func (m *KiroAuthManager) persistRefreshAuthFields(prevRefreshToken string) {
	if m == nil || m.creds == nil {
		return
	}

	authFields := &kiroShared.KiroCredentials{
		AccessToken:  m.creds.AccessToken,
		RefreshToken: m.creds.RefreshToken,
		ProfileArn:   m.creds.ProfileArn,
		ExpiresAt:    m.creds.ExpiresAt,
	}
	if strings.TrimSpace(authFields.RefreshToken) != "" && strings.TrimSpace(m.creds.MachineID) == "" {
		authFields.MachineID = kiroapi.ComputeMachineID(authFields.RefreshToken)
	}

	// Persist to kiro.json
	kiroJsonPath := strings.TrimSpace(m.credsPath)
	if kiroJsonPath == "" {
		return
	}
	if err := kiroShared.SyncTokenToKiroJson(kiroJsonPath, prevRefreshToken, authFields); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to sync kiro.json: %v\n", err)
		return
	}

	// If refreshToken rotated, reload the global pool so MarkFailed/Select can match the new key.
	if strings.TrimSpace(prevRefreshToken) != "" &&
		strings.TrimSpace(prevRefreshToken) != strings.TrimSpace(authFields.RefreshToken) {
		if pool := kiroapi.GetPool(); pool != nil {
			pool.Reload()
		}
	}
}

// isRetryableStatus checks if an HTTP status code is retryable
func isRetryableStatus(status int) bool {
	return status == 429 || status == 500 || status == 502 || status == 503 || status == 504
}
