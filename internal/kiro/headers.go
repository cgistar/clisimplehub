package kiro

import (
	"fmt"
	"net/http"
	"strings"

	kiroShared "clisimplehub/internal/kiro/shared"

	"github.com/google/uuid"
)

// AuthApplier handles authentication for Kiro transformer requests.
type AuthApplier struct {
	source KiroAuthSource
}

// KiroAuthSource provides the dynamic values needed to build Kiro request headers.
// It is implemented by Kiro transformers that can mint/refresh access tokens.
type KiroAuthSource interface {
	GetAccessToken() (string, error)
	MachineID() string
	KiroUserAgentBase() string
	KiroVersion() string
}

func NewAuthApplier(source KiroAuthSource) *AuthApplier {
	return &AuthApplier{source: source}
}

// applyBaseHeaders sets the 6 common headers shared by all Kiro AWS requests.
func applyBaseHeaders(req *http.Request, userAgent, xAmzUserAgent string, maxAttempts int) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection", "close")
	req.Header.Set("amz-sdk-invocation-id", uuid.NewString())
	req.Header.Set("amz-sdk-request", fmt.Sprintf("attempt=1; max=%d", maxAttempts))
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("x-amz-user-agent", xAmzUserAgent)
}

// Apply applies Kiro API headers (Profile 1) to the request.
func (k *AuthApplier) Apply(req *http.Request) error {
	if req == nil {
		return fmt.Errorf("nil request")
	}

	if k == nil || k.source == nil {
		return fmt.Errorf("kiro auth source not initialized")
	}
	// Get access token from transformer (auto-refreshes if needed).
	token, err := k.source.GetAccessToken()
	if err != nil {
		return fmt.Errorf("failed to get kiro access token: %w", err)
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("missing kiro access token")
	}
	if strings.ContainsAny(token, "\r\n") {
		return fmt.Errorf("invalid kiro access token")
	}

	// Build a clean header set for Kiro requests.
	// The upstream Kiro/CodeWhisperer API is sensitive to unrelated client/vendor headers,
	// so we intentionally do not forward any pre-existing headers (including endpoint custom headers).
	req.Header = make(http.Header)
	if strings.TrimSpace(req.Host) == "" {
		req.Host = req.URL.Host
	}

	fp := k.source.MachineID()
	fp = kiroShared.TruncateFingerprint(fp, 64)

	userAgentBase := k.source.KiroUserAgentBase()
	kiroVersion := k.source.KiroVersion()

	userAgent := strings.TrimSpace(userAgentBase) + " " + strings.TrimSpace(kiroVersion) + "-" + fp
	xAmzUA := kiroShared.KiroXAmzUserAgentBase(userAgentBase) + " " + strings.TrimSpace(kiroVersion) + "-" + fp

	req.Header.Set("Authorization", "Bearer "+token)
	applyBaseHeaders(req, userAgent, xAmzUA, 1)
	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("x-amzn-codewhisperer-optout", "true")
	req.Header.Set("x-amzn-kiro-agent-mode", "vibe")

	return nil
}

// ApplyIdcOidc applies IDC OIDC headers (Profile 2) to the request.
func ApplyIdcOidc(req *http.Request) {
	ua := kiroShared.IDCOidcUserAgent
	xAmzUA := kiroShared.KiroXAmzUserAgentBase(ua) + " KiroIDE"
	applyBaseHeaders(req, ua, xAmzUA, 4)
}
