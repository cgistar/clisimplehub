package executor

import (
	"bytes"
	"net/http"
	"strings"
)

func isLikelyHTMLResponse(statusCode int, contentType string, body []byte) bool {
	if statusCode != http.StatusOK {
		return false
	}

	ct := strings.ToLower(strings.TrimSpace(contentType))
	if strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml") {
		return true
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false
	}
	if len(trimmed) > 4096 {
		trimmed = trimmed[:4096]
	}

	lower := bytes.ToLower(trimmed)
	if bytes.HasPrefix(lower, []byte("<!doctype html")) || bytes.HasPrefix(lower, []byte("<html")) {
		return true
	}
	return bytes.Contains(lower, []byte("<html"))
}
