package codexplugin

import (
	"net/http"
	"strings"
	"time"

	codexShared "clisimplehub/internal/codex/shared"

	"github.com/tidwall/gjson"
)

const codexRateLimitsEventType = "codex.rate_limits"

// extractCodexRateLimitsSnapshot 从 Codex 流事件中提取主、次限流窗口。
func extractCodexRateLimitsSnapshot(payload []byte, now time.Time) *codexShared.CodexUsageSnapshot {
	if len(payload) == 0 {
		return nil
	}

	root := gjson.ParseBytes(payload)
	eventType := strings.TrimSpace(root.Get("type").String())
	if eventType == "error" {
		return extractCodexUsageHeadersAt(codexQuotaErrorHeaders(root.Get("headers")), now)
	}
	if eventType != codexRateLimitsEventType {
		return nil
	}

	rateLimits := firstCodexRateLimitResult(root, "rate_limits", "rateLimit")
	if !rateLimits.Exists() || !rateLimits.IsObject() {
		return nil
	}

	snapshot := &codexShared.CodexUsageSnapshot{UpdatedAt: now}
	hasWindow := false
	if window, ok := parseCodexRateLimitWindow(rateLimits.Get("primary"), now); ok {
		snapshot.PrimaryUsedPercent = window.usedPercent
		snapshot.PrimaryWindowMinutes = window.windowMinutes
		snapshot.PrimaryResetAfterSeconds = window.resetAfterSeconds
		hasWindow = true
	}
	if window, ok := parseCodexRateLimitWindow(rateLimits.Get("secondary"), now); ok {
		snapshot.SecondaryUsedPercent = window.usedPercent
		snapshot.SecondaryWindowMinutes = window.windowMinutes
		snapshot.SecondaryResetAfterSeconds = window.resetAfterSeconds
		hasWindow = true
	}
	if !hasWindow {
		return nil
	}
	return snapshot
}

// codexQuotaErrorHeaders 只接收错误帧中与配额有关的标量响应头。
func codexQuotaErrorHeaders(node gjson.Result) http.Header {
	if !node.Exists() || !node.IsObject() {
		return nil
	}
	headers := make(http.Header)
	node.ForEach(func(key, value gjson.Result) bool {
		name := http.CanonicalHeaderKey(strings.TrimSpace(key.String()))
		if !isCodexQuotaHeaderName(name) {
			return true
		}
		raw := codexQuotaScalarValue(value)
		if raw != "" {
			headers.Set(name, raw)
		}
		return true
	})
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func isCodexQuotaHeaderName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "retry-after" || strings.HasPrefix(lower, "x-ratelimit-") {
		return true
	}
	if lower == "x-codex-active-limit" || lower == "x-codex-plan-type" ||
		strings.HasPrefix(lower, "x-codex-credits-") {
		return true
	}
	if !strings.HasPrefix(lower, "x-codex-") {
		return false
	}
	for _, marker := range []string{
		"-allowed",
		"-limit-reached",
		"-limit-name",
		"-used-percent",
		"-window-minutes",
		"-reset-after-seconds",
		"-reset-at",
		"-over-secondary-limit-percent",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func codexQuotaScalarValue(value gjson.Result) string {
	switch value.Type {
	case gjson.String:
		return strings.TrimSpace(value.String())
	case gjson.Number, gjson.True, gjson.False:
		return strings.TrimSpace(value.Raw)
	default:
		return ""
	}
}

type codexRateLimitWindow struct {
	usedPercent       float64
	windowMinutes     int
	resetAfterSeconds int
}

func parseCodexRateLimitWindow(window gjson.Result, now time.Time) (codexRateLimitWindow, bool) {
	if !window.Exists() || !window.IsObject() {
		return codexRateLimitWindow{}, false
	}

	usedPercent := firstCodexRateLimitResult(window, "used_percent", "usedPercent")
	windowMinutes := firstCodexRateLimitResult(window, "window_minutes", "windowMinutes")
	if !usedPercent.Exists() || !windowMinutes.Exists() ||
		usedPercent.Float() < 0 || usedPercent.Float() > 100 || windowMinutes.Int() <= 0 {
		return codexRateLimitWindow{}, false
	}

	resetAfter := firstCodexRateLimitResult(window, "reset_after_seconds", "resetAfterSeconds")
	resetAt := firstCodexRateLimitResult(window, "reset_at", "resetAt")
	resetAfterSeconds := 0
	switch {
	case resetAfter.Exists() && resetAfter.Int() >= 0:
		resetAfterSeconds = int(resetAfter.Int())
	case resetAt.Exists() && resetAt.Int() > 0:
		resetAfterSeconds = int(time.Unix(resetAt.Int(), 0).Sub(now).Seconds())
		if resetAfterSeconds < 0 {
			resetAfterSeconds = 0
		}
	default:
		return codexRateLimitWindow{}, false
	}

	return codexRateLimitWindow{
		usedPercent:       usedPercent.Float(),
		windowMinutes:     int(windowMinutes.Int()),
		resetAfterSeconds: resetAfterSeconds,
	}, true
}

func firstCodexRateLimitResult(object gjson.Result, paths ...string) gjson.Result {
	for _, path := range paths {
		value := object.Get(path)
		if value.Exists() && value.Type != gjson.Null {
			return value
		}
	}
	return gjson.Result{}
}
