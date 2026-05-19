package codexplugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	codex "clisimplehub/internal/codex"
	codexAuth "clisimplehub/internal/codex/auth"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/executor"
	"clisimplehub/internal/logger"
	"clisimplehub/internal/plugin"
	chat_responses "clisimplehub/internal/transformer/chat/openai/responses"
)

const (
	maxRetryAccounts          = 5
	codexNetworkRetryAttempts = 3
	codexNetworkRetryDelay    = 3 * time.Second
)

// logCodexRequestToConsole 输出 Codex 请求日志到控制台
func logCodexRequestToConsole(requestID, method, path string, account *codexShared.CodexAccount, statusCode int, status string, runTime int64) {
	// 根据状态码确定日志级别
	level := "INFO"
	if strings.HasPrefix(status, "error") {
		level = "WARN"
		if statusCode == 0 || statusCode >= 500 {
			level = "ERROR"
		}
	} else if statusCode >= 500 {
		level = "ERROR"
	} else if statusCode >= 400 {
		level = "WARN"
	}

	// 构建账号信息
	accountInfo := "no-account"
	if account != nil {
		if account.Email != "" {
			accountInfo = account.Email
		} else if account.AccountID != "" {
			accountInfo = account.AccountID
		} else {
			accountInfo = maskToken(account.RefreshToken)
		}
	}

	// 格式化请求ID（取前8位）
	shortID := strings.TrimSpace(requestID)
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	if shortID == "" {
		shortID = "-"
	}

	// 构建状态信息
	statusInfo := status
	if statusCode > 0 {
		statusInfo = fmt.Sprintf("%s (%d)", status, statusCode)
	}

	// 输出日志：[级别] [requestID] 方法 路径 | codex | 账号 | 状态 | 用时
	log.Printf("[%s] [%s] %s %s | codex | %s | %s | %.3fs",
		level, shortID, method, path, accountInfo, statusInfo, float64(runTime)/1000.0)
}

