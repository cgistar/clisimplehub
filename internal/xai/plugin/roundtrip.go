package xaiplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	xai "clisimplehub/internal/xai"
	xaiBackend "clisimplehub/internal/xai/backend"
	xaiShared "clisimplehub/internal/xai/shared"
	"clisimplehub/internal/executor"
	"clisimplehub/internal/plugin"
)

const (
	maxRetryAccounts       = 5
	xaiNetworkRetryAttempts = 2
	xaiNetworkRetryDelay    = 2 * time.Second
)

func (s *XaiService) RoundTrip(ctx context.Context, req *executor.UpstreamRequest) *executor.UpstreamRoundTripResult {
	if req == nil {
		return roundTripError(http.StatusInternalServerError, "nil upstream request", fmt.Errorf("nil upstream request"))
	}
	pool := xai.GetPool()
	if pool == nil {
		return roundTripError(http.StatusServiceUnavailable, "xai pool not initialized", fmt.Errorf("xai pool not initialized"))
	}
	snap := pool.Snapshot()
	if snap == nil {
		snap = &xaiShared.XaiMultiConfig{Config: xaiShared.DefaultXaiConfig()}
	}

	mode := pool.Mode()
	first := pool.Select()
	if first == nil {
		status, body := buildNoAccountsError(mode)
		return &executor.UpstreamRoundTripResult{
			StatusCode: status,
			Body:       body,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Error:      fmt.Errorf("no available xai accounts in %s mode", mode),
		}
	}

	excluded := make(map[string]bool)
	var lastErr error
	for attempt := 0; attempt < maxRetryAccounts; attempt++ {
		select {
		case <-ctx.Done():
			return roundTripError(499, "request cancelled", ctx.Err())
		default:
		}

		var account *xaiShared.XaiAccount
		if attempt == 0 {
			account = first
		} else {
			if mode == xaiShared.RotationFixed {
				break
			}
			account = pool.SelectExcluding(excluded)
			if account == nil {
				break
			}
		}
		result, retryable := s.roundTripWithAccount(ctx, pool, snap, account, req)
		if result == nil {
			return roundTripError(http.StatusBadGateway, "empty upstream result", fmt.Errorf("empty upstream result"))
		}
		if result.StatusCode >= 200 && result.StatusCode < 300 && result.Error == nil {
			pool.ReportSuccess(account.ID)
			return result
		}
		if !retryable {
			return result
		}
		excluded[strings.TrimSpace(account.ID)] = true
		lastErr = result.Error
	}

	status, body := buildAllFailedError(lastErr)
	return &executor.UpstreamRoundTripResult{
		StatusCode: status,
		Body:       body,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Error:      lastErr,
	}
}

