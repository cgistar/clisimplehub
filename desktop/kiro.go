package main

import (
	"context"
	"fmt"
	"path/filepath"
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
	RefreshToken string `json:"refreshToken"`
	ProfileArn   string `json:"profileArn"`
	Region       string `json:"region"`
	ProxyURL     string `json:"proxyUrl"`
	UserAgent    string `json:"userAgent"`
	Version      string `json:"version"`
	AccessToken  string `json:"accessToken,omitempty"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
}

func (a *App) getKiroAuthTokenPath() string {
	if a != nil && a.configLoader != nil {
		if p := strings.TrimSpace(a.configLoader.GetPath()); p != "" {
			return filepath.Join(filepath.Dir(p), filepath.Base(kiroShared.GetDefaultKiroCredentialsPath()))
		}
	}
	return kiroShared.GetDefaultKiroCredentialsPath()
}

// GetKiroConfig retrieves Kiro configuration
func (a *App) GetKiroConfig() (*KiroConfig, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	config := &KiroConfig{
		Region: "us-east-1",
	}

	credsPath := a.getKiroAuthTokenPath()
	if creds, err := kiroShared.LoadKiroCredentials(credsPath); err == nil && creds != nil {
		config.RefreshToken = creds.RefreshToken
		config.ProfileArn = creds.ProfileArn
		config.AccessToken = creds.AccessToken
		if !creds.ExpiresAt.IsZero() {
			config.ExpiresAt = creds.ExpiresAt.Format(time.RFC3339Nano)
		}
		if strings.TrimSpace(creds.Region) != "" {
			config.Region = creds.Region
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
		creds.AuthMethod = ""
		creds.Provider = ""
	} else {
		// Keep/overwrite profileArn only if explicitly provided.
		if newProfileArn != "" && newProfileArn != oldProfileArn {
			creds.ProfileArn = newProfileArn
		}
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

	region := strings.TrimSpace(config.Region)
	if region == "" {
		region = "us-east-1"
	}

	creds := &kiroShared.KiroCredentials{
		RefreshToken: refreshToken,
		Region:       region,
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