func (s *CodexService) HandleResponses(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	// Extract User-Agent for request body processing
	userAgent := r.Header.Get("User-Agent")

	processedBody, err := processRequestBody(body, r.URL.Path, userAgent)
	if err != nil {
		if errors.Is(err, errCompactStreamingNotSupported) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(compactStreamingErrorPayload())
			return
		}
		// Continue with original body if processing fails
		processedBody = body
	}
	inboundModel := extractModelFromBody(processedBody)
	if rewrittenBody, rewritten := applyResolvedModelToBody(processedBody, ""); rewritten {
		processedBody = rewrittenBody
	}
	if bodyWithThinking, applied := applySuffixThinkingToCodexBody(processedBody, inboundModel); applied {
		processedBody = bodyWithThinking
	}

	isStreaming := strings.Contains(r.Header.Get("Accept"), "text/event-stream")
	isStreaming = normalizeStreamingModeForCodexPath(r.URL.Path, isStreaming)
	clientHeaders := r.Header.Clone() // Preserve client headers for forwarding

	pool := codex.GetPool()
	if pool == nil {
		http.Error(w, `{"error":"codex pool not initialized"}`, http.StatusInternalServerError)
		return
	}

	// 创建请求级别的调试日志记录器（每次检查配置，支持热更新）
	var debugLogger *logger.RequestDebugLogger
	requestID := executor.RequestIDFromContext(r.Context())
	if requestID == "" {
		requestID = fmt.Sprintf("codex-%d", time.Now().UnixNano())
	}
	if logger.IsDebugFileModeEnabled() {
		debugLogger = logger.NewRequestDebugLogger(requestID)
		debugLogger.SetMetadata("Plugin", "Codex")
		debugLogger.SetMetadata("Path", r.URL.Path)
		debugLogger.SetMetadata("Method", r.Method)
		debugLogger.SetMetadata("Streaming", fmt.Sprintf("%v", isStreaming))
		debugLogger.Log("Codex 请求开始")
		debugLogger.SetSection("OriginalRequest", string(processedBody))
		defer func() {
			if debugLogger != nil {
				_ = debugLogger.Flush()
			}
		}()
	}

	ctx := r.Context()
	if debugLogger != nil {
		ctx = executor.WithDebugLogger(ctx, debugLogger)
	}

	var lastErr error
	var finalStatusCode int
	var finalStatus string
	var usedAccount *codexShared.CodexAccount
	var finalInputTokens, finalOutputTokens int64
	requestModel := extractModelFromBody(processedBody)
	configPath := pool.ConfigPath()
	config, cfgErr := codexShared.LoadCodexMultiConfig(configPath)
	if cfgErr != nil && !os.IsNotExist(cfgErr) {
		if debugLogger != nil {
			debugLogger.Log("配置加载失败: %v", cfgErr)
		}
		result := buildInternalError(fmt.Errorf("config load failed: %v", cfgErr))
		runTime := time.Since(startTime).Milliseconds()
		logCodexRequestToConsole(requestID, r.Method, r.URL.Path, nil, result.StatusCode, "error_config_load", runTime)
		writeExecutorResult(w, result)
		return
	}
	if config == nil {
		config = &codexShared.CodexMultiConfig{}
	}

	// Check if any accounts are available before retrying
	mode := pool.Mode()

	// Deferred stat recording
	defer func() {
		if store := pool.Store(); store != nil && usedAccount != nil {
			now := time.Now()
			stat := &codexShared.CodexAccountStat{
				AccountID:    usedAccount.ID,
				AccountEmail: usedAccount.Email,
				Model:        requestModel,
				Date:         now.Format("2006-01-02"),
				Hour:         now.Hour(),
				InputTokens:  finalInputTokens,
				OutputTokens: finalOutputTokens,
				TotalTokens:  finalInputTokens + finalOutputTokens,
				StatusCode:   finalStatusCode,
				DurationMs:   time.Since(startTime).Milliseconds(),
				RequestPath:  r.URL.Path,
			}
			if finalStatusCode == http.StatusOK {
				stat.Status = "success"
			} else {
				stat.Status = "error"
				stat.ErrorType = finalStatus
			}
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = store.InsertStat(ctx, stat)
			}()
		}
	}()

	firstAccount := pool.Select()
	if firstAccount == nil {
		// No accounts available in any mode - return error immediately
		if debugLogger != nil {
			debugLogger.Log("%s 模式下无可用账号（账号可能已弃用或冷却中）", mode)
		}
		runTime := time.Since(startTime).Milliseconds()
		finalStatus = fmt.Sprintf("error_no_accounts_%s", mode)
		finalStatusCode = http.StatusServiceUnavailable
		logCodexRequestToConsole(requestID, r.Method, r.URL.Path, nil, finalStatusCode, finalStatus, runTime)

		statusCode, errJSON := buildNoAccountsError(mode)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = w.Write(errJSON)
		return
	}

	// First account is available, proceed with retry loop
	for attempt := 0; attempt < maxRetryAccounts; attempt++ {
		var account *codexShared.CodexAccount
		if attempt == 0 {
			// Use the first account we already selected
			account = firstAccount
		} else {
			// Select next account for retry
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

		result, retryable := s.forwardWithAccount(ctx, account, processedBody, isStreaming, w, pool, config, clientHeaders, r.URL.Path, nil)
		if result.Streamed {
			finalStatusCode = http.StatusOK
			finalStatus = "success"
			if result.Tokens != nil {
				finalInputTokens = result.Tokens.InputTokens
				finalOutputTokens = result.Tokens.OutputTokens
			}
			if debugLogger != nil {
				debugLogger.Log("流式响应已写入")
			}
			runTime := time.Since(startTime).Milliseconds()
			logCodexRequestToConsole(requestID, r.Method, r.URL.Path, account, finalStatusCode, finalStatus, runTime)
			return
		}

		finalStatusCode = result.StatusCode
		if result.StatusCode == http.StatusOK {
			finalStatus = "success"
			if result.Tokens != nil {
				finalInputTokens = result.Tokens.InputTokens
				finalOutputTokens = result.Tokens.OutputTokens
			}
		} else if result.Error != nil {
			finalStatus = result.Error.Error()
		} else {
			finalStatus = fmt.Sprintf("upstream_status_%d", result.StatusCode)
		}

		if !retryable {
			if debugLogger != nil {
				debugLogger.SetMetadata("FinalStatusCode", fmt.Sprintf("%d", result.StatusCode))
				debugLogger.Log("请求完成（不可重试）")
			}
			// 打印控制台日志
			runTime := time.Since(startTime).Milliseconds()
			logCodexRequestToConsole(requestID, r.Method, r.URL.Path, account, finalStatusCode, finalStatus, runTime)
			writeExecutorResult(w, result)
			return
		}
		lastErr = result.Error
		if lastErr == nil {
			lastErr = fmt.Errorf("status %d", result.StatusCode)
		}
		if debugLogger != nil {
			debugLogger.Log("账号失败，准备重试: %v", lastErr)
		}
	}

	if debugLogger != nil {
		debugLogger.Log("所有账号均失败")
	}

	// 打印控制台日志
	runTime := time.Since(startTime).Milliseconds()
	if lastErr != nil {
		finalStatus = "error_all_failed"
		finalStatusCode = http.StatusBadGateway
	} else {
		finalStatus = "error_no_accounts"
		finalStatusCode = http.StatusServiceUnavailable
	}
	logCodexRequestToConsole(requestID, r.Method, r.URL.Path, usedAccount, finalStatusCode, finalStatus, runTime)

	if lastErr != nil {
		_, errBody := buildAllFailedError(lastErr)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write(errBody)
		return
	}
	http.Error(w, `{"error":"no available codex accounts"}`, http.StatusServiceUnavailable)
}

