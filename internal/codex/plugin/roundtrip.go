package codexplugin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	codex "clisimplehub/internal/codex"
	codexBackend "clisimplehub/internal/codex/backend"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/executor"
	"clisimplehub/internal/plugin"
)

func (s *CodexService) RoundTrip(ctx context.Context, req *executor.UpstreamRequest) *executor.UpstreamRoundTripResult {
	if req == nil {
		return roundTripInternalError(fmt.Errorf("nil upstream request"))
	}

	debugLogger := executor.DebugLoggerFromContext(ctx)
	pool := codex.GetPool()
	if pool == nil {
		return roundTripInternalError(fmt.Errorf("codex pool not initialized"))
	}

	configPath := pool.ConfigPath()
	config, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil && !os.IsNotExist(err) {
		if debugLogger != nil {
			debugLogger.Log("配置加载失败: %v", err)
		}
		return roundTripInternalError(fmt.Errorf("config load failed: %v", err))
	}
	if config == nil {
		config = &codexShared.CodexMultiConfig{}
	}

	mode := pool.Mode()
	firstAccount := pool.Select()
	if firstAccount == nil {
		if debugLogger != nil {
			debugLogger.Log("%s 模式下无可用账号（账号可能已弃用或冷却中）", mode)
		}
		statusCode, errBody := buildNoAccountsError(mode)
		return &executor.UpstreamRoundTripResult{
			StatusCode: statusCode,
			Body:       errBody,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Error:      fmt.Errorf("no available codex accounts in %s mode", mode),
		}
	}

	var lastErr error
	excluded := make(map[string]bool)
	for attempt := 0; attempt < maxRetryAccounts; attempt++ {
		select {
		case <-ctx.Done():
			return roundTripCancelledError(ctx.Err())
		default:
		}

		var account *codexShared.CodexAccount
		if attempt == 0 {
			account = firstAccount
		} else {
			if mode == codexShared.RotationFixed {
				break
			}
			account = pool.SelectExcluding(excluded)
			if account == nil {
				if debugLogger != nil {
					debugLogger.Log("第 %d 次尝试：无可用账号", attempt+1)
				}
				break
			}
		}
		if debugLogger != nil {
			debugLogger.Log("尝试账号 %s (attempt %d)", maskToken(account.RefreshToken), attempt+1)
		}

		upstream, retryable := s.roundTripWithAccount(ctx, account, req, pool, config, nil)
		if upstream == nil {
			return roundTripInternalError(fmt.Errorf("unexpected nil round-trip result"))
		}
		if upstream.StatusCode == http.StatusOK {
			return upstream
		}
		if !retryable {
			return upstream
		}
		excluded[strings.TrimSpace(account.ID)] = true
		lastErr = upstream.Error
		if debugLogger != nil {
			debugLogger.Log("账号失败，准备重试: %v", lastErr)
		}
	}

	statusCode, errBody := buildAllFailedError(lastErr)
	return &executor.UpstreamRoundTripResult{
		StatusCode: statusCode,
		Body:       errBody,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Error:      lastErr,
	}
}

