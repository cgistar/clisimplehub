package auth

import (
	"net/http"
	"strings"

	codexShared "clisimplehub/internal/codex/shared"

	"github.com/google/uuid"
)

// HeaderBuilder provides a reusable way to build common Codex API request headers
type HeaderBuilder struct {
	accessToken string
	accountID   string
	userAgent   string
	originator  string
	sessionID   string
	accept      string
	connection  string
}

// NewHeaderBuilder creates a new HeaderBuilder with the given access token and account ID
func NewHeaderBuilder(accessToken, accountID string) *HeaderBuilder {
	return &HeaderBuilder{
		accessToken: strings.TrimSpace(accessToken),
		accountID:   strings.TrimSpace(accountID),
		userAgent:   codexShared.DefaultCodexUserAgent,
		originator:  codexShared.DefaultCodexOriginator,
		accept:      "application/json",
		connection:  "Keep-Alive",
	}
}

// WithUserAgent sets a custom User-Agent header
func (b *HeaderBuilder) WithUserAgent(userAgent string) *HeaderBuilder {
	if ua := strings.TrimSpace(userAgent); ua != "" {
		b.userAgent = ua
	}
	return b
}

// WithOriginator sets the Originator header used by Codex OAuth requests.
func (b *HeaderBuilder) WithOriginator(originator string) *HeaderBuilder {
	if v := strings.TrimSpace(originator); v != "" {
		b.originator = v
	}
	return b
}

// WithSessionID sets a stable session_id/Session_id header value.
func (b *HeaderBuilder) WithSessionID(sessionID string) *HeaderBuilder {
	b.sessionID = strings.TrimSpace(sessionID)
	return b
}

// ApplyTo applies the headers to an http.Request
func (b *HeaderBuilder) ApplyTo(req *http.Request) {
	if req == nil {
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", b.accept)
	req.Header.Set("Connection", b.connection)
	req.Header.Set("User-Agent", b.userAgent)
	req.Header.Set("Originator", b.originator)

	sessionID := b.sessionID
	if sessionID == "" && strings.Contains(b.userAgent, "Mac OS") {
		sessionID = uuid.NewString()
	}
	if sessionID != "" {
		req.Header.Set("Session_id", sessionID)
	}

	if b.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+b.accessToken)
	}

	if b.accountID != "" {
		req.Header.Set("Chatgpt-Account-Id", b.accountID)
		req.Header.Set("ChatGPT-Account-ID", b.accountID)
	}
}

// BuildHeaders returns a map of headers for manual construction
func (b *HeaderBuilder) BuildHeaders() map[string]string {
	headers := map[string]string{
		"Content-Type": "application/json",
		"Accept":       b.accept,
		"Connection":   b.connection,
		"User-Agent":   b.userAgent,
		"Originator":   b.originator,
	}

	sessionID := b.sessionID
	if sessionID == "" && strings.Contains(b.userAgent, "Mac OS") {
		sessionID = uuid.NewString()
	}
	if sessionID != "" {
		headers["Session_id"] = sessionID
	}

	if b.accessToken != "" {
		headers["Authorization"] = "Bearer " + b.accessToken
	}

	if b.accountID != "" {
		headers["Chatgpt-Account-Id"] = b.accountID
		headers["ChatGPT-Account-ID"] = b.accountID
	}

	return headers
}