type forwardResult struct {
	statusCode   int
	headers      http.Header
	body         []byte
	errMsg       string
	inputTokens  int64
	outputTokens int64
	streamed     bool
}

func (s *CodexService) forwardToUpstream(ctx context.Context, account *codexShared.CodexAccount, body []byte, isStreaming bool, w http.ResponseWriter, pool *codex.CodexAccountPool, clientHeaders http.Header, requestPath string, chatConv *chatCompletionsConversion) (result *forwardResult, retryable bool) {
	debugLogger := executor.DebugLoggerFromContext(ctx)
	configPath := pool.ConfigPath()

	// Load config for URL and headers
	// Only use defaults if config file doesn't exist; propagate other errors
	config, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			// Config file exists but is malformed/unreadable - this is a real error
			if debugLogger != nil {
				debugLogger.Log("配置加载失败: %v", err)
			}
			return &forwardResult{errMsg: fmt.Sprintf("config load failed: %v", err)}, false
		}
		// Config file doesn't exist - use defaults
		config = &codexShared.CodexMultiConfig{}
	}

	// Resolve proxy: appConfig.proxyUrl > account > plugin
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

	authMgr := s.GetOrCreateAuthManager(account.ID, configPath, proxyURL)
	var accessToken string
	var accountID string
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
			return &forwardResult{errMsg: fmt.Sprintf("auth failed: %v", err)}, true
		} else if strings.Contains(errStr, "invalid_grant") || strings.Contains(errStr, "HTTP 401") || strings.Contains(errStr, "HTTP 403") {
			pool.MarkFailed(account.ID, codexShared.CodexStatusBanned, 0, "auth_failed")
			return &forwardResult{errMsg: fmt.Sprintf("auth failed: %v", err)}, true
		}
		if authAttempt < codexNetworkRetryAttempts {
			if debugLogger != nil {
				debugLogger.Log("认证瞬时失败，%d 秒后进行第 %d/%d 次重试", int(codexNetworkRetryDelay/time.Second), authAttempt+1, codexNetworkRetryAttempts)
			}
			if waitErr := waitForRetry(ctx, codexNetworkRetryDelay); waitErr != nil {
				return buildCancelledForwardError(waitErr), false
			}
			continue
		}
		return buildGatewayError(
			"auth_transient",
			"Codex authentication temporarily unavailable after retries",
			map[string]any{
				"accountId":         account.AccountID,
				"category":          "auth_transient",
				"reason":            errStr,
				"attempts":          codexNetworkRetryAttempts,
				"retryDelaySeconds": int(codexNetworkRetryDelay / time.Second),
			},
		), false
	}

	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 0)

	upstreamURL := getCodexUpstreamURL(config, requestPath)

	// openai-response 直连路径：仅透传客户端已有的 prompt_cache_key，不自动派生。
	body, clientHeaders = passthroughPromptCacheKey(body, clientHeaders)

	buildRequest := func() (*http.Request, error) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(body))
		if reqErr != nil {
			return nil, reqErr
		}
		applyCodexHeaders(req, accessToken, accountID, isStreaming, config, clientHeaders)
		return req, nil
	}

	var resp *http.Response
	for requestAttempt := 1; requestAttempt <= codexNetworkRetryAttempts; requestAttempt++ {
		req, reqErr := buildRequest()
		if reqErr != nil {
			if debugLogger != nil {
				debugLogger.Log("创建请求失败: %v", reqErr)
			}
			return &forwardResult{errMsg: reqErr.Error()}, false
		}

		if debugLogger != nil {
			debugLogger.SetMetadata("UpstreamURL", upstreamURL)
			debugLogger.Log("发送上游请求")
			var headerLines []string
			for k, vals := range req.Header {
				if k == "Authorization" {
					headerLines = append(headerLines, fmt.Sprintf("%s: Bearer ***", k))
				} else {
					for _, v := range vals {
						headerLines = append(headerLines, fmt.Sprintf("%s: %s", k, v))
					}
				}
			}
			debugLogger.SetSection("UpstreamRequestHeaders", strings.Join(headerLines, "\n"))
		}

		resp, err = client.Do(req)
		if err == nil {
			break
		}
		if debugLogger != nil {
			debugLogger.Log("上游请求失败: %v", err)
		}
		if requestAttempt < codexNetworkRetryAttempts {
			if debugLogger != nil {
				debugLogger.Log("网络请求失败，%d 秒后进行第 %d/%d 次重试", int(codexNetworkRetryDelay/time.Second), requestAttempt+1, codexNetworkRetryAttempts)
			}
			if waitErr := waitForRetry(ctx, codexNetworkRetryDelay); waitErr != nil {
				return buildCancelledForwardError(waitErr), false
			}
			continue
		}
		return buildGatewayError(
			"transport_error",
			"Codex upstream network request failed after retries",
			map[string]any{
				"accountId":         account.AccountID,
				"category":          "transport_error",
				"reason":            err.Error(),
				"attempts":          codexNetworkRetryAttempts,
				"retryDelaySeconds": int(codexNetworkRetryDelay / time.Second),
			},
		), false
	}
	defer resp.Body.Close()

	if debugLogger != nil {
		debugLogger.SetMetadata("StatusCode", fmt.Sprintf("%d", resp.StatusCode))
		debugLogger.Log("收到上游响应: %d", resp.StatusCode)
		// 记录响应头
		var respHeaderLines []string
		for k, vals := range resp.Header {
			for _, v := range vals {
				respHeaderLines = append(respHeaderLines, fmt.Sprintf("%s: %s", k, v))
			}
		}
		debugLogger.SetSection("UpstreamResponseHeaders", strings.Join(respHeaderLines, "\n"))
	}

	// Handle error status codes
	if resp.StatusCode == http.StatusUnauthorized {
		respBody, _ := io.ReadAll(resp.Body)
		if debugLogger != nil {
			debugLogger.Log("401 未授权，尝试强制刷新 token")
			debugLogger.SetSection("UpstreamResponseBody", string(respBody))
		}
		// Try force refresh
		if refreshErr := authMgr.ForceRefresh(); refreshErr == nil {
			newToken, newAccountID, tokenErr := authMgr.GetAccessToken()
			if tokenErr == nil && newToken != "" {
				if debugLogger != nil {
					debugLogger.Log("Token 刷新成功，重试请求")
				}
				retryReq, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(body))
				if reqErr == nil {
					applyCodexHeaders(retryReq, newToken, newAccountID, isStreaming, config, clientHeaders)
					retryResp, retryErr := client.Do(retryReq)
					if retryErr == nil {
						defer retryResp.Body.Close()
						if retryResp.StatusCode == http.StatusOK {
							if debugLogger != nil {
								debugLogger.Log("重试成功")
							}
							return s.handleSuccess(ctx, retryResp, isStreaming, w, pool, account, chatConv)
						}
					}
				}
			}
		}
		pool.MarkFailed(account.ID, codexShared.CodexStatusBanned, 24*time.Hour, "unauthorized")
		return &forwardResult{statusCode: resp.StatusCode, body: respBody, errMsg: "unauthorized"}, true
	}

	if resp.StatusCode == http.StatusForbidden {
		respBody, _ := io.ReadAll(resp.Body)
		if debugLogger != nil {
			debugLogger.Log("403 禁止访问")
			debugLogger.SetSection("UpstreamResponseBody", string(respBody))
		}
		pool.MarkFailed(account.ID, codexShared.CodexStatusBanned, 24*time.Hour, "suspended")
		return &forwardResult{statusCode: resp.StatusCode, body: respBody, errMsg: "forbidden"}, true
	}

	if resp.StatusCode == http.StatusPaymentRequired {
		respBody, _ := io.ReadAll(resp.Body)
		if debugLogger != nil {
			debugLogger.Log("402 配额耗尽")
			debugLogger.SetSection("UpstreamResponseBody", string(respBody))
		}
		pool.MarkFailed(account.ID, codexShared.CodexStatusExhausted, 0, "quota_exhausted")
		return &forwardResult{statusCode: resp.StatusCode, body: respBody, errMsg: "payment required"}, true
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		respBody, _ := io.ReadAll(resp.Body)
		cooldown := parseCooldownFromBody(respBody)
		if cooldown <= 0 {
			cooldown = parseCooldownDuration(resp)
		}
		if debugLogger != nil {
			debugLogger.Log("429 速率限制，冷却时间: %v", cooldown)
			debugLogger.SetSection("UpstreamResponseBody", string(respBody))
		}
		if snapshot := extractCodexUsageHeaders(resp.Header); snapshot != nil {
			pool.UpdateUsageSnapshot(account.ID, snapshot)
			if debugLogger != nil {
				debugLogger.Log("提取使用率快照: primary=%.1f%%, secondary=%.1f%%",
					snapshot.PrimaryUsedPercent, snapshot.SecondaryUsedPercent)
			}
		}
		pool.MarkFailed(account.ID, codexShared.CodexStatusValid, cooldown, "rate_limit")
		return &forwardResult{statusCode: resp.StatusCode, body: respBody, errMsg: "rate limited"}, true
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		if debugLogger != nil {
			debugLogger.Log("非 200 响应: %d", resp.StatusCode)
			debugLogger.SetSection("UpstreamResponseBody", string(respBody))
		}
		return &forwardResult{statusCode: resp.StatusCode, headers: resp.Header.Clone(), body: respBody}, false
	}

	pool.ReportSuccess(account.ID)
	if debugLogger != nil {
		debugLogger.Log("请求成功")
	}
	return s.handleSuccess(ctx, resp, isStreaming, w, pool, account, chatConv)
}

