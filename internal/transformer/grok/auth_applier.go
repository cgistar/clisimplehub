package grok

import (
	"fmt"
	"net/http"
	"strings"

	"clisimplehub/internal/transformer/grok/shared"

	"github.com/google/uuid"
)

type GrokAuthSource interface {
	GetSsoToken() string
	GrokProxyURL() string
	GetSettings() *shared.GrokSettings
}

type AuthApplier struct {
	source GrokAuthSource
}

func NewAuthApplier(source GrokAuthSource) *AuthApplier {
	return &AuthApplier{source: source}
}

func (a *AuthApplier) Apply(req *http.Request) error {
	if req == nil {
		return fmt.Errorf("nil request")
	}
	if a == nil || a.source == nil {
		return fmt.Errorf("grok auth source not initialized")
	}

	token := strings.TrimSpace(a.source.GetSsoToken())
	if token == "" {
		return fmt.Errorf("missing grok sso token (all accounts may be invalid or exhausted, check grok.json)")
	}
	token = strings.TrimPrefix(token, "sso=")
	if strings.ContainsAny(token, "\r\n") {
		return fmt.Errorf("invalid grok sso token: contains control characters")
	}

	settings := a.source.GetSettings()
	if settings == nil {
		def := shared.DefaultSettings()
		settings = &def
	}

	cookie := "sso=" + token
	if cf := settings.GetCfClearance(); cf != "" {
		cookie += "; cf_clearance=" + cf
	}

	req.Header = make(http.Header)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Baggage", "sentry-environment=production,sentry-release=d6add6fb570b78b37fed8f2d23f70f498c081f10,sentry-public_key=b311e0f2690c81f25f4a50b78d2b87d1")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Origin", "https://grok.com")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Priority", "u=1, i")
	req.Header.Set("Referer", "https://grok.com/")
	req.Header.Set("Sec-Ch-Ua", `"Google Chrome";v="136", "Chromium";v="136", "Not(A:Brand";v="24"`)
	req.Header.Set("Sec-Ch-Ua-Arch", `"arm"`)
	req.Header.Set("Sec-Ch-Ua-Bitness", `"64"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Model", `""`)
	req.Header.Set("Sec-Ch-Ua-Platform", `"macOS"`)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("User-Agent", settings.GetUserAgent())
	req.Header.Set("x-statsig-id", GenStatsigID(settings.GetDynamicStatsig()))
	req.Header.Set("x-xai-request-id", uuid.NewString())

	return nil
}
