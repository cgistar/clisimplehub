package kiroplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"
	"time"

	kiroCore "clisimplehub/internal/kiro"
	kiroClaude "clisimplehub/internal/kiro/claude"
	kiroClient "clisimplehub/internal/kiro/client"
	kiroIdc "clisimplehub/internal/kiro/idc"
	kiroShared "clisimplehub/internal/kiro/shared"
	"clisimplehub/internal/plugin"
)

// desktopFacade implements plugin.KiroDesktopProvider.
// Stateless — all config data is passed via configPath parameters.
type desktopFacade struct{}

// --- accountJSON: JSON shape matching desktop's KiroAccountDTO ---

type accountJSON struct {
	RefreshToken       string          `json:"refreshToken"`
	AccessToken        string          `json:"accessToken,omitempty"`
	ProfileArn         string          `json:"profileArn,omitempty"`
	ExpiresAt          string          `json:"expiresAt,omitempty"`
	Region             string          `json:"region,omitempty"`
	AuthMethod         string          `json:"authMethod,omitempty"`
	Provider           string          `json:"provider,omitempty"`
	ClientId           string          `json:"clientId,omitempty"`
	ClientSecret       string          `json:"clientSecret,omitempty"`
	MachineId          string          `json:"machineId,omitempty"`
	Status             string          `json:"status"`
	Weight             int             `json:"weight,omitempty"`
	SubscriptionTitle  string          `json:"subscriptionTitle,omitempty"`
	UsageLimit         float64         `json:"usageLimit,omitempty"`
	CurrentUsage       float64         `json:"currentUsage,omitempty"`
	Balance            float64         `json:"balance"`
	UsagePct           float64         `json:"usagePct,omitempty"`
	Email              string          `json:"email,omitempty"`
	UserID             string          `json:"userId,omitempty"`
	DaysUntilReset     *int32          `json:"daysUntilReset,omitempty"`
	NextDateReset      *float64        `json:"nextDateReset,omitempty"`
	LastUsageCheck     string          `json:"lastUsageCheck,omitempty"`
	UsageBreakdownList json.RawMessage `json:"usageBreakdownList,omitempty"`
	ProxyUrl           string          `json:"proxyUrl,omitempty"`
	UserAgent          string          `json:"userAgent,omitempty"`
	Version            string          `json:"version,omitempty"`
	CreatedAt          string          `json:"createdAt,omitempty"`
	UpdatedAt          string          `json:"updatedAt,omitempty"`
	IsActive           bool            `json:"isActive"`
}

func toAccountJSON(a *kiroShared.KiroAccount, isActive bool) accountJSON {
	j := accountJSON{
		RefreshToken:       a.RefreshToken,
		AccessToken:        a.AccessToken,
		ProfileArn:         a.ProfileArn,
		Region:             a.Region,
		AuthMethod:         a.AuthMethod,
		Provider:           a.Provider,
		ClientId:           a.ClientId,
		ClientSecret:       a.ClientSecret,
		MachineId:          a.MachineId,
		Status:             string(a.Status),
		Weight:             a.Weight,
		SubscriptionTitle:  a.SubscriptionTitle,
		UsageLimit:         a.UsageLimit,
		CurrentUsage:       a.CurrentUsage,
		Balance:            a.Balance,
		UsagePct:           a.UsagePct,
		Email:              a.Email,
		UserID:             a.UserID,
		DaysUntilReset:     a.DaysUntilReset,
		NextDateReset:      a.NextDateReset,
		UsageBreakdownList: a.UsageBreakdownList,
		ProxyUrl:           a.ProxyUrl,
		UserAgent:          a.UserAgent,
		Version:            a.Version,
		IsActive:           isActive,
	}
	if !a.ExpiresAt.IsZero() {
		j.ExpiresAt = a.ExpiresAt.Format(time.RFC3339Nano)
	}
	if !a.LastUsageCheck.IsZero() {
		j.LastUsageCheck = a.LastUsageCheck.Format(time.RFC3339Nano)
	}
	if !a.CreatedAt.IsZero() {
		j.CreatedAt = a.CreatedAt.Format(time.RFC3339Nano)
	}
	if !a.UpdatedAt.IsZero() {
		j.UpdatedAt = a.UpdatedAt.Format(time.RFC3339Nano)
	}
	return j
}

// parseTimeFlexible parses a time string using RFC3339Nano, falling back to RFC3339.
func parseTimeFlexible(s string) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func fromAccountJSON(j *accountJSON) *kiroShared.KiroAccount {
	a := &kiroShared.KiroAccount{
		RefreshToken:       j.RefreshToken,
		AccessToken:        j.AccessToken,
		ProfileArn:         j.ProfileArn,
		Region:             j.Region,
		AuthMethod:         j.AuthMethod,
		Provider:           j.Provider,
		ClientId:           j.ClientId,
		ClientSecret:       j.ClientSecret,
		MachineId:          j.MachineId,
		Status:             kiroShared.KiroAccountStatus(j.Status),
		Weight:             j.Weight,
		SubscriptionTitle:  j.SubscriptionTitle,
		UsageLimit:         j.UsageLimit,
		CurrentUsage:       j.CurrentUsage,
		Balance:            j.Balance,
		UsagePct:           j.UsagePct,
		Email:              j.Email,
		UserID:             j.UserID,
		DaysUntilReset:     j.DaysUntilReset,
		NextDateReset:      j.NextDateReset,
		UsageBreakdownList: j.UsageBreakdownList,
		ProxyUrl:           j.ProxyUrl,
		UserAgent:          j.UserAgent,
		Version:            j.Version,
	}
	if j.ExpiresAt != "" {
		if t, ok := parseTimeFlexible(j.ExpiresAt); ok {
			a.ExpiresAt = t
		}
	}
	if j.LastUsageCheck != "" {
		if t, ok := parseTimeFlexible(j.LastUsageCheck); ok {
			a.LastUsageCheck = t
		}
	}
	if j.CreatedAt != "" {
		if t, ok := parseTimeFlexible(j.CreatedAt); ok {
			a.CreatedAt = t
		}
	}
	if j.UpdatedAt != "" {
		if t, ok := parseTimeFlexible(j.UpdatedAt); ok {
			a.UpdatedAt = t
		}
	}
	return a
}