func (s *CodexService) handleSuccess(ctx context.Context, resp *http.Response, isStreaming bool, w http.ResponseWriter, pool *codex.CodexAccountPool, account *codexShared.CodexAccount, chatConv *chatCompletionsConversion) (result *forwardResult, retryable bool) {
	debugLogger := executor.DebugLoggerFromContext(ctx)

	if snapshot := extractCodexUsageHeaders(resp.Header); snapshot != nil {
		pool.UpdateUsageSnapshot(account.ID, snapshot)
		if debugLogger != nil {
			debugLogger.Log("提取使用率快照: primary=%.1f%%, secondary=%.1f%%",
				snapshot.PrimaryUsedPercent, snapshot.SecondaryUsedPercent)
		}
	}

	if isStreaming {
		if debugLogger != nil {
			debugLogger.Log("开始流式响应")
		}
		input, output := s.streamResponse(ctx, resp, w, chatConv)
		return &forwardResult{statusCode: http.StatusOK, streamed: true, inputTokens: input, outputTokens: output}, false
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		if debugLogger != nil {
			debugLogger.Log("读取响应体失败: %v", err)
		}
		return &forwardResult{errMsg: fmt.Sprintf("read response: %v", err)}, false
	}

	// Extract tokens BEFORE CC conversion (Responses API format)
	input, output := parseCodexBodyUsage(respBody)

	// Convert Responses API → Chat Completions if needed
	headers := resp.Header.Clone()
	if chatConv != nil && resp.StatusCode == http.StatusOK && len(respBody) > 0 {
		tr := chat_responses.Transformer{}
		requestModel := extractModelFromBody(chatConv.originalBody)
		if converted, convErr := tr.TransformResponseNonStream(ctx, requestModel, chatConv.originalBody, nil, respBody, nil); convErr == nil {
			respBody = converted
			headers.Del("Content-Length")
			headers.Set("Content-Type", "application/json")
			if debugLogger != nil {
				debugLogger.Log("Responses API → Chat Completions 非流式响应转换完成")
			}
		}
	}

	if debugLogger != nil {
		debugLogger.Log("读取响应体成功，长度: %d bytes", len(respBody))
		if len(respBody) > 0 && len(respBody) < 10240 { // 仅记录小于 10KB 的响应
			debugLogger.SetSection("UpstreamResponseBody", string(respBody))
		} else if len(respBody) >= 10240 {
			debugLogger.Log("响应体过大，不记录完整内容")
		}
	}

	return &forwardResult{statusCode: resp.StatusCode, headers: headers, body: respBody, inputTokens: input, outputTokens: output}, false
}

