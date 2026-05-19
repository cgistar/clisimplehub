package codexplugin

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	codex "clisimplehub/internal/codex"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/executor"
	"clisimplehub/internal/plugin"
)

func (s *CodexService) RoundTrip(ctx context.Context, req *executor.UpstreamRequest) (ret *executor.UpstreamRoundTripResult) {
	startTime := time.Now()
	var usedAccount *codexShared.CodexAccount
	requestModel := ""
	requestPath := ""
	if req != nil {
		requestModel = strings.TrimSpace(req.RequestModel)
		if requestModel == "" {
			requestModel = extractModelFromBody(req.Body)
		}
		requestPath = req.OriginalPath
		if requestPath == "" {
			requestPath = req.TargetPath
		}
	}

	defer func() {
		pool := codex.GetPool()
		if pool == nil || ret == nil || usedAccount == nil {
			return
		}
		store := pool.Store()
		if store == nil {
			return
		}
		now := time.Now()
		stat := &codexShared.CodexAccountStat{
			AccountID:    usedAccount.ID,
			AccountEmail: usedAccount.Email,
			Model:        requestModel,
			Date:         now.Format("2006-01-02"),
			Hour:         now.Hour(),
			StatusCode:   ret.StatusCode,
			DurationMs:   time.Since(startTime).Milliseconds(),
			RequestPath:  requestPath,
		}
		if ret.Error != nil {
			stat.Status = "error"
			stat.ErrorType = ret.Error.Error()
		} else {
			stat.Status = "success"
		}
		if ret.Tokens != nil {
			stat.InputTokens = ret.Tokens.InputTokens
			stat.OutputTokens = ret.Tokens.OutputTokens
			stat.TotalTokens = tokenUsageTotal(ret.Tokens)
		}
		if ret.Stream != nil {
			// 流式响应的 token 只能在下游读取到 completed 事件后确定。
			ret.Stream = newCodexStatsReadCloser(ret.Stream, store, stat)
			return
		}
		insertCodexAccountStatAsync(store, stat)
	}()

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
			account = pool.Select()
			if account == nil {
				if debugLogger != nil {
					debugLogger.Log("第 %d 次尝试：无可用账号", attempt+1)
				}
				break
			}
		}
		usedAccount = account

		if debugLogger != nil {
			debugLogger.Log("尝试账号 %s (attempt %d)", maskToken(account.RefreshToken), attempt+1)
		}

		upstream, retryable := s.roundTripWithAccount(ctx, account, req, pool, config)
		if upstream == nil {
			return roundTripInternalError(fmt.Errorf("unexpected nil round-trip result"))
		}
		if upstream.StatusCode == http.StatusOK {
			return upstream
		}
		if !retryable {
			return upstream
		}
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

