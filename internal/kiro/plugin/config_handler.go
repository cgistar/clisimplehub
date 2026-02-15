package kiroplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	kiroapi "clisimplehub/internal/kiro"
	kiroClaude "clisimplehub/internal/kiro/claude"
	kiroClient "clisimplehub/internal/kiro/client"
	kiroShared "clisimplehub/internal/kiro/shared"
	"clisimplehub/internal/storage"
)

// StorageAccessor provides optional access to storage for endpoint management.
type StorageAccessor interface {
	GetStorage() storage.Storage
	TriggerReload()
}

// SetStorageAccessor allows the server to inject storage access into the service.
func (s *KiroService) SetStorageAccessor(sa StorageAccessor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.storageAccessor = sa
}

// KiroConfigRequest 请求结构
type KiroConfigRequest struct {
	RefreshToken   string `json:"refreshToken"`
	Region         string `json:"region"`
	AuthMethod     string `json:"authMethod"`
	ClientId       string `json:"clientId"`
	ClientSecret   string `json:"clientSecret"`
	ProxyURL       string `json:"proxyUrl"`
	BufferedStream bool   `json:"bufferedStream"`
}

// KiroConfigResponse 响应结构
type KiroConfigResponse struct {
	AccessToken string     `json:"accessToken"`
	ExpiresAt   string     `json:"expiresAt"`
	ProfileArn  string     `json:"profileArn"`
	Usage       *UsageInfo `json:"usage,omitempty"`
}

// UsageInfo 使用量信息
type UsageInfo struct {
	Used  int `json:"used"`
	Limit int `json:"limit"`
}