// --- helpers ---

func (f *desktopFacade) loadOrCreateMultiConfig(configPath string) (*kiroShared.KiroMultiConfig, error) {
	config, err := kiroShared.LoadKiroMultiConfig(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &kiroShared.KiroMultiConfig{
				ModelMapping: kiroShared.DefaultKiroModelMapping(),
				UserAgent:    kiroShared.DefaultKiroUserAgentBase,
				Version:      kiroShared.DefaultKiroVersion,
				Accounts:     []kiroShared.KiroAccount{},
			}, nil
		}
		return nil, err
	}
	return config, nil
}

func marshalJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// --- KiroDesktopProvider: Config path ---

func (f *desktopFacade) DefaultMultiConfigBasename() string {
	return kiroShared.GetDefaultKiroMultiConfigPath()
}

func (f *desktopFacade) GetProxyURL(configPath string) string {
	mc, err := f.loadOrCreateMultiConfig(configPath)
	if err != nil || mc == nil {
		return ""
	}
	return strings.TrimSpace(mc.ProxyURL)
}

// --- Defaults ---

func (f *desktopFacade) DefaultModelMapping() map[string]string {
	return kiroShared.DefaultKiroModelMapping()
}
func (f *desktopFacade) DefaultUserAgent() string { return kiroShared.DefaultKiroUserAgentBase }
func (f *desktopFacade) DefaultVersion() string   { return kiroShared.DefaultKiroVersion }

// --- Config operations ---

type kiroConfigJSON struct {
	RefreshToken   string `json:"refreshToken"`
	ProfileArn     string `json:"profileArn"`
	Region         string `json:"region"`
	ProxyURL       string `json:"proxyUrl"`
	UserAgent      string `json:"userAgent"`
	Version        string `json:"version"`
	BufferedStream bool   `json:"bufferedStream"`
	AuthMethod     string `json:"authMethod"`
	Provider       string `json:"provider"`
	ClientId       string `json:"clientId"`
	ClientSecret   string `json:"clientSecret"`
	AccessToken    string `json:"accessToken,omitempty"`
	ExpiresAt      string `json:"expiresAt,omitempty"`
}

func (f *desktopFacade) GetKiroConfig(configPath string) (json.RawMessage, error) {
	cfg := &kiroConfigJSON{Region: "us-east-1", AuthMethod: "social"}
	mc, err := f.loadOrCreateMultiConfig(configPath)
	if err == nil && mc != nil {
		cfg.ProxyURL = mc.ProxyURL
		cfg.UserAgent = mc.UserAgent
		cfg.Version = mc.Version
		cfg.BufferedStream = mc.BufferedStream
		if account := mc.GetActiveAccount(); account != nil {
			cfg.RefreshToken = account.RefreshToken
			cfg.ProfileArn = account.ProfileArn
			cfg.AccessToken = account.AccessToken
			cfg.AuthMethod = account.AuthMethod
			cfg.Provider = account.Provider
			cfg.ClientId = account.ClientId
			cfg.ClientSecret = account.ClientSecret
			if !account.ExpiresAt.IsZero() {
				cfg.ExpiresAt = account.ExpiresAt.Format(time.RFC3339Nano)
			}
			if strings.TrimSpace(account.Region) != "" {
				cfg.Region = account.Region
			}
			if strings.TrimSpace(cfg.AuthMethod) == "" {
				cfg.AuthMethod = "social"
			}
		}
	}
	return marshalJSON(cfg), nil
}

