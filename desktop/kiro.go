package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"clisimplehub/internal/executor"
	kiroCore "clisimplehub/internal/transformer/kiro"
	kiroClaude "clisimplehub/internal/transformer/kiro/claude"
	kiroShared "clisimplehub/internal/transformer/kiro/shared"
)

// =============================================================================
// Kiro Configuration Methods
// =============================================================================

// KiroConfig represents Kiro-specific configuration
type KiroConfig struct {
	RefreshToken   string `json:"refreshToken"`
	ProfileArn     string `json:"profileArn"`
	Region         string `json:"region"`
	ProxyURL       string `json:"proxyUrl"`
	UserAgent      string `json:"userAgent"`
	Version        string `json:"version"`
	BufferedStream bool   `json:"bufferedStream"`
	AuthMethod     string `json:"authMethod"`
	ClientId       string `json:"clientId"`
	ClientSecret   string `json:"clientSecret"`
	AccessToken    string `json:"accessToken,omitempty"`
	ExpiresAt      string `json:"expiresAt,omitempty"`
}

func (a *App) getKiroAuthTokenPath() string {
	if a != nil && a.configLoader != nil {
		if p := strings.TrimSpace(a.configLoader.GetPath()); p != "" {
			return filepath.Join(filepath.Dir(p), filepath.Base(kiroShared.GetDefaultKiroCredentialsPath()))
		}
	}
	return kiroShared.GetDefaultKiroCredentialsPath()
}

// GetKiroAuthTokenJSON 返回 kiro-auth-token.json 的原始 JSON 内容用于备份
// 文件不存在时返回 (nil, nil)
func (a *App) GetKiroAuthTokenJSON() (map[string]interface{}, error) {
	credsPath := kiroShared.ExpandTilde(a.getKiroAuthTokenPath())
	data, err := os.ReadFile(credsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read kiro-auth-token.json: %w", err)
	}

	var token map[string]interface{}
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("parse kiro-auth-token.json: %w", err)
	}
	return token, nil
}

// SaveKiroAuthTokenJSON 从备份恢复 kiro-auth-token.json
// 采用原子写入 + 0600 权限
func (a *App) SaveKiroAuthTokenJSON(token map[string]interface{}) error {
	if len(token) == 0 {
		return nil
	}

	refreshToken, _ := token["refreshToken"].(string)
	if strings.TrimSpace(refreshToken) == "" {
		return fmt.Errorf("invalid kiro auth token: refreshToken is required")
	}

	credsPath := kiroShared.ExpandTilde(a.getKiroAuthTokenPath())
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal kiro auth token: %w", err)
	}
	if err := writeFileAtomic0600(credsPath, data); err != nil {
		return fmt.Errorf("write kiro-auth-token.json: %w", err)
	}
	return nil
}

// writeFileAtomic0600 原子写入文件并设置 0600 权限
func writeFileAtomic0600(path string, data []byte) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("empty path")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		_ = tmp.Close()
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()

	if runtime.GOOS != "windows" {
		if err := tmp.Chmod(0600); err != nil {
			return err
		}
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	// Windows: 删除目标文件后再重命名
	if runtime.GOOS == "windows" {
		_ = os.Remove(path)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	renamed = true

	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0600); err != nil {
			return err
		}
	}
	return nil
}

// GetKiroConfig retrieves Kiro configuration
func (a *App) GetKiroConfig() (*KiroConfig, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	config := &KiroConfig{
		Region:     "us-east-1",
		AuthMethod: "social",
	}

	credsPath := a.getKiroAuthTokenPath()
	if creds, err := kiroShared.LoadKiroCredentials(credsPath); err == nil && creds != nil {
		config.RefreshToken = creds.RefreshToken
		config.ProfileArn = creds.ProfileArn
		config.AccessToken = creds.AccessToken
		config.AuthMethod = creds.AuthMethod
		config.ClientId = creds.ClientId
		config.ClientSecret = creds.ClientSecret
		if !creds.ExpiresAt.IsZero() {
			config.ExpiresAt = creds.ExpiresAt.Format(time.RFC3339Nano)
		}
		if strings.TrimSpace(creds.Region) != "" {
			config.Region = creds.Region
		}
		if strings.TrimSpace(config.AuthMethod) == "" {
			config.AuthMethod = "social"
		}
	}
	if proxy, err := a.storage.GetConfig("kiro.proxyUrl"); err == nil {
		config.ProxyURL = proxy
	}
	if ua, err := a.storage.GetConfig("kiro.userAgent"); err == nil {
		config.UserAgent = ua
	}
	if v, err := a.storage.GetConfig("kiro.version"); err == nil {
		config.Version = v
	}
	if bs, err := a.storage.GetConfig("kiro.bufferedStream"); err == nil {
		config.BufferedStream = bs == "true"
	}

	return config, nil
}

