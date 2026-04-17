package middleware

import (
	"strings"

	"clisimplehub/internal/config"
	"clisimplehub/internal/storage"
)

type resolvedClaudeMessagesConfig struct {
	Mode                   string
	StrictMode             bool
	AuthMode               string
	CacheUserID            bool
	CacheSessionID         bool
	SensitiveWords         []string
	ExperimentalCCHSigning bool
}

func resolveClaudeMessagesConfig(endpoint *storage.Endpoint) resolvedClaudeMessagesConfig {
	cfg := resolvedClaudeMessagesConfig{
		Mode:           "auto",
		AuthMode:       "auto",
		CacheUserID:    false,
		CacheSessionID: true,
	}
	if endpoint == nil || endpoint.ClaudeMessages == nil {
		return cfg
	}
	return mergeClaudeMessagesConfig(cfg, endpoint.ClaudeMessages)
}

func mergeClaudeMessagesConfig(base resolvedClaudeMessagesConfig, override *config.ClaudeMessagesCloakConfig) resolvedClaudeMessagesConfig {
	if override == nil {
		return base
	}
	if mode := strings.ToLower(strings.TrimSpace(override.Mode)); mode != "" {
		base.Mode = mode
	}
	if authMode := strings.ToLower(strings.TrimSpace(override.AuthMode)); authMode != "" {
		base.AuthMode = authMode
	}
	base.StrictMode = override.StrictMode
	base.SensitiveWords = append([]string(nil), override.SensitiveWords...)
	base.ExperimentalCCHSigning = override.ExperimentalCCHSigning
	if override.CacheUserID != nil {
		base.CacheUserID = *override.CacheUserID
	}
	if override.CacheSessionID != nil {
		base.CacheSessionID = *override.CacheSessionID
	}
	return base
}