func (s *CodexService) roundTripWithAccount(ctx context.Context, account *codexShared.CodexAccount, req *executor.UpstreamRequest, pool *codex.CodexAccountPool, config *codexShared.CodexMultiConfig, reporter *codexUsageReporter) (ret *executor.UpstreamRoundTripResult, retryable bool) {
	if reporter == nil {
		reporter = newCodexHTTPUsageReporter(ctx, s, account, req)
	}
	defer func() {
		if ret == nil {
			reporter.PublishFailure(http.StatusInternalServerError, errors.New("codex round trip returned nil result"))
			return
		}
		reporter.ObserveHeaders(ret.Headers)
		if len(ret.RequestBody) > 0 {
			reporter.SetTranslatedRequest(ret.RequestBody)
		}
		if ret.Stream != nil && ret.Error == nil && ret.StatusCode == http.StatusOK {
			ret.Stream = reporter.TrackSSEStream(ret.Stream)
			return
		}
		if ret.Error != nil || ret.StatusCode != http.StatusOK {
			reportErr := ret.Error
			if reportErr == nil {
				reportErr = fmt.Errorf("codex upstream returned %d", ret.StatusCode)
			}
			reporter.PublishFailure(ret.StatusCode, reportErr)
			return
		}
		reporter.PublishSuccess(ret.Body)
	}()

	debugLogger := executor.DebugLoggerFromContext(ctx)
	configPath := pool.ConfigPath()

	proxyURL := plugin.GetAppProxyURL()
	if proxyURL == "" {
		proxyURL = strings.TrimSpace(account.ProxyUrl)
	}
	if proxyURL == "" {
		proxyURL = pool.ProxyURL()
	}

	if debugLogger != nil {
		debugLogger.SetMetadata("AccountEmail", account.Email)
		debugLogger.SetMetadata("ProxyURL", proxyURL)
	}

	upstreamURL := codexBackend.UpstreamURL(config, codexBackend.TargetPath(req.TargetPath))
	authMgr := s.GetOrCreateAuthManager(account.ID, configPath, proxyURL)

	var accessToken string
	var accountID string
	var err error
	for authAttempt := 1; authAttempt <= codexNetworkRetryAttempts; authAttempt++ {
		accessToken, accountID, err = authMgr.GetAccessToken()
		if err == nil {
			break
		}
		if debugLogger != nil {
			debugLogger.Log("认证失败: %v", err)
		}
		errStr := err.Error()
		if strings.Contains(errStr, "refresh_token_reused") {
			pool.MarkFailed(account.ID, codexShared.CodexStatusReused, 0, "refresh_token_reused")
			return &executor.UpstreamRoundTripResult{
				StatusCode: http.StatusUnauthorized,
				Error:      fmt.Errorf("auth failed: %v", err),
				TargetURL:  upstreamURL,
			}, true
		}
		if strings.Contains(errStr, "invalid_grant") {
			pool.MarkFailed(account.ID, codexShared.CodexStatusBanned, 0, "auth_failed")
			return &executor.UpstreamRoundTripResult{
				StatusCode: http.StatusUnauthorized,
				Error:      fmt.Errorf("auth failed: %v", err),
				TargetURL:  upstreamURL,
			}, true
		}
		if strings.Contains(errStr, "HTTP 401") || strings.Contains(errStr, "HTTP 403") {
			return &executor.UpstreamRoundTripResult{
				StatusCode: http.StatusUnauthorized,
				Error:      fmt.Errorf("auth failed: %v", err),
				TargetURL:  upstreamURL,
			}, true
		}
		if authAttempt < codexNetworkRetryAttempts {
			if waitErr := waitForRetry(ctx, codexNetworkRetryDelay); waitErr != nil {
				return roundTripCancelledError(waitErr), false
			}
			continue
		}
		return roundTripGatewayError(
			"auth_transient",
			"Codex authentication temporarily unavailable after retries",
			map[string]any{
				"accountId":         account.AccountID,
				"category":          "auth_transient",
				"reason":            errStr,
				"attempts":          codexNetworkRetryAttempts,
				"retryDelaySeconds": int(codexNetworkRetryDelay / time.Second),
			},
			fmt.Errorf("auth failed: %v", err),
			upstreamURL,
		), false
	}

	client := reporter.TrackHTTPClient(executor.NewHTTPClientForcedProxyURL(proxyURL, 0))
	requestPath := req.OriginalPath
	if strings.TrimSpace(requestPath) == "" {
		requestPath = req.TargetPath
	}
	source := strings.TrimSpace(req.TargetInterfaceType)
	if req.TransformContext != nil && req.TransformContext.Metadata != nil {
		if v, _ := req.TransformContext.Metadata["source_type"].(string); strings.TrimSpace(v) != "" {
			source = strings.TrimSpace(v)
		}
	}
	if strings.EqualFold(source, "chat") {
		source = codexBackend.SourceOpenAI
	}
	if strings.EqualFold(source, "claude") {
		source = codexBackend.SourceClaude
	}
	if codexBackend.IsImagesPath(requestPath) {
		source = codexBackend.SourceOpenAIImage
	}
	originalBody := req.Body
	if req.TransformContext != nil && len(req.TransformContext.OriginalRequestBody) > 0 {
		originalBody = req.TransformContext.OriginalRequestBody
	}
	buildBackendReq := func(token, acctID string) codexBackend.Request {
		return codexBackend.Request{
			Method:                 req.Method,
			Path:                   requestPath,
			Source:                 source,
			Model:                  req.RequestModel,
			Body:                   req.Body,
			OriginalBody:           originalBody,
			Headers:                req.Headers,
			IsStreaming:            req.IsStreaming,
			Config:                 config,
			EndpointHeaders:        endpointHeadersFromUpstreamRequest(req),
			Client:                 client,
			AccessToken:            token,
			AccountID:              acctID,
			LocalAccountID:         account.ID,
			PlanType:               account.PlanType,
			DisableImageGeneration: plugin.GetAppDisableImageGeneration(),
			Attempts:               codexNetworkRetryAttempts,
			RetryDelay:             codexNetworkRetryDelay,
		}
	}

	backendResult, err := codexBackend.Execute(ctx, buildBackendReq(accessToken, accountID))
	if backendResult == nil {
		backendResult = &codexBackend.Result{TargetURL: upstreamURL}
	}
	if debugLogger != nil {
		debugLogger.SetMetadata("UpstreamURL", backendResult.TargetURL)
		debugLogger.SetMetadata("StatusCode", fmt.Sprintf("%d", backendResult.StatusCode))
		debugLogger.SetSection("UpstreamRequestHeaders", formatHeaderMap(backendResult.TargetHeaders))
		if len(backendResult.RequestBody) > 0 {
			debugLogger.SetSection("UpstreamRequestBody", string(backendResult.RequestBody))
		}
		debugLogger.SetSection("UpstreamResponseHeaders", formatHTTPHeaderDebug(backendResult.StatusCode, backendResult.Headers))
	}
	if err != nil && backendResult.StatusCode == 0 {
		return roundTripGatewayError(
			"transport_error",
			"Codex upstream network request failed after retries",
			map[string]any{
				"accountId":         account.AccountID,
				"category":          "transport_error",
				"reason":            err.Error(),
				"attempts":          codexNetworkRetryAttempts,
				"retryDelaySeconds": int(codexNetworkRetryDelay / time.Second),
			},
			fmt.Errorf("upstream error: %v", err),
			backendResult.TargetURL,
		), false
	}

	if backendResult.StatusCode == http.StatusUnauthorized {
		respBody := backendResult.Body
		if refreshErr := authMgr.ForceRefresh(); refreshErr == nil {
			newToken, newAccountID, tokenErr := authMgr.GetAccessToken()
			if tokenErr == nil && newToken != "" {
				retryResult, retryErr := codexBackend.Execute(ctx, buildBackendReq(newToken, newAccountID))
				if retryErr == nil && retryResult != nil && retryResult.StatusCode == http.StatusOK {
					return buildCodexBackendSuccessRoundTrip(retryResult, debugLogger, pool, account), false
				}
			}
		}
		return &executor.UpstreamRoundTripResult{
			StatusCode:    backendResult.StatusCode,
			Body:          respBody,
			Headers:       cloneHTTPHeader(backendResult.Headers),
			TargetURL:     backendResult.TargetURL,
			TargetHeaders: backendResult.TargetHeaders,
			RequestBody:   append([]byte(nil), backendResult.RequestBody...),
			Error:         fmt.Errorf("unauthorized"),
		}, true
	}

	if backendResult.StatusCode == http.StatusForbidden {
		respBody := backendResult.Body
		pool.MarkFailed(account.ID, codexShared.CodexStatusBanned, 24*time.Hour, "suspended")
		return &executor.UpstreamRoundTripResult{
			StatusCode:    backendResult.StatusCode,
			Body:          respBody,
			Headers:       cloneHTTPHeader(backendResult.Headers),
			TargetURL:     backendResult.TargetURL,
			TargetHeaders: backendResult.TargetHeaders,
			RequestBody:   append([]byte(nil), backendResult.RequestBody...),
			Error:         fmt.Errorf("forbidden"),
		}, true
	}

	if backendResult.StatusCode == http.StatusPaymentRequired {
		respBody := backendResult.Body
		pool.MarkFailed(account.ID, codexShared.CodexStatusExhausted, 0, "quota_exhausted")
		return &executor.UpstreamRoundTripResult{
			StatusCode:    backendResult.StatusCode,
			Body:          respBody,
			Headers:       cloneHTTPHeader(backendResult.Headers),
			TargetURL:     backendResult.TargetURL,
			TargetHeaders: backendResult.TargetHeaders,
			RequestBody:   append([]byte(nil), backendResult.RequestBody...),
			Error:         fmt.Errorf("payment required"),
		}, true
	}

	if backendResult.StatusCode == http.StatusTooManyRequests {
		respBody := backendResult.Body
		cooldown := retryAfterFromBackendError(backendResult.Error)
		if cooldown <= 0 {
			cooldown = parseCooldownFromBody(respBody)
		}
		if cooldown <= 0 {
			cooldown = parseCooldownDuration(&http.Response{Header: backendResult.Headers})
		}
		pool.MarkFailed(account.ID, codexShared.CodexStatusValid, cooldown, "rate_limit")
		return &executor.UpstreamRoundTripResult{
			StatusCode:    backendResult.StatusCode,
			Body:          respBody,
			Headers:       cloneHTTPHeader(backendResult.Headers),
			TargetURL:     backendResult.TargetURL,
			TargetHeaders: backendResult.TargetHeaders,
			RequestBody:   append([]byte(nil), backendResult.RequestBody...),
			Error:         fmt.Errorf("rate limited"),
		}, true
	}

	if backendResult.StatusCode != http.StatusOK {
		respBody := backendResult.Body
		return &executor.UpstreamRoundTripResult{
			StatusCode:    backendResult.StatusCode,
			Body:          respBody,
			Headers:       cloneHTTPHeader(backendResult.Headers),
			TargetURL:     backendResult.TargetURL,
			TargetHeaders: backendResult.TargetHeaders,
			RequestBody:   append([]byte(nil), backendResult.RequestBody...),
			Error:         fmt.Errorf("upstream returned %d", backendResult.StatusCode),
		}, false
	}

	return buildCodexBackendSuccessRoundTrip(backendResult, debugLogger, pool, account), false
}

