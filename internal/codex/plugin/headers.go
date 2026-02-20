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

	// Apply whitelist filtering to client headers (aligned with claude-relay-service L315-322)
	// Only allow: version, openai-beta, session_id
	filtered := filterClientHeaders(clientHeaders)

	// Set headers with priority: filtered client headers > defaults
	if val := filtered.Get("Version"); val != "" {
		req.Header.Set("Version", val)
	} else {
		req.Header.Set("Version", clientVersion)
	}

	if val := filtered.Get("Session_id"); val != "" {
		req.Header.Set("Session_id", val)
	} else {
		req.Header.Set("Session_id", uuid.NewString())
	}

	if val := filtered.Get("Openai-Beta"); val != "" {
		req.Header.Set("Openai-Beta", val)
	}

	req.Header.Set("User-Agent", userAgent)

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

// isCompactResponsesPath checks if the request path is for the compact endpoint
func isCompactResponsesPath(requestPath string) bool {
	normalizedPath := strings.TrimRight(strings.TrimSpace(requestPath), "/")
	return strings.HasSuffix(normalizedPath, "/responses/compact")
}

// getCodexUpstreamURL constructs the upstream URL based on config and request path.
// Aligned with claude-relay-service L362-364: dynamically selects /responses or /responses/compact.
func getCodexUpstreamURL(config *codexShared.CodexMultiConfig, requestPath string) string {
	baseURL := "https://chatgpt.com/backend-api/codex"
	if config != nil {
		if cfgBaseURL := strings.TrimSpace(config.GetBaseURL()); cfgBaseURL != "" {
			baseURL = cfgBaseURL
		}
	}

	// Normalize: remove trailing /responses or /responses/compact if present
	baseURL = strings.TrimSuffix(baseURL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/responses/compact")
	baseURL = strings.TrimSuffix(baseURL, "/responses")

	// Determine endpoint based on request path
	if isCompactResponsesPath(requestPath) {
		return baseURL + "/responses/compact"
	}
	return baseURL + "/responses"
}
