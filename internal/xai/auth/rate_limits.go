package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"clisimplehub/internal/executor"
	xaiShared "clisimplehub/internal/xai/shared"
)

const rateLimitsURL = "https://grok.com/rest/rate-limits"

// modelName 与 grok.com/rest/rate-limits 对齐（bootstrap 探测用）。
var bootstrapModelNames = []string{
	"auto",
	"fast",
	"expert",
	"heavy",
	"grok-420-computer-use-sa",
}

// RateLimitWindow 上游 rate-limits 单 mode 窗口。
type RateLimitWindow struct {
	ModelName     string `json:"modelName,omitempty"`
	Remaining     int    `json:"remaining"`
	Total         int    `json:"total"`
	WindowSeconds int    `json:"windowSeconds"`
}

// RateLimitFetchOptions 控制 rate-limits 请求头行为。
type RateLimitFetchOptions struct {
	// DynamicStatsig 对应 xai.json config.dynamicStatsig；默认 true。
	DynamicStatsig bool
}

// FetchRateLimit 对单个 modelName 调用 rate-limits。
// 认证使用 sso Cookie（与浏览器 grok.com 一致），非 OAuth Bearer。
func FetchRateLimit(ctx context.Context, sso, proxyURL, modelName string, opts RateLimitFetchOptions) (*RateLimitWindow, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sso = normalizeSSO(sso)
	if sso == "" {
		return nil, fmt.Errorf("sso cookie is required for rate-limits")
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return nil, fmt.Errorf("modelName is required")
	}

	payload, err := json.Marshal(map[string]string{"modelName": modelName})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rateLimitsURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", "https://grok.com")
	req.Header.Set("Referer", "https://grok.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	// 对齐 grok2api reverse headers：x-statsig-id + x-xai-request-id
	req.Header.Set(HeaderStatsigID, GenerateStatsigID(opts.DynamicStatsig))
	req.Header.Set(HeaderXAIRequestID, uuid.NewString())
	req.Header.Set("Cookie", buildSSOCookie(sso))

	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 20*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("rate-limits HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var raw struct {
		WindowSizeSeconds *int `json:"windowSizeSeconds"`
		RemainingQueries  *int `json:"remainingQueries"`
		TotalQueries      *int `json:"totalQueries"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse rate-limits: %w", err)
	}
	if raw.RemainingQueries == nil {
		return nil, fmt.Errorf("rate-limits missing remainingQueries")
	}
	remaining := *raw.RemainingQueries
	total := remaining
	if raw.TotalQueries != nil {
		total = *raw.TotalQueries
	}
	windowSecs := 7200
	if raw.WindowSizeSeconds != nil && *raw.WindowSizeSeconds > 0 {
		windowSecs = *raw.WindowSizeSeconds
	}
	return &RateLimitWindow{
		ModelName:     modelName,
		Remaining:     remaining,
		Total:         total,
		WindowSeconds: windowSecs,
	}, nil
}

// FetchAllRateLimits 并发拉取 bootstrap 所需 mode。
// 返回成功的 mode 映射；若全部失败则 error。
func FetchAllRateLimits(ctx context.Context, sso, proxyURL string, opts RateLimitFetchOptions) (map[string]*RateLimitWindow, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sso = normalizeSSO(sso)
	if sso == "" {
		return nil, fmt.Errorf("sso cookie is required for rate-limits")
	}

	type result struct {
		name string
		win  *RateLimitWindow
		err  error
	}
	ch := make(chan result, len(bootstrapModelNames))
	var wg sync.WaitGroup
	for _, name := range bootstrapModelNames {
		wg.Add(1)
		go func(modelName string) {
			defer wg.Done()
			win, err := FetchRateLimit(ctx, sso, proxyURL, modelName, opts)
			ch <- result{name: modelName, win: win, err: err}
		}(name)
	}
	wg.Wait()
	close(ch)

	out := make(map[string]*RateLimitWindow, len(bootstrapModelNames))
	var firstErr error
	for r := range ch {
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		if r.win != nil {
			out[r.name] = r.win
		}
	}
	if len(out) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, fmt.Errorf("rate-limits returned no windows")
	}
	return out, nil
}

// InferPoolFromWindows 根据 live total 推断 basic / super / heavy。
// 主信号 auto.total；fallback expert / grok-420 totals。
func InferPoolFromWindows(windows map[string]*RateLimitWindow) string {
	if windows == nil {
		return ""
	}
	if auto := windows["auto"]; auto != nil {
		switch auto.Total {
		case 20:
			return xaiShared.PoolBasic
		case 50:
			return xaiShared.PoolSuper
		case 150:
			return xaiShared.PoolHeavy
		}
		// auto 存在但 total 非常规：继续用 expert/grok 兜底
		if auto.Total > 0 && auto.Total != 20 {
			// 非 basic 指纹时优先用映射
		}
	}
	for _, key := range []string{"expert", "grok-420-computer-use-sa"} {
		win := windows[key]
		if win == nil {
			continue
		}
		switch win.Total {
		case 150:
			return xaiShared.PoolHeavy
		case 50:
			return xaiShared.PoolSuper
		}
	}
	// 仅有 fast、且 total 接近 free 档，视为 basic
	if auto := windows["auto"]; auto == nil {
		if fast := windows["fast"]; fast != nil && fast.Total > 0 && fast.Total <= 40 {
			return xaiShared.PoolBasic
		}
	}
	return ""
}

// ApplyRateLimitsToAccount 将 rate-limits 结果写入账号 pool + quota。
func ApplyRateLimitsToAccount(account *xaiShared.XaiAccount, windows map[string]*RateLimitWindow) {
	if account == nil || len(windows) == 0 {
		return
	}
	now := time.Now().UTC()
	nowMs := now.UnixMilli()
	quota := &xaiShared.XaiQuota{}
	if account.Quota != nil {
		// 保留未刷新 mode
		cp := *account.Quota
		quota = &cp
	}
	setWin := func(dst **xaiShared.XaiQuotaWindow, src *RateLimitWindow) {
		if src == nil {
			return
		}
		resetAt := nowMs + int64(src.WindowSeconds)*1000
		*dst = &xaiShared.XaiQuotaWindow{
			Remaining:     src.Remaining,
			Total:         src.Total,
			WindowSeconds: src.WindowSeconds,
			ResetAt:       resetAt,
			SyncedAt:      nowMs,
		}
	}
	setWin(&quota.Auto, windows["auto"])
	setWin(&quota.Fast, windows["fast"])
	setWin(&quota.Expert, windows["expert"])
	setWin(&quota.Heavy, windows["heavy"])
	setWin(&quota.Grok43, windows["grok-420-computer-use-sa"])

	if inferred := InferPoolFromWindows(windows); inferred != "" {
		account.Pool = inferred
	} else if strings.TrimSpace(account.Pool) == "" {
		account.Pool = xaiShared.PoolBasic
	}
	// basic 不展示 unsupported 的 auto/expert 等
	if account.Pool == xaiShared.PoolBasic {
		quota.Auto = nil
		quota.Expert = nil
		quota.Heavy = nil
		quota.Grok43 = nil
	} else if account.Pool == xaiShared.PoolSuper {
		quota.Heavy = nil
	}
	account.Quota = quota
	account.LastQuotaSync = now
}

func normalizeSSO(sso string) string {
	sso = strings.TrimSpace(sso)
	if strings.HasPrefix(strings.ToLower(sso), "sso=") {
		sso = strings.TrimSpace(sso[4:])
	}
	return sso
}

func buildSSOCookie(sso string) string {
	sso = normalizeSSO(sso)
	return "sso=" + sso + "; sso-rw=" + sso
}
