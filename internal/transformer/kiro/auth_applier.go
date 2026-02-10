package kiro

import (
	"fmt"
	"net/http"
	"strings"

	kiroShared "clisimplehub/internal/transformer/kiro/shared"

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

// Apply applies Kiro-specific authentication headers to the request.
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

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	fp := k.source.MachineID()
	fp = kiroShared.TruncateFingerprint(fp, 64)

	userAgentBase := k.source.KiroUserAgentBase()
	kiroVersion := k.source.KiroVersion()

	userAgent := strings.TrimSpace(userAgentBase) + " " + strings.TrimSpace(kiroVersion) + "-" + fp
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("x-amz-user-agent", kiroShared.KiroXAmzUserAgentBase(userAgentBase)+" "+strings.TrimSpace(kiroVersion)+"-"+fp)
	req.Header.Set("x-amzn-codewhisperer-optout", "true")
	req.Header.Set("x-amzn-kiro-agent-mode", "vibe")
	req.Header.Set("amz-sdk-invocation-id", uuid.NewString())
	req.Header.Set("amz-sdk-request", "attempt=1; max=1")
	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("Connection", "close")

	return nil
}
