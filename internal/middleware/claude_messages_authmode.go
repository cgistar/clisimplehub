package middleware

import (
	"strings"

	"clisimplehub/internal/storage"
)

func resolveClaudeMessagesAuthMode(endpoint *storage.Endpoint, cfg resolvedClaudeMessagesConfig) string {
	if cfg.AuthMode == "oauth" || cfg.AuthMode == "api_key" {
		return cfg.AuthMode
	}
	if endpoint == nil {
		return "api_key"
	}
	apiKey := strings.TrimSpace(endpoint.APIKey)
	if strings.Contains(apiKey, "sk-ant-oat") {
		return "oauth"
	}
	return "api_key"
}