func (s *CodexService) streamResponse(ctx context.Context, resp *http.Response, w http.ResponseWriter, chatConv *chatCompletionsConversion) (int64, int64) {
	debugLogger := executor.DebugLoggerFromContext(ctx)

	flusher, ok := w.(http.Flusher)
	if !ok {
		if debugLogger != nil {
			debugLogger.Log("ResponseWriter 不支持 Flusher，回退到非流式")
		}
		respBody, _ := io.ReadAll(resp.Body)
		if chatConv != nil && resp.StatusCode == http.StatusOK && len(respBody) > 0 {
			tr := chat_responses.Transformer{}
			requestModel := extractModelFromBody(chatConv.originalBody)
			if converted, convErr := tr.TransformResponseNonStream(ctx, requestModel, chatConv.originalBody, nil, respBody, nil); convErr == nil {
				respBody = converted
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(respBody)
		in, out := parseCodexBodyUsage(respBody)
		return in, out
	}

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	if chatConv != nil {
		w.Header().Set("Content-Type", "text/event-stream")
	}
	w.WriteHeader(resp.StatusCode)

	if debugLogger != nil {
		debugLogger.Log("开始流式传输")
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	eventCount := 0
	var totalBytes int64
	var capturedInput, capturedOutput int64

	for scanner.Scan() {
		line := scanner.Bytes()

		if chatConv != nil {
			tr := chat_responses.Transformer{}
			requestModel := extractModelFromBody(chatConv.originalBody)
			outs, _ := tr.TransformResponseStream(ctx, requestModel, chatConv.originalBody, nil, line, &chatConv.streamState)
			for _, out := range outs {
				if _, err := w.Write([]byte(out)); err != nil {
					if debugLogger != nil {
						debugLogger.Log("写入转换响应失败: %v", err)
					}
					return capturedInput, capturedOutput
				}
				flusher.Flush()
			}
		} else {
			_, _ = w.Write(line)
			_, _ = w.Write([]byte("\n"))
			flusher.Flush()
		}

		eventCount++
		totalBytes += int64(len(line)) + 1

		if tokens := tokensFromCodexStreamLine(line); tokens != nil {
			capturedInput, capturedOutput = tokens.InputTokens, tokens.OutputTokens
			if debugLogger != nil {
				debugLogger.Log("Token 使用: input=%d, output=%d", tokens.InputTokens, tokens.OutputTokens)
			}
		}
	}

	if debugLogger != nil {
		debugLogger.Log("流式传输完成: %d 事件, %d bytes", eventCount, totalBytes)
	}

	if err := scanner.Err(); err != nil {
		if debugLogger != nil {
			debugLogger.Log("流式传输错误: %v", err)
		}
		_, _ = fmt.Fprintf(w, "data: {\"error\":\"stream read error: %s\"}\n\n", err.Error())
		flusher.Flush()
	}

	if chatConv != nil && scanner.Err() == nil {
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}

	return capturedInput, capturedOutput
}

func writeResult(w http.ResponseWriter, result *forwardResult) {
	if result == nil {
		return
	}
	if result.headers != nil {
		for k, vals := range result.headers {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
	}
	if result.statusCode > 0 {
		w.WriteHeader(result.statusCode)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
	}
	if len(result.body) > 0 {
		_, _ = w.Write(result.body)
	}
}

func parseCooldownDuration(resp *http.Response) time.Duration {
	// Try Retry-After header (RFC 7231: delta-seconds or HTTP-date)
	if ra := strings.TrimSpace(resp.Header.Get("Retry-After")); ra != "" {
		// Try delta-seconds format first
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
		// Try HTTP-date format
		if t, err := http.ParseTime(ra); err == nil {
			if duration := time.Until(t); duration > 0 {
				return duration
			}
		}
	}
	// Default: 1 min
	return 1 * time.Minute
}

func parseCooldownFromBody(body []byte) time.Duration {
	var envelope struct {
		Error struct {
			ResetsInSeconds float64 `json:"resets_in_seconds"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.ResetsInSeconds > 0 {
		return time.Duration(envelope.Error.ResetsInSeconds * float64(time.Second))
	}
	return 0
}

func extractCodexUsageHeaders(headers http.Header) *codexShared.CodexUsageSnapshot {
	hasAny := false
	parseFloat := func(key string) float64 {
		v := headers.Get(key)
		if v == "" {
			return 0
		}
		hasAny = true
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0
		}
		return f
	}
	parseInt := func(key string) int {
		v := headers.Get(key)
		if v == "" {
			return 0
		}
		hasAny = true
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0
		}
		return n
	}

	s := &codexShared.CodexUsageSnapshot{
		PrimaryUsedPercent:          parseFloat("x-codex-primary-used-percent"),
		PrimaryResetAfterSeconds:    parseInt("x-codex-primary-reset-after-seconds"),
		PrimaryWindowMinutes:        parseInt("x-codex-primary-window-minutes"),
		SecondaryUsedPercent:        parseFloat("x-codex-secondary-used-percent"),
		SecondaryResetAfterSeconds:  parseInt("x-codex-secondary-reset-after-seconds"),
		SecondaryWindowMinutes:      parseInt("x-codex-secondary-window-minutes"),
		PrimaryOverSecondaryPercent: parseFloat("x-codex-primary-over-secondary-limit-percent"),
	}

	if !hasAny {
		return nil
	}
	s.UpdatedAt = time.Now()
	return s
}

func maskToken(token string) string {
	if len(token) <= 8 {
		return "***"
	}
	return token[:4] + "..." + token[len(token)-4:]
}

// parseCodexBodyUsage extracts token usage from a non-streaming response body
func parseCodexBodyUsage(body []byte) (int64, int64) {
	var resp struct {
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &resp) == nil {
		return resp.Usage.InputTokens, resp.Usage.OutputTokens
	}
	return 0, 0
}

func extractModelFromBody(body []byte) string {
	var m struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &m) == nil {
		return m.Model
	}
	return ""
}

// Placeholder for token estimation
func EstimateCodexInputTokens(body []byte) int {
	return len(body) / 4
}

// -- Helpers for OAuth login via Wails --

func StartCodexLoginWithURL(ctx context.Context, proxyURL string) (string, func() (*codexAuth.CodexLoginResult, error), func(), error) {
	return codexAuth.StartCodexLoginWithURL(ctx, proxyURL)
}