// HandleKiroConfig handles Kiro configuration update requests.
func (s *KiroService) HandleKiroConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req KiroConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": fmt.Sprintf("Invalid request body: %v", err),
		})
		return
	}

	refreshToken := strings.TrimSpace(req.RefreshToken)
	if refreshToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "refreshToken is required",
		})
		return
	}

	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = "us-east-1"
	}

	authMethod := strings.ToLower(strings.TrimSpace(req.AuthMethod))
	if authMethod == "" {
		if strings.TrimSpace(req.ClientId) != "" && strings.TrimSpace(req.ClientSecret) != "" {
			authMethod = "idc"
		} else {
			authMethod = "social"
		}
	}

	if authMethod == "idc" {
		if strings.TrimSpace(req.ClientId) == "" || strings.TrimSpace(req.ClientSecret) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"error": "clientId and clientSecret are required for IdC authentication",
			})
			return
		}
	}

	configPath := s.resolveConfigPath()
	if configPath == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "config path not set",
		})
		return
	}

	kiroJsonPath := filepath.Join(filepath.Dir(configPath), filepath.Base(kiroShared.GetDefaultKiroMultiConfigPath()))

	version := ""
	userAgent := ""
	oldBufferedStream := false
	if mc, err := kiroShared.LoadKiroMultiConfig(kiroJsonPath); err == nil {
		version = strings.TrimSpace(mc.Version)
		userAgent = strings.TrimSpace(mc.UserAgent)
		oldBufferedStream = mc.BufferedStream
	}
	version = kiroShared.KiroVersionOrDefault(version)
	userAgent = kiroShared.KiroUserAgentBaseOrDefault(userAgent)

	var existingRefreshToken string
	var existingAccessToken string
	var existingExpiresAt time.Time
	var existingProfileArn string
	if mc, err := kiroShared.LoadKiroMultiConfig(kiroJsonPath); err == nil {
		if active := mc.GetActiveAccount(); active != nil {
			existingRefreshToken = strings.TrimSpace(active.RefreshToken)
			existingAccessToken = strings.TrimSpace(active.AccessToken)
			existingExpiresAt = active.ExpiresAt
			existingProfileArn = strings.TrimSpace(active.ProfileArn)
		}
	}

	refreshTokenChanged := existingRefreshToken != "" && refreshToken != "" && existingRefreshToken != refreshToken
	machineID := kiroapi.ComputeMachineID(refreshToken)
	bufferedStreamChanged := req.BufferedStream != oldBufferedStream

	if !refreshTokenChanged && existingAccessToken != "" {
		if !bufferedStreamChanged {
			response := KiroConfigResponse{
				AccessToken: existingAccessToken,
				ProfileArn:  existingProfileArn,
			}
			if !existingExpiresAt.IsZero() {
				response.ExpiresAt = existingExpiresAt.Format(time.RFC3339Nano)
			}
			writeJSON(w, http.StatusOK, response)
			return
		}
		if mc, err := kiroShared.LoadKiroMultiConfig(kiroJsonPath); err == nil {
			mc.BufferedStream = req.BufferedStream
			_ = kiroShared.SaveKiroMultiConfig(kiroJsonPath, mc)
		}
		response := KiroConfigResponse{
			AccessToken: existingAccessToken,
			ProfileArn:  existingProfileArn,
		}
		if !existingExpiresAt.IsZero() {
			response.ExpiresAt = existingExpiresAt.Format(time.RFC3339Nano)
		}
		writeJSON(w, http.StatusOK, response)
		return
	}

	creds := &kiroShared.KiroCredentials{
		RefreshToken: refreshToken,
		Region:       region,
		AuthMethod:   authMethod,
		ClientId:     req.ClientId,
		ClientSecret: req.ClientSecret,
	}

	mgr := kiroClaude.NewKiroAuthManager(creds, "", strings.TrimSpace(req.ProxyURL), version)
	accessToken, err := mgr.ForceRefresh()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Kiro token refresh failed: %v\n", err)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error": fmt.Sprintf("Failed to refresh token: %v", err),
		})
		return
	}
	if accessToken == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "Access token is empty after refresh",
		})
		return
	}

	profileArn := mgr.GetProfileArn()
	if authMethod == "idc" {
		profileArn = ""
	}

	var usageInfo *UsageInfo
	if accessToken != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		httpClient := kiroClient.NewHTTPClientWithProxy(strings.TrimSpace(req.ProxyURL), 10*time.Second)
		usageQuery := kiroapi.UsageQuery{
			AccessToken:   accessToken,
			ProfileArn:    profileArn,
			Region:        region,
			MachineID:     machineID,
			UserAgentBase: userAgent,
			Version:       version,
		}
		if usageResult, err := kiroapi.FetchUsage(ctx, httpClient, usageQuery); err == nil && usageResult != nil {
			usageInfo = &UsageInfo{
				Used:  int(usageResult.CurrentUsage),
				Limit: int(usageResult.UsageLimit),
			}
		}
	}

	saveCreds := &kiroShared.KiroCredentials{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ProfileArn:   profileArn,
		Region:       region,
		MachineID:    machineID,
		AuthMethod:   authMethod,
		ClientId:     req.ClientId,
		ClientSecret: req.ClientSecret,
	}
	if creds.ExpiresAt.IsZero() {
		saveCreds.ExpiresAt = time.Now().Add(1 * time.Hour)
	} else {
		saveCreds.ExpiresAt = creds.ExpiresAt
	}

	if err := kiroShared.SyncTokenToKiroJson(kiroJsonPath, existingRefreshToken, saveCreds); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to sync kiro.json: %v\n", err)
	}

	if mc, err := kiroShared.LoadKiroMultiConfig(kiroJsonPath); err == nil {
		mc.BufferedStream = req.BufferedStream
		_ = kiroShared.SaveKiroMultiConfig(kiroJsonPath, mc)
	}

	_ = kiroClaude.ReloadAllTransformers()

	s.ensureKiroEndpoint()

	expiresAt := ""
	if !saveCreds.ExpiresAt.IsZero() {
		expiresAt = saveCreds.ExpiresAt.Format(time.RFC3339Nano)
	}

	writeJSON(w, http.StatusOK, KiroConfigResponse{
		AccessToken: accessToken,
		ExpiresAt:   expiresAt,
		ProfileArn:  profileArn,
		Usage:       usageInfo,
	})
}