func (f *desktopFacade) SaveKiroConfig(configPath string, config json.RawMessage) (json.RawMessage, error) {
	var cfg kiroConfigJSON
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config JSON: %w", err)
	}
	if strings.TrimSpace(cfg.RefreshToken) == "" {
		return nil, fmt.Errorf("refresh token is required")
	}
	authMethod := strings.ToLower(strings.TrimSpace(cfg.AuthMethod))
	if authMethod == "idc" {
		if strings.TrimSpace(cfg.ClientId) == "" || strings.TrimSpace(cfg.ClientSecret) == "" {
			return nil, fmt.Errorf("clientId and clientSecret are required for IdC authentication")
		}
	}

	multiConfig, err := f.loadOrCreateMultiConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kiro multi config: %w", err)
	}

	newRefreshToken := strings.TrimSpace(cfg.RefreshToken)
	newRegion := strings.TrimSpace(cfg.Region)
	if newRegion == "" {
		newRegion = "us-east-1"
	}
	fallbackMachineID := kiroCore.ComputeMachineID(newRefreshToken)

	account := multiConfig.FindAccountByRefreshToken(newRefreshToken)
	if account == nil {
		activeAccount := multiConfig.GetActiveAccount()
		if activeAccount != nil {
			oldRefreshToken := strings.TrimSpace(activeAccount.RefreshToken)
			refreshTokenChanged := oldRefreshToken != "" && oldRefreshToken != newRefreshToken
			if refreshTokenChanged {
				if strings.TrimSpace(cfg.AccessToken) == "" {
					return nil, fmt.Errorf("accessToken is required when refreshToken changes; please test first")
				}
				if strings.TrimSpace(cfg.ExpiresAt) == "" {
					return nil, fmt.Errorf("expiresAt is required when refreshToken changes; please test first")
				}
				account = activeAccount
			}
		}
	}

	if account != nil {
		account.RefreshToken = newRefreshToken
		account.Region = newRegion
		if mid := kiroShared.NormalizeMachineID(account.MachineId); mid != "" {
			account.MachineId = mid
		} else {
			account.MachineId = fallbackMachineID
		}
		account.AuthMethod = cfg.AuthMethod
		account.Provider = cfg.Provider
		account.ClientId = cfg.ClientId
		account.ClientSecret = cfg.ClientSecret

		accessToken := strings.TrimSpace(cfg.AccessToken)
		expiresAtStr := strings.TrimSpace(cfg.ExpiresAt)
		if accessToken != "" && expiresAtStr != "" {
			expiresAt, err := time.Parse(time.RFC3339Nano, expiresAtStr)
			if err != nil {
				expiresAt, err = time.Parse(time.RFC3339, expiresAtStr)
			}
			if err != nil {
				return nil, fmt.Errorf("invalid expiresAt: %w", err)
			}
			account.AccessToken = accessToken
			account.ExpiresAt = expiresAt
		}

		if strings.TrimSpace(cfg.ProfileArn) != "" {
			account.ProfileArn = cfg.ProfileArn
		}
		if authMethod == "idc" {
			account.ProfileArn = ""
		}
		account.Status = kiroShared.KiroStatusValid
		account.UpdatedAt = time.Now()
		multiConfig.UpdateAccount(account)
	} else {
		now := time.Now()
		newAccount := &kiroShared.KiroAccount{
			RefreshToken: newRefreshToken,
			Region:       newRegion,
			MachineId:    fallbackMachineID,
			AuthMethod:   cfg.AuthMethod,
			Provider:     cfg.Provider,
			ClientId:     cfg.ClientId,
			ClientSecret: cfg.ClientSecret,
			Status:       kiroShared.KiroStatusValid,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		accessToken := strings.TrimSpace(cfg.AccessToken)
		expiresAtStr := strings.TrimSpace(cfg.ExpiresAt)
		if accessToken != "" && expiresAtStr != "" {
			expiresAt, err := time.Parse(time.RFC3339Nano, expiresAtStr)
			if err != nil {
				expiresAt, err = time.Parse(time.RFC3339, expiresAtStr)
			}
			if err != nil {
				return nil, fmt.Errorf("invalid expiresAt: %w", err)
			}
			newAccount.AccessToken = accessToken
			newAccount.ExpiresAt = expiresAt
		}

		if strings.TrimSpace(cfg.ProfileArn) != "" {
			newAccount.ProfileArn = cfg.ProfileArn
		}
		if authMethod == "idc" {
			newAccount.ProfileArn = ""
		}
		multiConfig.Accounts = append(multiConfig.Accounts, *newAccount)
	}

	multiConfig.ActiveRefreshToken = newRefreshToken
	multiConfig.ProxyURL = cfg.ProxyURL
	multiConfig.UserAgent = cfg.UserAgent
	multiConfig.Version = cfg.Version
	multiConfig.BufferedStream = cfg.BufferedStream

	if err := kiroShared.SaveKiroMultiConfig(configPath, multiConfig); err != nil {
		return nil, err
	}
	// Return the saved config state
	return f.GetKiroConfig(configPath)
}

type kiroGlobalConfigJSON struct {
	Region         string            `json:"region"`
	ProxyURL       string            `json:"proxyUrl"`
	UserAgent      string            `json:"userAgent"`
	Version        string            `json:"version"`
	BufferedStream bool              `json:"bufferedStream"`
	RotationMode   string            `json:"rotationMode"`
	ModelMapping   map[string]string `json:"modelMapping"`
}

func (f *desktopFacade) GetKiroGlobalConfig(configPath string) (json.RawMessage, error) {
	mc, err := f.loadOrCreateMultiConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kiro config: %w", err)
	}
	mapping := mc.ModelMapping
	if len(mapping) == 0 {
		mapping = kiroShared.DefaultKiroModelMapping()
	}
	clone := make(map[string]string, len(mapping))
	maps.Copy(clone, mapping)
	return marshalJSON(&kiroGlobalConfigJSON{
		Region:         mc.GetRegion(),
		ProxyURL:       mc.ProxyURL,
		UserAgent:      mc.UserAgent,
		Version:        mc.Version,
		BufferedStream: mc.BufferedStream,
		RotationMode:   mc.GetRotationMode(),
		ModelMapping:   clone,
	}), nil
}

func (f *desktopFacade) SaveKiroGlobalConfig(configPath string, dto json.RawMessage) error {
	var cfg kiroGlobalConfigJSON
	if err := json.Unmarshal(dto, &cfg); err != nil {
		return fmt.Errorf("invalid global config JSON: %w", err)
	}

	mc, err := f.loadOrCreateMultiConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load kiro config: %w", err)
	}
	mc.Region = strings.TrimSpace(cfg.Region)
	mc.ProxyURL = strings.TrimSpace(cfg.ProxyURL)
	mc.UserAgent = strings.TrimSpace(cfg.UserAgent)
	mc.Version = strings.TrimSpace(cfg.Version)
	mc.BufferedStream = cfg.BufferedStream
	mc.RotationMode = strings.TrimSpace(cfg.RotationMode)
	if cfg.ModelMapping != nil {
		sanitized := make(map[string]string, len(cfg.ModelMapping))
		for k, v := range cfg.ModelMapping {
			alias := strings.TrimSpace(k)
			name := strings.TrimSpace(v)
			if alias != "" && name != "" {
				sanitized[alias] = name
			}
		}
		mc.ModelMapping = sanitized
	}

	if err := kiroShared.SaveKiroMultiConfig(configPath, mc); err != nil {
		return fmt.Errorf("failed to save kiro config: %w", err)
	}

	kiroClaude.SetCachedBufferedStream(mc.BufferedStream)
	kiroClaude.SetCachedModelMapping(mc.ModelMapping)
	if err := kiroClaude.ReloadAllTransformers(); err != nil {
		return fmt.Errorf("saved kiro config but failed to reload transformers: %w", err)
	}
	return nil
}