func endpointHeadersFromUpstreamRequest(req *executor.UpstreamRequest) map[string]string {
	if req == nil || req.Endpoint == nil || len(req.Endpoint.Headers) == 0 {
		return nil
	}
	headers := make(map[string]string, len(req.Endpoint.Headers))
	for key, value := range req.Endpoint.Headers {
		headers[key] = value
	}
	return headers
}

func buildCodexBackendSuccessRoundTrip(result *codexBackend.Result, debugLogger interface{ Log(string, ...any) }, pool *codex.CodexAccountPool, account *codexShared.CodexAccount) *executor.UpstreamRoundTripResult {
	if result == nil {
		return roundTripInternalError(fmt.Errorf("nil backend result"))
	}
	pool.ReportSuccess(account.ID)

	out := &executor.UpstreamRoundTripResult{
		StatusCode:    result.StatusCode,
		Headers:       cloneHTTPHeader(result.Headers),
		Body:          result.Body,
		Stream:        result.Stream,
		TargetURL:     result.TargetURL,
		TargetHeaders: result.TargetHeaders,
		RequestBody:   append([]byte(nil), result.RequestBody...),
	}
	if result.Stream == nil {
		out.Tokens = extractTokensFromBody(result.Body)
	}
	return out
}

func roundTripInternalError(err error) *executor.UpstreamRoundTripResult {
	forward := buildInternalError(err)
	return &executor.UpstreamRoundTripResult{
		StatusCode: forward.StatusCode,
		Headers:    cloneHTTPHeader(forward.Headers),
		Body:       append([]byte(nil), forward.Body...),
		Error:      forward.Error,
		TargetURL:  forward.TargetURL,
	}
}