func (s *XaiService) roundTripWithAccount(
	ctx context.Context,
	pool *xai.XaiAccountPool,
	config *xaiShared.XaiMultiConfig,
	account *xaiShared.XaiAccount,
	req *executor.UpstreamRequest,
) (*executor.UpstreamRoundTripResult, bool) {
	proxyURL := resolveAccountProxy(pool, account)
	token, err := ensureAccessToken(ctx, pool, account, proxyURL)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "invalid_grant") {
			pool.MarkFailed(account.ID, xaiShared.XaiStatusBanned, 0, "auth_failed")
		}
		return &executor.UpstreamRoundTripResult{
			StatusCode: http.StatusUnauthorized,
			Body:       mustJSON(map[string]any{"error": map[string]any{"type": "authentication_error", "message": err.Error()}}),
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Error:      err,
		}, true
	}

	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 0)
	// 与 codex 一致：优先 TargetPath（chat→responses 转换后的上游路径），
	// 避免 OriginalPath=/xai/v1/chat/completions 把 Responses body 打到 /chat/completions
	// 导致上游 "Messages cannot be empty"。
	path := strings.TrimSpace(req.TargetPath)
	if path == "" {
		path = strings.TrimSpace(req.OriginalPath)
	}
	// Claude 源启用 reasoning replay；session key 优先用 plan metadata（转换前解析）
	enableReplay := false
	replaySession := ""
	if req.TransformContext != nil && req.TransformContext.Metadata != nil {
		if v, ok := req.TransformContext.Metadata["enable_xai_replay"].(bool); ok && v {
			enableReplay = true
		}
		if src, _ := req.TransformContext.Metadata["source_type"].(string); strings.EqualFold(src, "claude") {
			enableReplay = true
		}
		if k, _ := req.TransformContext.Metadata["xai_replay_session"].(string); strings.TrimSpace(k) != "" {
			replaySession = strings.TrimSpace(k)
		}
	}
	if !enableReplay {
		pathLower := strings.ToLower(req.OriginalPath)
		enableReplay = strings.Contains(pathLower, "/messages")
	}
	// body 可能已是 Responses；若仍有 Claude metadata 则从 body 解析
	if enableReplay && replaySession == "" {
		replaySession = xaiBackend.ResolveReplaySessionKeyWithClaude(req.Body, req.Body, req.Headers, "")
	}

	// 若 plan 已 prepare 过且 body 含 input，仍让 backend 再 prepare 以应用 stream 强制与 headers；
	// 但 replay session 通过 header 旁路注入（x-xai-replay-session）避免丢失。
	headers := req.Headers
	if headers == nil {
		headers = http.Header{}
	} else {
		headers = headers.Clone()
	}
	if replaySession != "" {
		headers.Set("x-xai-replay-session", replaySession)
	}

	backendReq := xaiBackend.Request{
		Method:       req.Method,
		Path:         path,
		RawQuery:     req.RawQuery,
		Body:         req.Body,
		Headers:      headers,
		IsStreaming:  req.IsStreaming,
		Model:        req.RequestModel,
		Config:       config,
		Account:      account,
		AccessToken:  token,
		ProxyURL:     proxyURL,
		Client:       client,
		EnableReplay: enableReplay,
		Attempts:     xaiNetworkRetryAttempts,
		RetryDelay:   xaiNetworkRetryDelay,
	}

	backendResult, execErr := xaiBackend.Execute(ctx, backendReq)
	if backendResult == nil {
		backendResult = &xaiBackend.Result{}
	}
	if execErr != nil && backendResult.StatusCode == 0 {
		return &executor.UpstreamRoundTripResult{
			StatusCode:    http.StatusBadGateway,
			Body:          mustJSON(map[string]any{"error": map[string]any{"type": "transport_error", "message": execErr.Error()}}),
			Headers:       http.Header{"Content-Type": []string{"application/json"}},
			TargetURL:     backendResult.TargetURL,
			TargetHeaders: backendResult.TargetHeaders,
			RequestBody:   backendResult.RequestBody,
			Error:         execErr,
		}, true
	}

	// 官方 API 路径（compact / images / videos）上 free OAuth 常 402/403 spending-limit，
	// 但同一账号在 cli-chat-proxy 的 free chat 仍可用。勿因此把账号永久 exhausted。
	paidOnlyPath := xaiBackend.IsCompactPath(path) || xaiBackend.IsMediaPath(path)

	switch backendResult.StatusCode {
	case http.StatusUnauthorized:
		// 强制刷新后重试一次
		if strings.TrimSpace(account.RefreshToken) != "" {
			svc := mustRefresh(ctx, pool, account, proxyURL)
			if svc != "" {
				backendReq.AccessToken = svc
				retryResult, retryErr := xaiBackend.Execute(ctx, backendReq)
				if retryErr == nil && retryResult != nil && retryResult.StatusCode >= 200 && retryResult.StatusCode < 300 {
					return toUpstreamResult(retryResult), false
				}
			}
		}
		return toUpstreamResult(backendResult), true
	case http.StatusForbidden:
		// spending-limit / personal-team-blocked 属于额度问题
		if isQuotaLikeBody(backendResult.Body) {
			if paidOnlyPath {
				return toUpstreamResult(backendResult), false
			}
			pool.MarkFailed(account.ID, xaiShared.XaiStatusExhausted, 0, "quota_or_subscription")
			return toUpstreamResult(backendResult), true
		}
		pool.MarkFailed(account.ID, xaiShared.XaiStatusBanned, 24*time.Hour, "forbidden")
		return toUpstreamResult(backendResult), true
	case http.StatusPaymentRequired:
		if paidOnlyPath {
			return toUpstreamResult(backendResult), false
		}
		pool.MarkFailed(account.ID, xaiShared.XaiStatusExhausted, 0, "quota_exhausted")
		return toUpstreamResult(backendResult), true
	case http.StatusTooManyRequests:
		cooldown := parseRetryAfter(backendResult.Headers)
		if cooldown <= 0 {
			cooldown = 60 * time.Second
		}
		// free-usage-exhausted：cli-chat-proxy 声明 24h 滚动窗口
		if isFreeUsageExhaustedBody(backendResult.Body) {
			cooldown = 24 * time.Hour
		}
		pool.MarkFailed(account.ID, xaiShared.XaiStatusValid, cooldown, "rate_limit")
		return toUpstreamResult(backendResult), true
	}

	if backendResult.StatusCode >= 500 {
		return toUpstreamResult(backendResult), true
	}
	if backendResult.StatusCode >= 400 {
		return toUpstreamResult(backendResult), false
	}
	return toUpstreamResult(backendResult), false
}