func (s *CodexService) roundTripWithAccount(ctx context.Context, account *codexShared.CodexAccount, req *executor.UpstreamRequest, pool *codex.CodexAccountPool, config *codexShared.CodexMultiConfig) (*executor.UpstreamRoundTripResult, bool) {
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

	upstreamURL := getCodexUpstreamURL(config, req.TargetPath)
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
		if strings.Contains(errStr, "invalid_grant") || strings.Contains(errStr, "HTTP 401") || strings.Contains(errStr, "HTTP 403") {
			pool.MarkFailed(account.ID, codexShared.CodexStatusBanned, 0, "auth_failed")
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

	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 0)
	buildRequest := func(token, acctID string) (*http.Request, map[string]string, error) {
		httpReq, reqErr := http.NewRequestWithContext(ctx, req.Method, upstreamURL, bytes.NewReader(req.Body))
		if reqErr != nil {
			return nil, nil, reqErr
		}
		applyCodexHeaders(httpReq, token, acctID, req.IsStreaming, config, req.Headers)
		return httpReq, sanitizeHeaderMap(httpReq.Header), nil
	}

	var (
		resp          *http.Response
		targetHeaders map[string]string
	)
	for requestAttempt := 1; requestAttempt <= codexNetworkRetryAttempts; requestAttempt++ {
		httpReq, sanitizedHeaders, reqErr := buildRequest(accessToken, accountID)
		if reqErr != nil {
			return roundTripInternalError(reqErr), false
		}
		targetHeaders = sanitizedHeaders

		if debugLogger != nil {
			debugLogger.SetMetadata("UpstreamURL", upstreamURL)
			debugLogger.SetSection("UpstreamRequestHeaders", formatHeaderMap(targetHeaders))
		}

		resp, err = client.Do(httpReq)
		if err == nil {
			break
		}
		if debugLogger != nil {
			debugLogger.Log("上游请求失败: %v", err)
		}
		if requestAttempt < codexNetworkRetryAttempts {
			if waitErr := waitForRetry(ctx, codexNetworkRetryDelay); waitErr != nil {
				return roundTripCancelledError(waitErr), false
			}
			continue
		}
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
			upstreamURL,
		), false
	}

	if debugLogger != nil {
		debugLogger.SetMetadata("StatusCode", fmt.Sprintf("%d", resp.StatusCode))
		debugLogger.SetSection("UpstreamResponseHeaders", formatHTTPHeaderDebug(resp.StatusCode, resp.Header))
	}

	if resp.StatusCode == http.StatusUnauthorized {
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if refreshErr := authMgr.ForceRefresh(); refreshErr == nil {
			newToken, newAccountID, tokenErr := authMgr.GetAccessToken()
			if tokenErr == nil && newToken != "" {
				retryReq, sanitizedHeaders, reqErr := buildRequest(newToken, newAccountID)
				if reqErr == nil {
					targetHeaders = sanitizedHeaders
					retryResp, retryErr := client.Do(retryReq)
					if retryErr == nil {
						if retryResp.StatusCode == http.StatusOK {
							return buildCodexSuccessRoundTrip(retryResp, req.IsStreaming, upstreamURL, targetHeaders, debugLogger, pool, account), false
						}
						_ = retryResp.Body.Close()
					}
				}
			}
		}
		pool.MarkFailed(account.ID, codexShared.CodexStatusBanned, 24*time.Hour, "unauthorized")
		return &executor.UpstreamRoundTripResult{
			StatusCode:    resp.StatusCode,
			Body:          respBody,
			Headers:       http.Header{"Content-Type": []string{"application/json"}},
			TargetURL:     upstreamURL,
			TargetHeaders: targetHeaders,
			Error:         fmt.Errorf("unauthorized"),
		}, true
	}

	if resp.StatusCode == http.StatusForbidden {
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		pool.MarkFailed(account.ID, codexShared.CodexStatusBanned, 24*time.Hour, "suspended")
		return &executor.UpstreamRoundTripResult{
			StatusCode:    resp.StatusCode,
			Body:          respBody,
			Headers:       cloneHTTPHeader(resp.Header),
			TargetURL:     upstreamURL,
			TargetHeaders: targetHeaders,
			Error:         fmt.Errorf("forbidden"),
		}, true
	}

	if resp.StatusCode == http.StatusPaymentRequired {
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		pool.MarkFailed(account.ID, codexShared.CodexStatusExhausted, 0, "quota_exhausted")
		return &executor.UpstreamRoundTripResult{
			StatusCode:    resp.StatusCode,
			Body:          respBody,
			Headers:       cloneHTTPHeader(resp.Header),
			TargetURL:     upstreamURL,
			TargetHeaders: targetHeaders,
			Error:         fmt.Errorf("payment required"),
		}, true
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cooldown := parseCooldownFromBody(respBody)
		if cooldown <= 0 {
			cooldown = parseCooldownDuration(resp)
		}
		if snapshot := extractCodexUsageHeaders(resp.Header); snapshot != nil {
			pool.UpdateUsageSnapshot(account.ID, snapshot)
		}
		pool.MarkFailed(account.ID, codexShared.CodexStatusValid, cooldown, "rate_limit")
		return &executor.UpstreamRoundTripResult{
			StatusCode:    resp.StatusCode,
			Body:          respBody,
			Headers:       cloneHTTPHeader(resp.Header),
			TargetURL:     upstreamURL,
			TargetHeaders: targetHeaders,
			Error:         fmt.Errorf("rate limited"),
		}, true
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return &executor.UpstreamRoundTripResult{
			StatusCode:    resp.StatusCode,
			Body:          respBody,
			Headers:       cloneHTTPHeader(resp.Header),
			TargetURL:     upstreamURL,
			TargetHeaders: targetHeaders,
			Error:         fmt.Errorf("upstream returned %d", resp.StatusCode),
		}, false
	}

	return buildCodexSuccessRoundTrip(resp, req.IsStreaming, upstreamURL, targetHeaders, debugLogger, pool, account), false
}