// --- Account CRUD ---

type accountsResponseJSON struct {
	ActiveRefreshToken string        `json:"activeRefreshToken"`
	Accounts           []accountJSON `json:"accounts"`
}

func (f *desktopFacade) GetAccounts(configPath string) (json.RawMessage, error) {
	config, err := f.loadOrCreateMultiConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kiro config: %w", err)
	}
	accounts := make([]accountJSON, 0, len(config.Accounts))
	for i := range config.Accounts {
		isActive := config.Accounts[i].RefreshToken == config.ActiveRefreshToken
		accounts = append(accounts, toAccountJSON(&config.Accounts[i], isActive))
	}
	return marshalJSON(&accountsResponseJSON{
		ActiveRefreshToken: config.ActiveRefreshToken,
		Accounts:           accounts,
	}), nil
}

func (f *desktopFacade) GetActiveAccount(configPath string) (json.RawMessage, error) {
	config, err := f.loadOrCreateMultiConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kiro config: %w", err)
	}
	account := config.GetActiveAccount()
	if account == nil {
		return []byte("null"), nil
	}
	return marshalJSON(toAccountJSON(account, true)), nil
}

func (f *desktopFacade) SetActiveAccount(configPath, refreshToken string) error {
	if strings.TrimSpace(refreshToken) == "" {
		return fmt.Errorf("refreshToken is required")
	}
	config, err := f.loadOrCreateMultiConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load kiro config: %w", err)
	}
	if config.FindAccountByRefreshToken(refreshToken) == nil {
		return fmt.Errorf("account not found")
	}
	config.ActiveRefreshToken = refreshToken
	return kiroShared.SaveKiroMultiConfig(configPath, config)
}

func (f *desktopFacade) AddAccount(configPath string, dto json.RawMessage) (json.RawMessage, error) {
	var j accountJSON
	if err := json.Unmarshal(dto, &j); err != nil {
		return nil, fmt.Errorf("invalid account JSON: %w", err)
	}
	if strings.TrimSpace(j.RefreshToken) == "" {
		return nil, fmt.Errorf("refreshToken is required")
	}

	config, err := f.loadOrCreateMultiConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kiro config: %w", err)
	}
	if config.FindAccountByRefreshToken(j.RefreshToken) != nil {
		return nil, fmt.Errorf("account with this refreshToken already exists")
	}

	now := time.Now()
	account := fromAccountJSON(&j)
	if mid := kiroShared.NormalizeMachineID(account.MachineId); mid != "" {
		account.MachineId = mid
	} else {
		account.MachineId = kiroCore.ComputeMachineID(account.RefreshToken)
	}
	account.CreatedAt = now
	account.UpdatedAt = now
	if account.Status == "" {
		account.Status = kiroShared.KiroStatusUnknown
	}
	if account.Region == "" {
		account.Region = "us-east-1"
	}
	config.Accounts = append(config.Accounts, *account)

	if len(config.Accounts) == 1 {
		config.ActiveRefreshToken = account.RefreshToken
	}

	if err := kiroShared.SaveKiroMultiConfig(configPath, config); err != nil {
		return nil, fmt.Errorf("failed to save kiro config: %w", err)
	}

	isActive := account.RefreshToken == config.ActiveRefreshToken
	return marshalJSON(toAccountJSON(account, isActive)), nil
}

func (f *desktopFacade) UpdateAccount(configPath string, dto json.RawMessage) error {
	var j accountJSON
	if err := json.Unmarshal(dto, &j); err != nil {
		return fmt.Errorf("invalid account JSON: %w", err)
	}
	if strings.TrimSpace(j.RefreshToken) == "" {
		return fmt.Errorf("refreshToken is required")
	}

	config, err := f.loadOrCreateMultiConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load kiro config: %w", err)
	}
	existing := config.FindAccountByRefreshToken(j.RefreshToken)
	if existing == nil {
		return fmt.Errorf("account not found")
	}

	account := fromAccountJSON(&j)
	account.CreatedAt = existing.CreatedAt
	account.UpdatedAt = time.Now()
	if mid := kiroShared.NormalizeMachineID(account.MachineId); mid != "" {
		account.MachineId = mid
	} else {
		account.MachineId = existing.MachineId
	}
	if !config.UpdateAccount(account) {
		return fmt.Errorf("failed to update account")
	}
	return kiroShared.SaveKiroMultiConfig(configPath, config)
}

func (f *desktopFacade) DeleteAccount(configPath, refreshToken string) error {
	if strings.TrimSpace(refreshToken) == "" {
		return fmt.Errorf("refreshToken is required")
	}
	config, err := f.loadOrCreateMultiConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load kiro config: %w", err)
	}
	if !config.DeleteAccount(refreshToken) {
		return fmt.Errorf("account not found")
	}
	return kiroShared.SaveKiroMultiConfig(configPath, config)
}

// --- Auth testing ---

type testResultJSON struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresAt    string `json:"expiresAt"`
	ProfileArn   string `json:"profileArn,omitempty"`
	Region       string `json:"region,omitempty"`
}

