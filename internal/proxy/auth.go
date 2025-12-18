package proxy

import (
	"net/http"
	"strings"
)

func isAuthorized(r *http.Request, requiredKey string) bool {
	if r == nil {
		return false
	}
	requiredKey = strings.TrimSpace(requiredKey)
	if requiredKey == "" || requiredKey == "-" {
		return true
	}

	if token := bearerToken(r.Header.Get("Authorization")); token != "" && token == requiredKey {
		return true
	}
	if key := strings.TrimSpace(r.Header.Get("x-api-key")); key != "" && key == requiredKey {
		return true
	}
	return false
}

func bearerToken(authorizationValue string) string {
	authorizationValue = strings.TrimSpace(authorizationValue)
	if authorizationValue == "" {
		return ""
	}
	parts := strings.Fields(authorizationValue)
	if len(parts) < 2 {
		return ""
	}
	if !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}