func mustRefresh(ctx context.Context, pool *xai.XaiAccountPool, account *xaiShared.XaiAccount, proxyURL string) string {
	// 清空 access 强制刷新
	account.AccessToken = ""
	account.ExpiresAt = time.Time{}
	token, err := ensureAccessToken(ctx, pool, account, proxyURL)
	if err != nil {
		return ""
	}
	return token
}

func resolveAccountProxy(pool *xai.XaiAccountPool, account *xaiShared.XaiAccount) string {
	proxyURL := plugin.GetAppProxyURL()
	if proxyURL == "" && account != nil {
		proxyURL = strings.TrimSpace(account.ProxyUrl)
	}
	if proxyURL == "" && pool != nil {
		proxyURL = pool.ProxyURL()
	}
	return proxyURL
}

func toUpstreamResult(r *xaiBackend.Result) *executor.UpstreamRoundTripResult {
	if r == nil {
		return roundTripError(http.StatusBadGateway, "empty result", fmt.Errorf("empty result"))
	}
	return &executor.UpstreamRoundTripResult{
		StatusCode:    r.StatusCode,
		Headers:       r.Headers,
		Body:          r.Body,
		Stream:        r.Stream,
		TargetURL:     r.TargetURL,
		TargetHeaders: r.TargetHeaders,
		RequestBody:   r.RequestBody,
		Error:         r.Error,
	}
}

func roundTripError(status int, message string, err error) *executor.UpstreamRoundTripResult {
	return &executor.UpstreamRoundTripResult{
		StatusCode: status,
		Body:       mustJSON(map[string]any{"error": map[string]any{"type": "internal_error", "message": message}}),
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Error:      err,
	}
}

func buildNoAccountsError(mode string) (int, []byte) {
	msg := "No available xAI accounts"
	switch mode {
	case xaiShared.RotationFixed:
		msg = "No available xAI accounts in fixed mode"
	case xaiShared.RotationFailover:
		msg = "No available xAI accounts in failover mode"
	case xaiShared.RotationLoadBalance:
		msg = "No available xAI accounts in load balance mode"
	}
	return http.StatusServiceUnavailable, mustJSON(map[string]any{
		"error": map[string]any{
			"type":    "no_available_accounts",
			"message": msg,
			"code":    "xai_account_unavailable",
			"mode":    mode,
		},
	})
}

func buildAllFailedError(lastErr error) (int, []byte) {
	message := "All xAI accounts failed"
	if lastErr != nil {
		message = lastErr.Error()
	}
	return http.StatusBadGateway, mustJSON(map[string]any{
		"error": map[string]any{
			"type":    "all_accounts_failed",
			"message": message,
			"code":    "xai_all_failed",
		},
	})
}

func parseRetryAfter(headers http.Header) time.Duration {
	if headers == nil {
		return 0
	}
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	if secs, err := time.ParseDuration(raw + "s"); err == nil {
		return secs
	}
	return 0
}

func isQuotaLikeBody(body []byte) bool {
	s := strings.ToLower(string(body))
	return strings.Contains(s, "spending-limit") ||
		strings.Contains(s, "run out of credits") ||
		strings.Contains(s, "need a grok subscription") ||
		strings.Contains(s, "personal-team-blocked")
}

func isFreeUsageExhaustedBody(body []byte) bool {
	s := strings.ToLower(string(body))
	return strings.Contains(s, "free-usage-exhausted") ||
		strings.Contains(s, "included free usage")
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