// SaveKiroConfig saves Kiro configuration
func (a *App) SaveKiroConfig(config *KiroConfig) error {
	if a.storage == nil {
		return fmt.Errorf("storage not initialized")
	}

	if strings.TrimSpace(config.RefreshToken) == "" {
		return fmt.Errorf("refresh token is required")
	}

	// Validate IdC fields
	authMethod := strings.ToLower(strings.TrimSpace(config.AuthMethod))
	if authMethod == "idc" {
		if strings.TrimSpace(config.ClientId) == "" || strings.TrimSpace(config.ClientSecret) == "" {
			return fmt.Errorf("clientId and clientSecret are required for IdC authentication")
		}
	}

	// Persist to credentials file next to config.json (single source of truth for Kiro auth).
	credsPath := a.getKiroAuthTokenPath()
	var creds *kiroShared.KiroCredentials
	if existing, err := kiroShared.LoadKiroCredentials(credsPath); err == nil {
		creds = existing
	} else {
		creds = &kiroShared.KiroCredentials{}
	}

	oldRefreshToken := strings.TrimSpace(creds.RefreshToken)
	oldProfileArn := strings.TrimSpace(creds.ProfileArn)

	newRefreshToken := strings.TrimSpace(config.RefreshToken)
	newProfileArn := strings.TrimSpace(config.ProfileArn)
	newRegion := strings.TrimSpace(config.Region)

	refreshTokenChanged := oldRefreshToken != "" && newRefreshToken != "" && oldRefreshToken != newRefreshToken

	machineID := kiroCore.ComputeMachineID(newRefreshToken)
	if err := a.storage.SetConfig("kiro.machineId", machineID); err != nil {
		return fmt.Errorf("failed to save machine id: %w", err)
	}

	creds.RefreshToken = newRefreshToken
	creds.Region = newRegion
	if creds.Region == "" {
		creds.Region = "us-east-1"
	}
	creds.AuthMethod = config.AuthMethod
	creds.ClientId = config.ClientId
	creds.ClientSecret = config.ClientSecret

	accessToken := strings.TrimSpace(config.AccessToken)
	expiresAtStr := strings.TrimSpace(config.ExpiresAt)
	if accessToken != "" || expiresAtStr != "" {
		if accessToken == "" {
			return fmt.Errorf("accessToken is required when expiresAt is provided")
		}
		if expiresAtStr == "" {
			return fmt.Errorf("expiresAt is required when accessToken is provided")
		}
		expiresAt, err := time.Parse(time.RFC3339Nano, expiresAtStr)
		if err != nil {
			expiresAt, err = time.Parse(time.RFC3339, expiresAtStr)
		}
		if err != nil {
			return fmt.Errorf("invalid expiresAt: %w", err)
		}
		creds.AccessToken = accessToken
		creds.ExpiresAt = expiresAt
	}

	// If the refresh token was changed by config update, require fresh auth state from UI test.
	if refreshTokenChanged {
		if strings.TrimSpace(config.AccessToken) == "" {
			return fmt.Errorf("accessToken is required when refreshToken changes; please test first")
		}
		if strings.TrimSpace(config.ExpiresAt) == "" {
			return fmt.Errorf("expiresAt is required when refreshToken changes; please test first")
		}
		creds.ProfileArn = strings.TrimSpace(config.ProfileArn)
		creds.Provider = ""
	} else {
		// Keep/overwrite profileArn only if explicitly provided.
		if newProfileArn != "" && newProfileArn != oldProfileArn {
			creds.ProfileArn = newProfileArn
		}
	}

	// IdC tokens do not require (and typically do not return) a CodeWhisperer profileArn.
	// Keeping a stale profileArn (e.g. from a prior Social login) can cause confusing upstream auth errors.
	if authMethod == "idc" || authMethod == "builder-id" {
		creds.ProfileArn = ""
	}
	if err := kiroShared.SaveKiroCredentials(credsPath, creds); err != nil {
		return fmt.Errorf("failed to save kiro credentials: %w", err)
	}

	if err := a.storage.SetConfig("kiro.proxyUrl", config.ProxyURL); err != nil {
		return fmt.Errorf("failed to save proxy URL: %w", err)
	}
	if err := a.storage.SetConfig("kiro.userAgent", config.UserAgent); err != nil {
		return fmt.Errorf("failed to save user agent: %w", err)
	}
	if err := a.storage.SetConfig("kiro.version", config.Version); err != nil {
		return fmt.Errorf("failed to save version: %w", err)
	}
	if err := a.storage.SetConfig("kiro.bufferedStream", fmt.Sprintf("%v", config.BufferedStream)); err != nil {
		return fmt.Errorf("failed to save buffered stream: %w", err)
	}

	return nil
}