// HandleKiroGetUsage handles fetching Kiro usage information.
func (s *KiroService) HandleKiroGetUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	configPath := s.resolveConfigPath()
	if configPath == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "config path not set",
		})
		return
	}

	kiroJsonPath := filepath.Join(filepath.Dir(configPath), filepath.Base(kiroShared.GetDefaultKiroMultiConfigPath()))
	mc, err := kiroShared.LoadKiroMultiConfig(kiroJsonPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": fmt.Sprintf("Failed to load kiro config: %v", err),
		})
		return
	}
	activeAccount := mc.GetActiveAccount()
	if activeAccount == nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "No active Kiro account, please configure Kiro first",
		})
		return
	}

	accessToken := strings.TrimSpace(activeAccount.AccessToken)
	if accessToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "No access token available, please configure Kiro first",
		})
		return
	}

	region := strings.TrimSpace(activeAccount.Region)
	if region == "" {
		region = "us-east-1"
	}

	profileArn := strings.TrimSpace(activeAccount.ProfileArn)
	refreshToken := strings.TrimSpace(activeAccount.RefreshToken)
	version := kiroShared.KiroVersionOrDefault(strings.TrimSpace(mc.Version))
	userAgent := kiroShared.KiroUserAgentBaseOrDefault(strings.TrimSpace(mc.UserAgent))
	proxyURL := strings.TrimSpace(mc.ProxyURL)

	machineID := strings.TrimSpace(activeAccount.MachineId)
	if machineID == "" && refreshToken != "" {
		machineID = kiroapi.ComputeMachineID(refreshToken)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	httpClient := kiroClient.NewHTTPClientWithProxy(proxyURL, 10*time.Second)
	usageQuery := kiroapi.UsageQuery{
		AccessToken:   accessToken,
		ProfileArn:    profileArn,
		Region:        region,
		MachineID:     machineID,
		UserAgentBase: userAgent,
		Version:       version,
	}

	usageResult, err := kiroapi.FetchUsage(ctx, httpClient, usageQuery)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": fmt.Sprintf("Failed to fetch usage: %v", err),
		})
		return
	}
	if usageResult == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "No usage data returned",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"subscriptionTitle": usageResult.SubscriptionTitle,
		"usageLimit":        usageResult.UsageLimit,
		"currentUsage":      usageResult.CurrentUsage,
		"balance":           usageResult.Balance,
		"usagePct":          usageResult.UsagePct,
		"isLowBalance":      usageResult.IsLowBalance,
	})
}

// ensureKiroEndpoint ensures a kiro/claude endpoint exists.
func (s *KiroService) ensureKiroEndpoint() {
	s.mu.RLock()
	sa := s.storageAccessor
	s.mu.RUnlock()
	if sa == nil {
		return
	}
	store := sa.GetStorage()
	if store == nil {
		return
	}

	endpoints, err := store.GetEndpoints()
	if err != nil {
		return
	}

	for _, ep := range endpoints {
		if ep.Transformer == "kiro/claude" && ep.InterfaceType == "claude" {
			return
		}
	}

	newEndpoint := &storage.Endpoint{
		Name:          "kiro",
		APIURL:        "https://q.us-east-1.amazonaws.com",
		APIKey:        "-",
		Active:        false,
		Enabled:       true,
		InterfaceType: "claude",
		Transformer:   "kiro/claude",
		Priority:      8,
	}

	sameTypeCount := 0
	for _, ep := range endpoints {
		if ep.InterfaceType == "claude" {
			sameTypeCount++
		}
	}
	if sameTypeCount == 0 {
		newEndpoint.Active = true
	}

	if err := store.SaveEndpoint(newEndpoint); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save kiro endpoint: %v\n", err)
		return
	}

	sa.TriggerReload()
}

// resolveConfigPath gets the config path from the config getter.
func (s *KiroService) resolveConfigPath() string {
	if v, err := kiroClaude.GetConfig("configPath"); err == nil {
		return strings.TrimSpace(v)
	}
	return ""
}