func (f *desktopFacade) TestRefreshToken(config json.RawMessage) (json.RawMessage, error) {
	var cfg kiroConfigJSON
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config JSON: %w", err)
	}
	refreshToken := strings.TrimSpace(cfg.RefreshToken)
	if refreshToken == "" {
		return nil, fmt.Errorf("refresh token is required")
	}
	authMethod := strings.ToLower(strings.TrimSpace(cfg.AuthMethod))
	if authMethod == "idc" {
		if strings.TrimSpace(cfg.ClientId) == "" || strings.TrimSpace(cfg.ClientSecret) == "" {
			return nil, fmt.Errorf("clientId and clientSecret are required for IdC authentication")
		}
	}

	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = "us-east-1"
	}

	creds := &kiroShared.KiroCredentials{
		RefreshToken: refreshToken,
		Region:       region,
		AuthMethod:   cfg.AuthMethod,
		ClientId:     cfg.ClientId,
		ClientSecret: cfg.ClientSecret,
	}
	mgr := kiroClaude.NewKiroAuthManager(creds, "", strings.TrimSpace(cfg.ProxyURL), strings.TrimSpace(cfg.Version))
	accessToken, err := mgr.ForceRefresh()
	if err != nil {
		return nil, err
	}

	expiresAt := ""
	if !creds.ExpiresAt.IsZero() {
		expiresAt = creds.ExpiresAt.Format(time.RFC3339Nano)
	}

	return marshalJSON(&testResultJSON{
		AccessToken:  accessToken,
		RefreshToken: strings.TrimSpace(creds.RefreshToken),
		ExpiresAt:    expiresAt,
		ProfileArn:   strings.TrimSpace(creds.ProfileArn),
		Region:       strings.TrimSpace(creds.Region),
	}), nil
}

func (f *desktopFacade) TestAccount(configPath, refreshToken string) (json.RawMessage, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, fmt.Errorf("refreshToken is required")
	}
	config, err := f.loadOrCreateMultiConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kiro config: %w", err)
	}
	account := config.FindAccountByRefreshToken(refreshToken)
	if account == nil {
		return nil, fmt.Errorf("account not found")
	}

	testCfg := marshalJSON(&kiroConfigJSON{
		RefreshToken: account.RefreshToken,
		Region:       account.Region,
		AuthMethod:   account.AuthMethod,
		ClientId:     account.ClientId,
		ClientSecret: account.ClientSecret,
		ProxyURL:     account.ProxyUrl,
		Version:      account.Version,
	})

	resultJSON, err := f.TestRefreshToken(testCfg)
	if err != nil {
		account.Status = kiroShared.KiroStatusUnknown
		config.UpdateAccount(account)
		_ = kiroShared.SaveKiroMultiConfig(configPath, config)
		return nil, err
	}

	var result testResultJSON
	_ = json.Unmarshal(resultJSON, &result)

	account.AccessToken = result.AccessToken
	account.ProfileArn = result.ProfileArn
	if result.ExpiresAt != "" {
		if t, ok := parseTimeFlexible(result.ExpiresAt); ok {
			account.ExpiresAt = t
		}
	}
	if result.RefreshToken != "" && result.RefreshToken != account.RefreshToken {
		account.RefreshToken = result.RefreshToken
		if strings.TrimSpace(account.MachineId) == "" {
			account.MachineId = kiroCore.ComputeMachineID(result.RefreshToken)
		}
	}
	account.Status = kiroShared.KiroStatusValid
	account.UpdatedAt = time.Now()
	config.UpdateAccount(account)
	_ = kiroShared.SaveKiroMultiConfig(configPath, config)

	// 重新加载所有 Transformer，确保新 token 立即生效
	_ = kiroClaude.ReloadAllTransformers()

	return resultJSON, nil
}

// --- Usage ---

type usageInputJSON struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	MachineID    string `json:"machineId,omitempty"`
	ProfileArn   string `json:"profileArn,omitempty"`
	Region       string `json:"region,omitempty"`
	ProxyURL     string `json:"proxyUrl,omitempty"`
	UserAgent    string `json:"userAgent,omitempty"`
	Version      string `json:"version,omitempty"`
}

type usageResultJSON struct {
	SubscriptionTitle string          `json:"subscriptionTitle,omitempty"`
	UsageLimit        float64         `json:"usageLimit"`
	CurrentUsage      float64         `json:"currentUsage"`
	Balance           float64         `json:"balance"`
	UsagePct          float64         `json:"usagePct"`
	IsLowBalance      bool            `json:"isLowBalance"`
	Details           json.RawMessage `json:"details,omitempty"`
}

func (f *desktopFacade) GetKiroUsage(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var inp usageInputJSON
	if err := json.Unmarshal(input, &inp); err != nil {
		return nil, fmt.Errorf("invalid usage input: %w", err)
	}
	accessToken := strings.TrimSpace(inp.AccessToken)
	if accessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}

	machineID := strings.TrimSpace(inp.MachineID)
	if machineID == "" {
		if v := strings.TrimSpace(inp.RefreshToken); v != "" {
			machineID = kiroCore.ComputeMachineID(v)
		}
	}
	machineID = strings.TrimSpace(machineID)
	if machineID == "" {
		return nil, fmt.Errorf("machineId is required (or provide refreshToken to compute it)")
	}
	if strings.ContainsAny(machineID, "\r\n") {
		return nil, fmt.Errorf("invalid machineId")
	}

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	proxyURL := strings.TrimSpace(inp.ProxyURL)
	client := kiroClient.NewHTTPClientWithProxy(proxyURL, 10*time.Second)

	result, err := kiroCore.FetchUsage(ctx, client, kiroCore.UsageQuery{
		AccessToken:   accessToken,
		ProfileArn:    strings.TrimSpace(inp.ProfileArn),
		Region:        strings.TrimSpace(inp.Region),
		MachineID:     machineID,
		UserAgentBase: strings.TrimSpace(inp.UserAgent),
		Version:       strings.TrimSpace(inp.Version),
	})
	if err != nil {
		// Wrap UsageHTTPError as plugin.KiroUsageHTTPError
		var httpErr *kiroCore.UsageHTTPError
		if errors.As(err, &httpErr) {
			return nil, &plugin.KiroUsageHTTPError{StatusCode: httpErr.StatusCode, Body: httpErr.Body}
		}
		return nil, err
	}

	var detailsJSON json.RawMessage
	if result.Details != nil {
		detailsJSON = marshalJSON(result.Details)
	}

	return marshalJSON(&usageResultJSON{
		SubscriptionTitle: strings.TrimSpace(result.SubscriptionTitle),
		UsageLimit:        result.UsageLimit,
		CurrentUsage:      result.CurrentUsage,
		Balance:           result.Balance,
		UsagePct:          result.UsagePct,
		IsLowBalance:      result.IsLowBalance,
		Details:           detailsJSON,
	}), nil
}

