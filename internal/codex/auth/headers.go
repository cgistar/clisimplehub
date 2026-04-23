package auth

import (
	"net/http"
	"strings"

	codexShared "clisimplehub/internal/codex/shared"
)

// HeaderBuilder provides a reusable way to build common Codex API request headers
type HeaderBuilder struct {
	accessToken string
	accountID   string
	userAgent   string
}

// NewHeaderBuilder creates a new HeaderBuilder with the given access token and account ID
func NewHeaderBuilder(accessToken, accountID string) *HeaderBuilder {
	return &HeaderBuilder{
		accessToken: strings.TrimSpace(accessToken),
		accountID:   strings.TrimSpace(accountID),
		userAgent:   codexShared.DefaultCodexUserAgent,
	}
}

// WithUserAgent sets a custom User-Agent header
func (b *HeaderBuilder) WithUserAgent(userAgent string) *HeaderBuilder {
	if ua := strings.TrimSpace(userAgent); ua != "" {
		b.userAgent = ua
	}
	return b
}

// ApplyTo applies the headers to an http.Request
func (b *HeaderBuilder) ApplyTo(req *http.Request) {
	if req == nil {
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", b.userAgent)

	if b.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+b.accessToken)
	}

	if b.accountID != "" {
		req.Header.Set("Chatgpt-Account-Id", b.accountID)
	}
}

// BuildHeaders returns a map of headers for manual construction
func (b *HeaderBuilder) BuildHeaders() map[string]string {
	headers := map[string]string{
		"Content-Type": "application/json",
		"User-Agent":   b.userAgent,
	}

	if b.accessToken != "" {
		headers["Authorization"] = "Bearer " + b.accessToken
	}

	if b.accountID != "" {
		headers["Chatgpt-Account-Id"] = b.accountID
	}

	return headers
}