type KiroTestResult struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresAt    string `json:"expiresAt"`
	ProfileArn   string `json:"profileArn,omitempty"`
	Region       string `json:"region,omitempty"`
}

type KiroUsageInput struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ProfileArn   string `json:"profileArn,omitempty"`
	Region       string `json:"region,omitempty"`
	ProxyURL     string `json:"proxyUrl,omitempty"`
	UserAgent    string `json:"userAgent,omitempty"`
	Version      string `json:"version,omitempty"`
}

type KiroUsageResult struct {
	SubscriptionTitle string                        `json:"subscriptionTitle,omitempty"`
	UsageLimit        float64                       `json:"usageLimit"`
	CurrentUsage      float64                       `json:"currentUsage"`
	Balance           float64                       `json:"balance"`
	UsagePct          float64                       `json:"usagePct"`
	IsLowBalance      bool                          `json:"isLowBalance"`
	Details           *kiroCore.UsageLimitsResponse `json:"details,omitempty"`
}

// TestKiroRefreshToken validates the provided refreshToken by exchanging it for a new accessToken.
// This is a best-effort network call and does not persist anything by itself.
func (a *App) TestKiroRefreshToken(config *KiroConfig) (*KiroTestResult, error) {
	if config == nil {
		return nil, fmt.Errorf("nil config")
	}
	refreshToken := strings.TrimSpace(config.RefreshToken)
	if refreshToken == "" {
		return nil, fmt.Errorf("refresh token is required")
	}

	// Validate IdC fields
	authMethod := strings.ToLower(strings.TrimSpace(config.AuthMethod))
	if authMethod == "idc" {
		if strings.TrimSpace(config.ClientId) == "" || strings.TrimSpace(config.ClientSecret) == "" {
			return nil, fmt.Errorf("clientId and clientSecret are required for IdC authentication")
		}
	}

	region := strings.TrimSpace(config.Region)
	if region == "" {
		region = "us-east-1"
	}

	creds := &kiroShared.KiroCredentials{
		RefreshToken: refreshToken,
		Region:       region,
		AuthMethod:   config.AuthMethod,
		ClientId:     config.ClientId,
		ClientSecret: config.ClientSecret,
	}

	mgr := kiroClaude.NewKiroAuthManager(creds, "", strings.TrimSpace(config.ProxyURL), strings.TrimSpace(config.Version))
	accessToken, err := mgr.ForceRefresh()
	if err != nil {
		return nil, err
	}

	expiresAt := ""
	if !creds.ExpiresAt.IsZero() {
		expiresAt = creds.ExpiresAt.Format(time.RFC3339Nano)
	}

	return &KiroTestResult{
		AccessToken:  accessToken,
		RefreshToken: strings.TrimSpace(creds.RefreshToken),
		ExpiresAt:    expiresAt,
		ProfileArn:   strings.TrimSpace(creds.ProfileArn),
		Region:       strings.TrimSpace(creds.Region),
	}, nil
}

// GetKiroUsage fetches current account usage limits based on the provided accessToken.
func (a *App) GetKiroUsage(input *KiroUsageInput) (*KiroUsageResult, error) {
	if input == nil {
		return nil, fmt.Errorf("nil input")
	}
	accessToken := strings.TrimSpace(input.AccessToken)
	if accessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}

	machineID := ""
	if v := strings.TrimSpace(input.RefreshToken); v != "" {
		machineID = kiroCore.ComputeMachineID(v)
	} else if a != nil && a.storage != nil {
		if v, getErr := a.storage.GetConfig("kiro.machineId"); getErr == nil {
			machineID = strings.TrimSpace(v)
		}
	}

	baseCtx := a.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(baseCtx, 12*time.Second)
	defer cancel()

	proxyURL := strings.TrimSpace(input.ProxyURL)
	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 10*time.Second)

	result, err := kiroCore.FetchUsage(ctx, client, kiroCore.UsageQuery{
		AccessToken:   accessToken,
		ProfileArn:    strings.TrimSpace(input.ProfileArn),
		Region:        strings.TrimSpace(input.Region),
		MachineID:     machineID,
		UserAgentBase: strings.TrimSpace(input.UserAgent),
		Version:       strings.TrimSpace(input.Version),
	})
	if err != nil {
		return nil, err
	}

	return &KiroUsageResult{
		SubscriptionTitle: strings.TrimSpace(result.SubscriptionTitle),
		UsageLimit:        result.UsageLimit,
		CurrentUsage:      result.CurrentUsage,
		Balance:           result.Balance,
		UsagePct:          result.UsagePct,
		IsLowBalance:      result.IsLowBalance,
		Details:           result.Details,
	}, nil
}