func (f *desktopFacade) GetAccountUsage(ctx context.Context, configPath, refreshToken string) (json.RawMessage, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, fmt.Errorf("refreshToken is required")
	}
	config, err := f.loadOrCreateMultiConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kiro config: %w", err)
	}
	account := config.FindAccountByRefreshToken(refreshToken)
	if account == nil {
		return nil, fmt.Errorf("account not found")
	}
	if strings.TrimSpace(account.AccessToken) == "" {
		return nil, fmt.Errorf("account has no accessToken, please test first")
	}

	effectiveProxyURL := strings.TrimSpace(account.ProxyUrl)
	if effectiveProxyURL == "" {
		effectiveProxyURL = strings.TrimSpace(config.ProxyURL)
	}
	effectiveUserAgent := strings.TrimSpace(account.UserAgent)
	if effectiveUserAgent == "" {
		effectiveUserAgent = strings.TrimSpace(config.UserAgent)
	}
	effectiveVersion := strings.TrimSpace(account.Version)
	if effectiveVersion == "" {
		effectiveVersion = strings.TrimSpace(config.Version)
	}

	input := marshalJSON(&usageInputJSON{
		AccessToken:  account.AccessToken,
		RefreshToken: account.RefreshToken,
		MachineID:    account.MachineId,
		ProfileArn:   account.ProfileArn,
		Region:       account.Region,
		ProxyURL:     effectiveProxyURL,
		UserAgent:    effectiveUserAgent,
		Version:      effectiveVersion,
	})

	resultJSON, err := f.GetKiroUsage(ctx, input)
	if err != nil {
		var httpErr *plugin.KiroUsageHTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == 403 {
			var errBody struct {
				Reason string `json:"reason"`
			}
			if parseErr := json.Unmarshal([]byte(httpErr.Body), &errBody); parseErr == nil {
				if errBody.Reason == "TEMPORARILY_SUSPENDED" {
					account.Status = kiroShared.KiroStatusBanned
					account.LastUsageCheck = time.Now()
					config.UpdateAccount(account)
					_ = kiroShared.SaveKiroMultiConfig(configPath, config)
				}
			}
		}
		return nil, err
	}

	var result usageResultJSON
	_ = json.Unmarshal(resultJSON, &result)

	account.SubscriptionTitle = result.SubscriptionTitle
	account.UsageLimit = result.UsageLimit
	account.CurrentUsage = result.CurrentUsage
	account.Balance = result.Balance
	account.UsagePct = result.UsagePct
	account.LastUsageCheck = time.Now()

	if result.Details != nil {
		var details struct {
			DaysUntilReset     *int32   `json:"daysUntilReset,omitempty"`
			NextDateReset      *float64 `json:"nextDateReset,omitempty"`
			UsageBreakdownList json.RawMessage `json:"usageBreakdownList,omitempty"`
			UserInfo           *struct {
				Email  *string `json:"email,omitempty"`
				UserID *string `json:"userId,omitempty"`
			} `json:"userInfo,omitempty"`
		}
		if parseErr := json.Unmarshal(result.Details, &details); parseErr == nil {
			if details.DaysUntilReset != nil {
				account.DaysUntilReset = details.DaysUntilReset
			}
			if details.NextDateReset != nil {
				account.NextDateReset = details.NextDateReset
			}
			if len(details.UsageBreakdownList) > 0 {
				account.UsageBreakdownList = details.UsageBreakdownList
			}
			if details.UserInfo != nil {
				if details.UserInfo.Email != nil {
					if v := strings.TrimSpace(*details.UserInfo.Email); v != "" {
						account.Email = v
					}
				}
				if details.UserInfo.UserID != nil {
					if v := strings.TrimSpace(*details.UserInfo.UserID); v != "" {
						account.UserID = v
					}
				}
			}
		}
	}

	if result.Balance <= 0 {
		account.Status = kiroShared.KiroStatusExhausted
	} else {
		account.Status = kiroShared.KiroStatusValid
	}

	config.UpdateAccount(account)
	_ = kiroShared.SaveKiroMultiConfig(configPath, config)

	return resultJSON, nil
}

// --- Backup/Restore ---

type multiConfigDTOJSON struct {
	ActiveRefreshToken string            `json:"activeRefreshToken"`
	Region             string            `json:"region,omitempty"`
	ProxyURL           string            `json:"proxyUrl,omitempty"`
	UserAgent          string            `json:"userAgent,omitempty"`
	Version            string            `json:"version,omitempty"`
	BufferedStream     bool              `json:"bufferedStream,omitempty"`
	RotationMode       string            `json:"rotationMode,omitempty"`
	ModelMapping       map[string]string `json:"modelMapping,omitempty"`
	Accounts           []accountJSON     `json:"accounts"`
	ReplaceMode        bool              `json:"replaceMode,omitempty"`
}