func roundTripGatewayError(errorType, message string, details map[string]any, err error, targetURL string) *executor.UpstreamRoundTripResult {
	forward := buildGatewayExecutorError(errorType, message, details, err, targetURL)
	return &executor.UpstreamRoundTripResult{
		StatusCode: forward.StatusCode,
		Headers:    cloneHTTPHeader(forward.Headers),
		Body:       append([]byte(nil), forward.Body...),
		Error:      forward.Error,
		TargetURL:  forward.TargetURL,
	}
}

func roundTripCancelledError(err error) *executor.UpstreamRoundTripResult {
	forward := buildCancelledExecutorError(err)
	return &executor.UpstreamRoundTripResult{
		StatusCode: forward.StatusCode,
		Headers:    cloneHTTPHeader(forward.Headers),
		Body:       append([]byte(nil), forward.Body...),
		Error:      forward.Error,
	}
}

func cloneHTTPHeader(headers http.Header) http.Header {
	if headers == nil {
		return http.Header{}
	}
	return headers.Clone()
}

func applyCodexAccountStatTokens(stat *codexShared.CodexAccountStat, tokens *executor.TokenUsage) {
	if stat == nil || tokens == nil {
		return
	}
	stat.InputTokens = tokens.InputTokens
	stat.OutputTokens = tokens.OutputTokens
	stat.TotalTokens = tokenUsageTotal(tokens)
	stat.CacheReadTokens = tokens.CachedRead
	stat.CacheCreationTokens = tokens.CachedCreate
	stat.CachedTokens = tokens.CachedRead
	if stat.CachedTokens == 0 {
		stat.CachedTokens = tokens.CachedCreate
	}
	stat.ReasoningTokens = tokens.Reasoning
}

func insertCodexAccountStatAsync(store codexShared.CodexAccountStore, stat *codexShared.CodexAccountStat) {
	if store == nil || stat == nil {
		return
	}
	statCopy := *stat
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = store.InsertStat(ctx, &statCopy)
	}()
}

func formatHeaderMap(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	var builder strings.Builder
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteString(": ")
		builder.WriteString(headers[key])
		builder.WriteByte('\n')
	}
	return builder.String()
}

func formatHTTPHeaderDebug(statusCode int, headers http.Header) string {
	var builder strings.Builder
	if statusCode > 0 {
		builder.WriteString(fmt.Sprintf("Status: %d %s\n", statusCode, http.StatusText(statusCode)))
	}
	for key, values := range sanitizeHeaders(headers) {
		builder.WriteString(key)
		builder.WriteString(": ")
		builder.WriteString(values[0])
		builder.WriteByte('\n')
	}
	return builder.String()
}
