package codexplugin

import (
	"net/http"
	"strings"

	codexShared "clisimplehub/internal/codex/shared"
	"github.com/google/uuid"
)

func applyCodexHeaders(req *http.Request, accessToken, accountID string, isStreaming bool, config *codexShared.CodexMultiConfig, clientHeaders http.Header) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	// Get config values with defaults
	clientVersion := "0.101.0"
	userAgent := "codex_cli_rs/0.101.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464"
	originator := "codex_cli_rs"
	if config != nil {
		clientVersion = config.GetClientVersion()
		userAgent = config.GetUserAgent()
		originator = config.GetOriginator()
	}

	// Use EnsureHeader pattern: preserve client headers if present, fallback to defaults
	// Priority: already set > client provided > default value
	ensureHeader(req.Header, clientHeaders, "Version", clientVersion)
	ensureHeader(req.Header, clientHeaders, "Session_id", uuid.NewString())
	ensureHeader(req.Header, clientHeaders, "User-Agent", userAgent)

	if isStreaming {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("Connection", "Keep-Alive")

	// Only set Originator and Chatgpt-Account-Id for non-API-key mode
	// API key mode detection: if Authorization was already set by client, it's API key mode
	isAPIKeyMode := false
	// For now, we always use refresh token mode, so always set these headers
	if !isAPIKeyMode {
		req.Header.Set("Originator", originator)
		if accountID != "" {
			req.Header.Set("Chatgpt-Account-Id", accountID)
		}
	}
}

// ensureHeader sets header with priority: clientHeaders > targetHeaders > defaultValue
// This matches CLIProxyAPIPlus's misc.EnsureHeader behavior
func ensureHeader(targetHeaders http.Header, clientHeaders http.Header, key, defaultValue string) {
	// Try to copy from client headers first (highest priority)
	if clientHeaders != nil {
		if clientValue := strings.TrimSpace(clientHeaders.Get(key)); clientValue != "" {
			targetHeaders.Set(key, clientValue)
			return
		}
	}
	// If already set in target, keep it (second priority)
	if existingValue := strings.TrimSpace(targetHeaders.Get(key)); existingValue != "" {
		return
	}
	// Use default value (lowest priority)
	if trimmedDefault := strings.TrimSpace(defaultValue); trimmedDefault != "" {
		targetHeaders.Set(key, trimmedDefault)
	}
}

func getCodexUpstreamURL(config *codexShared.CodexMultiConfig) string {
	baseURL := "https://chatgpt.com/backend-api/codex"
	if config != nil {
		baseURL = config.GetBaseURL()
	}
	// Normalize: remove trailing /responses if present to avoid duplication
	baseURL = strings.TrimSuffix(strings.TrimSuffix(baseURL, "/"), "/responses")
	return baseURL + "/responses"
}