func (f *desktopFacade) LoadMultiConfigDTO(configPath string) (json.RawMessage, error) {
	config, err := kiroShared.LoadKiroMultiConfig(configPath)
	if err != nil {
		return nil, err
	}
	if config == nil || len(config.Accounts) == 0 {
		return nil, fmt.Errorf("no accounts")
	}
	accounts := make([]accountJSON, 0, len(config.Accounts))
	for i := range config.Accounts {
		isActive := config.Accounts[i].RefreshToken == config.ActiveRefreshToken
		accounts = append(accounts, toAccountJSON(&config.Accounts[i], isActive))
	}
	return marshalJSON(&multiConfigDTOJSON{
		ActiveRefreshToken: config.ActiveRefreshToken,
		Region:             config.Region,
		ProxyURL:           config.ProxyURL,
		UserAgent:          config.UserAgent,
		Version:            config.Version,
		BufferedStream:     config.BufferedStream,
		RotationMode:       config.RotationMode,
		ModelMapping:       config.ModelMapping,
		Accounts:           accounts,
	}), nil
}

func (f *desktopFacade) SaveMultiConfigFromBackup(configPath string, dto json.RawMessage) error {
	var d multiConfigDTOJSON
	if err := json.Unmarshal(dto, &d); err != nil {
		return fmt.Errorf("invalid backup data: %w", err)
	}
	if len(d.Accounts) == 0 {
		return fmt.Errorf("no accounts in backup")
	}
	hasValid := false
	for _, acc := range d.Accounts {
		if strings.TrimSpace(acc.RefreshToken) != "" {
			hasValid = true
			break
		}
	}
	if !hasValid {
		return fmt.Errorf("no valid account found in backup")
	}

	if d.ReplaceMode {
		return f.replaceMultiConfig(configPath, &d)
	}
	return f.mergeMultiConfig(configPath, &d)
}

func (f *desktopFacade) replaceMultiConfig(configPath string, dto *multiConfigDTOJSON) error {
	accounts := make([]kiroShared.KiroAccount, 0, len(dto.Accounts))
	for i := range dto.Accounts {
		accounts = append(accounts, *fromAccountJSON(&dto.Accounts[i]))
	}

	activeRefreshToken := strings.TrimSpace(dto.ActiveRefreshToken)
	if activeRefreshToken != "" {
		found := false
		for _, acc := range accounts {
			if acc.RefreshToken == activeRefreshToken {
				found = true
				break
			}
		}
		if !found && len(accounts) > 0 {
			activeRefreshToken = accounts[0].RefreshToken
		}
	} else if len(accounts) > 0 {
		activeRefreshToken = accounts[0].RefreshToken
	}

	newConfig := &kiroShared.KiroMultiConfig{
		ActiveRefreshToken: activeRefreshToken,
		Region:             dto.Region,
		ProxyURL:           dto.ProxyURL,
		UserAgent:          dto.UserAgent,
		Version:            dto.Version,
		BufferedStream:     dto.BufferedStream,
		RotationMode:       dto.RotationMode,
		ModelMapping:       dto.ModelMapping,
		Accounts:           accounts,
	}
	return kiroShared.SaveKiroMultiConfig(configPath, newConfig)
}

func (f *desktopFacade) mergeMultiConfig(configPath string, dto *multiConfigDTOJSON) error {
	localConfig, err := f.loadOrCreateMultiConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load local config: %w", err)
	}

	localAccountMap := make(map[string]*kiroShared.KiroAccount)
	for i := range localConfig.Accounts {
		rt := strings.TrimSpace(localConfig.Accounts[i].RefreshToken)
		if rt != "" {
			localAccountMap[rt] = &localConfig.Accounts[i]
		}
	}

	mergedAccounts := make([]kiroShared.KiroAccount, 0, len(dto.Accounts))
	remoteRefreshTokens := make(map[string]bool)

	for i := range dto.Accounts {
		remoteAccount := fromAccountJSON(&dto.Accounts[i])
		rt := strings.TrimSpace(remoteAccount.RefreshToken)
		if rt == "" {
			continue
		}
		remoteRefreshTokens[rt] = true
		if localAccount, exists := localAccountMap[rt]; exists {
			mergedAccount := *remoteAccount
			if remoteAccount.CurrentUsage == 0 && localAccount.CurrentUsage > 0 {
				mergedAccount.CurrentUsage = localAccount.CurrentUsage
				mergedAccount.UsageLimit = localAccount.UsageLimit
				mergedAccount.Balance = localAccount.Balance
				mergedAccount.UsagePct = localAccount.UsagePct
				mergedAccount.LastUsageCheck = localAccount.LastUsageCheck
				mergedAccount.UsageBreakdownList = localAccount.UsageBreakdownList
			}
			if remoteAccount.Status == kiroShared.KiroStatusUnknown && localAccount.Status != kiroShared.KiroStatusUnknown {
				mergedAccount.Status = localAccount.Status
			}
			if remoteAccount.CreatedAt.IsZero() && !localAccount.CreatedAt.IsZero() {
				mergedAccount.CreatedAt = localAccount.CreatedAt
			}
			mergedAccounts = append(mergedAccounts, mergedAccount)
		} else {
			mergedAccounts = append(mergedAccounts, *remoteAccount)
		}
	}

	for rt, localAccount := range localAccountMap {
		if !remoteRefreshTokens[rt] {
			mergedAccounts = append(mergedAccounts, *localAccount)
		}
	}

	activeRefreshToken := strings.TrimSpace(dto.ActiveRefreshToken)
	if activeRefreshToken == "" {
		activeRefreshToken = localConfig.ActiveRefreshToken
	}
	if activeRefreshToken != "" {
		found := false
		for _, acc := range mergedAccounts {
			if acc.RefreshToken == activeRefreshToken {
				found = true
				break
			}
		}
		if !found && len(mergedAccounts) > 0 {
			activeRefreshToken = mergedAccounts[0].RefreshToken
		}
	} else if len(mergedAccounts) > 0 {
		activeRefreshToken = mergedAccounts[0].RefreshToken
	}

	region := dto.Region
	if region == "" {
		region = localConfig.Region
	}
	proxyURL := dto.ProxyURL
	if proxyURL == "" {
		proxyURL = localConfig.ProxyURL
	}
	userAgent := dto.UserAgent
	if userAgent == "" {
		userAgent = localConfig.UserAgent
	}
	version := dto.Version
	if version == "" {
		version = localConfig.Version
	}
	bufferedStream := dto.BufferedStream || localConfig.BufferedStream
	rotationMode := dto.RotationMode
	if rotationMode == "" {
		rotationMode = localConfig.RotationMode
	}
	modelMapping := dto.ModelMapping
	if len(modelMapping) == 0 {
		modelMapping = localConfig.ModelMapping
	}

	mergedConfig := &kiroShared.KiroMultiConfig{
		ActiveRefreshToken: activeRefreshToken,
		Region:             region,
		ProxyURL:           proxyURL,
		UserAgent:          userAgent,
		Version:            version,
		BufferedStream:     bufferedStream,
		RotationMode:       rotationMode,
		ModelMapping:       modelMapping,
		Accounts:           mergedAccounts,
	}
	return kiroShared.SaveKiroMultiConfig(configPath, mergedConfig)
}