func buildCodexSuccessRoundTrip(resp *http.Response, isStreaming bool, upstreamURL string, targetHeaders map[string]string, debugLogger interface{ Log(string, ...any) }, pool *codex.CodexAccountPool, account *codexShared.CodexAccount) *executor.UpstreamRoundTripResult {
	if snapshot := extractCodexUsageHeaders(resp.Header); snapshot != nil {
		pool.UpdateUsageSnapshot(account.ID, snapshot)
	}
	pool.ReportSuccess(account.ID)

	result := &executor.UpstreamRoundTripResult{
		StatusCode:    resp.StatusCode,
		Headers:       cloneHTTPHeader(resp.Header),
		TargetURL:     upstreamURL,
		TargetHeaders: targetHeaders,
	}
	if isStreaming {
		result.Stream = resp.Body
		return result
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	_ = resp.Body.Close()
	if err != nil {
		return roundTripInternalError(fmt.Errorf("read response: %v", err))
	}
	result.Body = body
	result.Tokens = extractTokensFromBody(body)
	return result
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

func sanitizeHeaderMap(headers http.Header) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		if strings.EqualFold(key, "Authorization") {
			out[key] = "Bearer ***"
			continue
		}
		out[key] = values[0]
	}
	return out
}

func cloneHTTPHeader(headers http.Header) http.Header {
	if headers == nil {
		return http.Header{}
	}
	return headers.Clone()
}

type codexStatsReadCloser struct {
	upstream io.ReadCloser
	store    codexShared.CodexAccountStore
	stat     *codexShared.CodexAccountStat

	lineBuf []byte
	latest  *executor.TokenUsage
	once    sync.Once
}

func newCodexStatsReadCloser(upstream io.ReadCloser, store codexShared.CodexAccountStore, stat *codexShared.CodexAccountStat) io.ReadCloser {
	if upstream == nil || store == nil || stat == nil {
		return upstream
	}
	statCopy := *stat
	return &codexStatsReadCloser{
		upstream: upstream,
		store:    store,
		stat:     &statCopy,
	}
}

func (r *codexStatsReadCloser) Read(p []byte) (int, error) {
	n, err := r.upstream.Read(p)
	if n > 0 {
		r.feed(p[:n])
	}
	if err != nil {
		r.finish(err)
	}
	return n, err
}

func (r *codexStatsReadCloser) Close() error {
	err := r.upstream.Close()
	r.finish(err)
	return err
}

func (r *codexStatsReadCloser) feed(chunk []byte) {
	r.lineBuf = append(r.lineBuf, chunk...)
	for {
		idx := bytes.IndexByte(r.lineBuf, '\n')
		if idx < 0 {
			return
		}
		line := append([]byte(nil), r.lineBuf[:idx]...)
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		r.lineBuf = r.lineBuf[idx+1:]
		if tokens := tokensFromCodexStreamLine(line); tokens != nil {
			r.latest = tokens
		}
	}
}

func (r *codexStatsReadCloser) finish(readErr error) {
	r.once.Do(func() {
		if len(r.lineBuf) > 0 {
			if tokens := tokensFromCodexStreamLine(r.lineBuf); tokens != nil {
				r.latest = tokens
			}
		}
		if readErr != nil && readErr != io.EOF {
			r.stat.Status = "error"
			r.stat.ErrorType = readErr.Error()
		}
		if r.latest != nil {
			r.stat.InputTokens = r.latest.InputTokens
			r.stat.OutputTokens = r.latest.OutputTokens
			r.stat.TotalTokens = tokenUsageTotal(r.latest)
		}
		insertCodexAccountStatAsync(r.store, r.stat)
	})
}

func tokensFromCodexStreamLine(line []byte) *executor.TokenUsage {
	payload := bytes.TrimSpace(line)
	if bytes.HasPrefix(payload, []byte("data:")) {
		payload = bytes.TrimSpace(payload[len("data:"):])
	}
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return nil
	}
	return extractTokensFromBody(payload)
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
