package xaiplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"clisimplehub/internal/executor"
	"clisimplehub/internal/plugin"
	xai "clisimplehub/internal/xai"
	xaiBackend "clisimplehub/internal/xai/backend"
	xaiShared "clisimplehub/internal/xai/shared"
)

const (
	maxRetryAccounts        = 5
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
	var first *xaiShared.XaiAccount
	if req.TransformContext != nil && req.TransformContext.Metadata != nil {
		if id, _ := req.TransformContext.Metadata["xai_preferred_account_id"].(string); id != "" {
			first = pool.SelectByID(id)
		}
	}
	if first == nil {
		first = pool.Select()
	}
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
	var lastResult *executor.UpstreamRoundTripResult
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
		if result.Headers == nil {
			result.Headers = http.Header{}
		}
		result.Headers.Set("X-Clisimplehub-XAI-Account-ID", strings.TrimSpace(account.ID))
		if result.StatusCode >= 200 && result.StatusCode < 300 && result.Error == nil {
			pool.ReportSuccess(account.ID)
			return result
		}
		if !retryable {
			return result
		}
		excluded[strings.TrimSpace(account.ID)] = true
		lastErr = result.Error
		lastResult = result
	}
	// 所有账号均返回了 HTTP 响应时，保留最后一个有意义的上游状态和标准错误体。
	// 只有没有任何 HTTP 结果（纯 token/transport failure）才合成 502。
	if lastResult != nil && lastResult.StatusCode >= 400 {
		return lastResult
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
	// client_mode / replay / execution_session 优先 plan metadata
	clientMode := xaiBackend.ClientModeAuto
	enableReplay := false
	replaySession := ""
	executionSessionID := ""
	sourceType := "openai-response"
	if req.TransformContext != nil && req.TransformContext.Metadata != nil {
		if m, _ := req.TransformContext.Metadata["client_mode"].(string); strings.TrimSpace(m) != "" {
			clientMode = xaiBackend.ClientMode(strings.TrimSpace(m))
		}
		if v, ok := req.TransformContext.Metadata["enable_xai_replay"].(bool); ok && v {
			enableReplay = true
		}
		if src, _ := req.TransformContext.Metadata["source_type"].(string); strings.TrimSpace(src) != "" {
			sourceType = strings.TrimSpace(src)
			if strings.EqualFold(sourceType, "claude") {
				enableReplay = true
			}
		}
		if k, _ := req.TransformContext.Metadata["xai_replay_session"].(string); strings.TrimSpace(k) != "" {
			replaySession = strings.TrimSpace(k)
		}
		executionSessionID = xaiBackend.ExecutionSessionIDFromMeta(req.TransformContext.Metadata)
	}
	// 无 plan 时：按 UA + path 解析（直接 /xai 或裸 RoundTrip）
	if clientMode == xaiBackend.ClientModeAuto {
		needsFmt := false
		if req.Transformer != nil {
			needsFmt = true
		}
		clientMode = xaiBackend.ResolveClientMode(
			xaiBackend.ClientModeAuto,
			headerUA(req.Headers),
			path,
			req.Body,
			needsFmt,
		)
	}
	if !enableReplay {
		pathLower := strings.ToLower(req.OriginalPath)
		enableReplay = strings.Contains(pathLower, "/messages")
	}
	if enableReplay && replaySession == "" {
		replaySession = xaiBackend.ResolveReplaySessionKeyWithClaude(req.Body, req.Body, req.Headers, "")
	}

	headers := req.Headers
	if headers == nil {
		headers = http.Header{}
	} else {
		headers = headers.Clone()
	}
	// 元数据中的 execution_session_id 注入请求头，供 ResolveUpstreamSessionID / WS 绑定
	if executionSessionID != "" && xaiBackend.ExecutionSessionIDFromHeaders(headers) == "" {
		headers.Set(xaiBackend.HeaderExecutionSessionID, executionSessionID)
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
		SourceType:   sourceType,
		Config:       config,
		Account:      account,
		AccessToken:  token,
		ProxyURL:     proxyURL,
		Client:       client,
		ClientMode:   clientMode,
		EnableReplay: enableReplay,
		Attempts:     xaiNetworkRetryAttempts,
		RetryDelay:   xaiNetworkRetryDelay,
	}

	backendResult, execErr := xaiBackend.Execute(ctx, backendReq)
	if backendResult == nil {
		backendResult = &xaiBackend.Result{}
	}
	if execErr != nil && backendResult.StatusCode == 0 {
		if xaiBackend.IsInvalidRequestError(execErr) {
			return &executor.UpstreamRoundTripResult{
				StatusCode:  http.StatusBadRequest,
				Body:        mustJSON(map[string]any{"error": map[string]any{"type": "invalid_request_error", "message": execErr.Error()}}),
				Headers:     http.Header{"Content-Type": []string{"application/json"}},
				RequestBody: backendResult.RequestBody,
				Error:       execErr,
			}, false
		}
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

	// 官方 API 媒体路径上 free OAuth 常 402/403 spending-limit，
	// 但同一账号在 cli-chat-proxy 的 free chat 仍可用。勿因此把账号永久 exhausted。
	// compact 走 chat base，额度错误应正常 exhausted。
	// 仍应 retryable=true，让 failover 换其它账号继续。
	paidOnlyPath := xaiBackend.IsMediaPath(path)

	refreshed401 := false
classifyResponse:
	switch backendResult.StatusCode {
	case http.StatusUnauthorized:
		// 强制刷新后重试一次
		if !refreshed401 && strings.TrimSpace(account.RefreshToken) != "" {
			refreshed401 = true
			svc := mustRefresh(ctx, pool, account, proxyURL)
			if svc != "" {
				backendReq.AccessToken = svc
				retryResult, retryErr := xaiBackend.Execute(ctx, backendReq)
				if retryResult != nil {
					if retryErr == nil && retryResult.StatusCode >= 200 && retryResult.StatusCode < 300 {
						return toUpstreamResult(retryResult), false
					}
					// 刷新后的响应才是该账号最终结果，不能退回过期 token 的首次 401。
					backendResult = retryResult
					goto classifyResponse
				}
			}
		}
		return toUpstreamResult(backendResult), true
	case http.StatusForbidden:
		// spending-limit / personal-team-blocked 属于额度问题
		if isQuotaLikeBody(backendResult.Body) || isFreeUsageExhaustedBody(backendResult.Body) {
			if !paidOnlyPath {
				pool.MarkFailed(account.ID, xaiShared.XaiStatusExhausted, 0, "quota_or_subscription")
			}
			return toUpstreamResult(backendResult), true
		}
		pool.MarkFailed(account.ID, xaiShared.XaiStatusBanned, 24*time.Hour, "forbidden")
		return toUpstreamResult(backendResult), true
	case http.StatusPaymentRequired:
		if !paidOnlyPath {
			pool.MarkFailed(account.ID, xaiShared.XaiStatusExhausted, 0, "quota_exhausted")
		}
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
		pool.CooldownMainAccount(account.ID, cooldown, "rate_limit")
		return toUpstreamResult(backendResult), true
	}

	if backendResult.StatusCode >= 500 {
		return toUpstreamResult(backendResult), true
	}
	if backendResult.StatusCode >= 400 {
		// 其它 4xx 若 body 明确是额度用尽，同样换号继续
		if isQuotaLikeBody(backendResult.Body) || isFreeUsageExhaustedBody(backendResult.Body) {
			if !paidOnlyPath {
				pool.MarkFailed(account.ID, xaiShared.XaiStatusExhausted, 0, "quota_exhausted")
			}
			return toUpstreamResult(backendResult), true
		}
		return toUpstreamResult(backendResult), false
	}
	return toUpstreamResult(backendResult), false
}

func mustRefresh(ctx context.Context, pool *xai.XaiAccountPool, account *xaiShared.XaiAccount, proxyURL string) string {
	token, err := refreshAccessToken(ctx, pool, account, proxyURL)
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
	if r.Headers == nil {
		r.Headers = http.Header{}
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
	if when, err := http.ParseTime(raw); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay
		}
	}
	return 0
}

func isQuotaLikeBody(body []byte) bool {
	s := strings.ToLower(string(body))
	return strings.Contains(s, "spending-limit") ||
		strings.Contains(s, "run out of credits") ||
		strings.Contains(s, "need a grok subscription") ||
		strings.Contains(s, "personal-team-blocked") ||
		strings.Contains(s, "quota") && (strings.Contains(s, "exceed") || strings.Contains(s, "exhaust") || strings.Contains(s, "limit")) ||
		strings.Contains(s, "usage limit") ||
		strings.Contains(s, "out of credits") ||
		strings.Contains(s, "insufficient") && strings.Contains(s, "credit")
}

func isFreeUsageExhaustedBody(body []byte) bool {
	s := strings.ToLower(string(body))
	return strings.Contains(s, "free-usage-exhausted") ||
		strings.Contains(s, "included free usage") ||
		strings.Contains(s, "free usage") && strings.Contains(s, "exhaust")
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func headerUA(h http.Header) string {
	if h == nil {
		return ""
	}
	return strings.TrimSpace(h.Get("User-Agent"))
}