// --- Utilities ---

func (f *desktopFacade) ComputeMachineID(refreshToken string) string {
	return kiroCore.ComputeMachineID(refreshToken)
}
func (f *desktopFacade) NormalizeMachineID(mid string) string {
	return kiroShared.NormalizeMachineID(mid)
}

// --- Transformer operations ---

func (f *desktopFacade) SetCachedBufferedStream(v bool)              { kiroClaude.SetCachedBufferedStream(v) }
func (f *desktopFacade) SetCachedModelMapping(m map[string]string)   { kiroClaude.SetCachedModelMapping(m) }
func (f *desktopFacade) ReloadAllTransformers() error                { return kiroClaude.ReloadAllTransformers() }

// --- IDC operations ---

func (f *desktopFacade) IdcRegisterClient(proxyURL, region string) (string, string, error) {
	if strings.TrimSpace(region) == "" {
		region = "us-east-1"
	}
	client := kiroIdc.NewClient(proxyURL, region)
	resp, err := client.RegisterClient(&kiroIdc.RegisterRequest{Region: region})
	if err != nil {
		return "", "", err
	}
	return resp.ClientId, resp.ClientSecret, nil
}

func (f *desktopFacade) IdcStartDeviceAuth(proxyURL, region string, req json.RawMessage) (json.RawMessage, error) {
	var params struct {
		ClientId     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
		Region       string `json:"region,omitempty"`
		StartUrl     string `json:"startUrl,omitempty"`
	}
	if err := json.Unmarshal(req, &params); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if strings.TrimSpace(region) == "" {
		region = "us-east-1"
	}
	client := kiroIdc.NewClient(proxyURL, region)
	resp, err := client.StartDeviceAuthorization(&kiroIdc.DeviceAuthRequest{
		ClientId:     params.ClientId,
		ClientSecret: params.ClientSecret,
		Region:       params.Region,
		StartUrl:     params.StartUrl,
	})
	if err != nil {
		return nil, err
	}
	return marshalJSON(resp), nil
}

func (f *desktopFacade) IdcPollToken(proxyURL, region string, req json.RawMessage) (json.RawMessage, error) {
	var params struct {
		ClientId     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
		DeviceCode   string `json:"deviceCode"`
		Region       string `json:"region,omitempty"`
	}
	if err := json.Unmarshal(req, &params); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if strings.TrimSpace(region) == "" {
		region = "us-east-1"
	}
	client := kiroIdc.NewClient(proxyURL, region)
	resp, err := client.PollToken(&kiroIdc.PollTokenRequest{
		ClientId:     params.ClientId,
		ClientSecret: params.ClientSecret,
		DeviceCode:   params.DeviceCode,
		Region:       params.Region,
	})
	if err != nil {
		return nil, err
	}
	return marshalJSON(resp), nil
}

func (f *desktopFacade) IdcRegisterAuthCodeClient(proxyURL, region, issuerUrl string) (string, string, error) {
	if strings.TrimSpace(region) == "" {
		region = "us-east-1"
	}
	client := kiroIdc.NewClient(proxyURL, region)
	resp, err := client.RegisterAuthCodeClient(&kiroIdc.AuthCodeRegisterRequest{
		Region:    region,
		IssuerUrl: issuerUrl,
	})
	if err != nil {
		return "", "", err
	}
	return resp.ClientId, resp.ClientSecret, nil
}

func (f *desktopFacade) IdcExchangeAuthCode(proxyURL, region string, req json.RawMessage) (json.RawMessage, error) {
	var params struct {
		ClientId     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
		Code         string `json:"code"`
		RedirectUri  string `json:"redirectUri"`
		CodeVerifier string `json:"codeVerifier"`
		Region       string `json:"region,omitempty"`
	}
	if err := json.Unmarshal(req, &params); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if strings.TrimSpace(region) == "" {
		region = "us-east-1"
	}
	client := kiroIdc.NewClient(proxyURL, region)
	resp, err := client.ExchangeAuthCode(&kiroIdc.AuthCodeTokenRequest{
		Region:       params.Region,
		ClientId:     params.ClientId,
		ClientSecret: params.ClientSecret,
		Code:         params.Code,
		RedirectUri:  params.RedirectUri,
		CodeVerifier: params.CodeVerifier,
	})
	if err != nil {
		return nil, err
	}
	return marshalJSON(resp), nil
}
