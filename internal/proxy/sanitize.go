package proxy

import (
	"net/http"
	"strings"
)

func sanitizeHeadersForLog(headers http.Header) map[string]string {
	if len(headers) == 0 {
		return map[string]string{}
	}

	sanitized := make(map[string]string, len(headers))
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		sanitized[key] = sanitizeHeaderValue(key, values[0])
	}
	return sanitized
}

func sanitizeHeaderValue(key string, value string) string {
	if value == "" {
		return ""
	}

	if strings.EqualFold(key, "Authorization") || strings.EqualFold(key, "Proxy-Authorization") {
		return maskAuthorizationValue(value)
	}
	if strings.EqualFold(key, "x-api-key") {
		return maskSecret(value)
	}
	if strings.EqualFold(key, "Cookie") {
		return "[redacted]"
	}
	return value
}

func maskAuthorizationValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	parts := strings.Fields(trimmed)
	if len(parts) >= 2 && strings.EqualFold(parts[0], "Bearer") {
		return "Bearer " + maskSecret(parts[1])
	}
	return maskSecret(trimmed)
}

func formatUpstreamAuthForLog(endpoint *Endpoint) string {
	if endpoint == nil {
		return ""
	}

	key := strings.TrimSpace(endpoint.APIKey)
	if key == "" {
		return ""
	}

	switch InterfaceType(endpoint.InterfaceType) {
	case InterfaceTypeGemini:
		return "key=" + maskSecret(key)
	case InterfaceTypeCodex, InterfaceTypeChat:
		return "Authorization: Bearer " + maskSecret(key)
	default:
		// Unknown interface type: show key only (masked) to avoid implying header format.
		return "key=" + maskSecret(key)
	}
}

func maskSecret(secret string) string {
	s := strings.TrimSpace(secret)
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "****"
	}

	prefixLen := 8
	suffixLen := 4
	if len(s) <= prefixLen+suffixLen {
		return "****"
	}

	return s[:prefixLen] + "..." + s[len(s)-suffixLen:]
}
