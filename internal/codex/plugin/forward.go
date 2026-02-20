package codexplugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
)

const maxRetryAccounts = 5

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

	// Process request body: handle store field and non-CLI adaptation (aligned with claude-relay-service)
	processedBody, err := processRequestBody(body, r.URL.Path, userAgent)
	if err != nil {
		// Continue with original body if processing fails
		processedBody = body
	}

	isStreaming := strings.Contains(r.Header.Get("Accept"), "text/event-stream")
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

	// Check if any accounts are available before retrying
	mode := pool.Mode()
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

		var message string
		switch mode {
		case codexShared.RotationFixed:
			message = "No available Codex accounts in fixed mode. The active account may be banned, exhausted, or cooling down."
		case codexShared.RotationFailover:
			message = "No available Codex accounts in failover mode. All accounts may be banned, exhausted, or cooling down."
		case codexShared.RotationLoadBalance:
			message = "No available Codex accounts in load balance mode. All accounts may be banned, exhausted, or cooling down."
		default:
			message = "No available Codex accounts."
		}

		errJSON, _ := json.Marshal(map[string]any{
			"error": map[string]any{
				"type":    "no_available_accounts",
				"message": message,
				"code":    "codex_account_unavailable",
				"mode":    mode,
			},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
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

		result, retryable := s.forwardToUpstream(ctx, account, processedBody, isStreaming, w, pool, clientHeaders, r.URL.Path)
		if result == nil {
			// 流式响应已写入
			finalStatusCode = http.StatusOK
			finalStatus = "success"
			if debugLogger != nil {
				debugLogger.Log("流式响应已写入")
			}
			// 打印控制台日志
			runTime := time.Since(startTime).Milliseconds()
			logCodexRequestToConsole(requestID, r.Method, r.URL.Path, account, finalStatusCode, finalStatus, runTime)
			return
		}

		finalStatusCode = result.statusCode
		if result.statusCode == http.StatusOK {
			finalStatus = "success"
		} else {
			finalStatus = result.errMsg
		}

		if !retryable {
			if debugLogger != nil {
				debugLogger.SetMetadata("FinalStatusCode", fmt.Sprintf("%d", result.statusCode))
				debugLogger.Log("请求完成（不可重试）")
			}
			// 打印控制台日志
			runTime := time.Since(startTime).Milliseconds()
			logCodexRequestToConsole(requestID, r.Method, r.URL.Path, account, finalStatusCode, finalStatus, runTime)
			writeResult(w, result)
			return
		}
		lastErr = fmt.Errorf("account %s: %s", maskToken(account.RefreshToken), result.errMsg)
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
		errJSON, _ := json.Marshal(map[string]string{"error": lastErr.Error()})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write(errJSON)
		return
	}
	http.Error(w, `{"error":"no available codex accounts"}`, http.StatusServiceUnavailable)
}

type forwardResult struct {
	statusCode int
	headers    http.Header
	body       []byte
	errMsg     string
}

func (s *CodexService) forwardToUpstream(ctx context.Context, account *codexShared.CodexAccount, body []byte, isStreaming bool, w http.ResponseWriter, pool *codex.CodexAccountPool, clientHeaders http.Header, requestPath string) (result *forwardResult, retryable bool) {
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

	// Resolve proxy: global > account > plugin
	proxyURL := ""
	if gp := plugin.GetGlobalProxyProviderCached(); gp != nil {
		proxyURL = gp.GetGlobalProxyURL()
	}
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

	authMgr := s.GetOrCreateAuthManager(account.AccountID, configPath, proxyURL)
	accessToken, accountID, err := authMgr.GetAccessToken()
	if err != nil {
		if debugLogger != nil {
			debugLogger.Log("认证失败: %v", err)
		}
		errStr := err.Error()
		if strings.Contains(errStr, "refresh_token_reused") {
			pool.MarkFailed(account.AccountID, codexShared.CodexStatusReused, 0, "refresh_token_reused")
		} else if strings.Contains(errStr, "invalid_grant") || strings.Contains(errStr, "HTTP 401") || strings.Contains(errStr, "HTTP 403") {
			pool.MarkFailed(account.AccountID, codexShared.CodexStatusBanned, 0, "auth_failed")
		} else {
			// Transient failure: short cooldown, not permanent ban
			pool.MarkFailed(account.AccountID, codexShared.CodexStatusValid, 2*time.Minute, "auth_transient")
		}
		return &forwardResult{errMsg: fmt.Sprintf("auth failed: %v", err)}, true
	}

	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 0)

	upstreamURL := getCodexUpstreamURL(config, requestPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(body))
	if err != nil {
		if debugLogger != nil {
			debugLogger.Log("创建请求失败: %v", err)
		}
		return &forwardResult{errMsg: err.Error()}, false
	}
	applyCodexHeaders(req, accessToken, accountID, isStreaming, config, clientHeaders)

	if debugLogger != nil {
		debugLogger.SetMetadata("UpstreamURL", upstreamURL)
		debugLogger.Log("发送上游请求")
		// 记录请求头（脱敏）
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

	resp, err := client.Do(req)
	if err != nil {
		if debugLogger != nil {
			debugLogger.Log("上游请求失败: %v", err)
		}
		// Transport error: short cooldown to avoid reselecting same broken account
		pool.MarkFailed(account.AccountID, codexShared.CodexStatusValid, 30*time.Second, "transport_error")
		return &forwardResult{errMsg: fmt.Sprintf("upstream error: %v", err)}, true
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
							return s.handleSuccess(ctx, retryResp, isStreaming, w, pool, account)
						}
					}
				}
			}
		}
		pool.MarkFailed(account.AccountID, codexShared.CodexStatusBanned, 24*time.Hour, "unauthorized")
		return &forwardResult{statusCode: resp.StatusCode, body: respBody, errMsg: "unauthorized"}, true
	}

	if resp.StatusCode == http.StatusForbidden {
		respBody, _ := io.ReadAll(resp.Body)
		if debugLogger != nil {
			debugLogger.Log("403 禁止访问")
			debugLogger.SetSection("UpstreamResponseBody", string(respBody))
		}
		pool.MarkFailed(account.AccountID, codexShared.CodexStatusBanned, 24*time.Hour, "suspended")
		return &forwardResult{statusCode: resp.StatusCode, body: respBody, errMsg: "forbidden"}, true
	}

	if resp.StatusCode == http.StatusPaymentRequired {
		respBody, _ := io.ReadAll(resp.Body)
		if debugLogger != nil {
			debugLogger.Log("402 配额耗尽")
			debugLogger.SetSection("UpstreamResponseBody", string(respBody))
		}
		pool.MarkFailed(account.AccountID, codexShared.CodexStatusExhausted, 0, "quota_exhausted")
		return &forwardResult{statusCode: resp.StatusCode, body: respBody, errMsg: "payment required"}, false
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
			pool.UpdateUsageSnapshot(account.AccountID, snapshot)
			if debugLogger != nil {
				debugLogger.Log("提取使用率快照: primary=%.1f%%, secondary=%.1f%%",
					snapshot.PrimaryUsedPercent, snapshot.SecondaryUsedPercent)
			}
		}
		pool.MarkFailed(account.AccountID, codexShared.CodexStatusValid, cooldown, "rate_limit")
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

	pool.ReportSuccess(account.AccountID)
	if debugLogger != nil {
		debugLogger.Log("请求成功")
	}
	return s.handleSuccess(ctx, resp, isStreaming, w, pool, account)
}

func (s *CodexService) handleSuccess(ctx context.Context, resp *http.Response, isStreaming bool, w http.ResponseWriter, pool *codex.CodexAccountPool, account *codexShared.CodexAccount) (result *forwardResult, retryable bool) {
	debugLogger := executor.DebugLoggerFromContext(ctx)

	if snapshot := extractCodexUsageHeaders(resp.Header); snapshot != nil {
		pool.UpdateUsageSnapshot(account.AccountID, snapshot)
		if debugLogger != nil {
			debugLogger.Log("提取使用率快照: primary=%.1f%%, secondary=%.1f%%",
				snapshot.PrimaryUsedPercent, snapshot.SecondaryUsedPercent)
		}
	}

	if isStreaming {
		if debugLogger != nil {
			debugLogger.Log("开始流式响应")
		}
		s.streamResponse(ctx, resp, w)
		return nil, false // response already written
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		if debugLogger != nil {
			debugLogger.Log("读取响应体失败: %v", err)
		}
		return &forwardResult{errMsg: fmt.Sprintf("read response: %v", err)}, false
	}

	if debugLogger != nil {
		debugLogger.Log("读取响应体成功，长度: %d bytes", len(respBody))
		if len(respBody) > 0 && len(respBody) < 10240 { // 仅记录小于 10KB 的响应
			debugLogger.SetSection("UpstreamResponseBody", string(respBody))
		} else if len(respBody) >= 10240 {
			debugLogger.Log("响应体过大，不记录完整内容")
		}
	}

	return &forwardResult{statusCode: resp.StatusCode, headers: resp.Header.Clone(), body: respBody}, false
}

func (s *CodexService) streamResponse(ctx context.Context, resp *http.Response, w http.ResponseWriter) {
	debugLogger := executor.DebugLoggerFromContext(ctx)

	flusher, ok := w.(http.Flusher)
	if !ok {
		if debugLogger != nil {
			debugLogger.Log("ResponseWriter 不支持 Flusher，回退到非流式")
		}
		respBody, _ := io.ReadAll(resp.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(respBody)
		return
	}

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	if debugLogger != nil {
		debugLogger.Log("开始流式传输")
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	eventCount := 0
	var totalBytes int64

	for scanner.Scan() {
		line := scanner.Bytes()
		_, _ = w.Write(line)
		_, _ = w.Write([]byte("\n"))
		flusher.Flush()

		eventCount++
		totalBytes += int64(len(line)) + 1

		// 解析 usage 信息（仅用于日志）
		if debugLogger != nil && bytes.HasPrefix(line, []byte("data: ")) {
			data := bytes.TrimPrefix(line, []byte("data: "))
			if bytes.Contains(data, []byte(`"type":"response.completed"`)) {
				inputTokens, outputTokens := parseCodexUsage(data)
				if inputTokens > 0 || outputTokens > 0 {
					debugLogger.Log("Token 使用: input=%d, output=%d", inputTokens, outputTokens)
				}
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
		// Write a terminal SSE error event if stream was truncated
		_, _ = fmt.Fprintf(w, "data: {\"error\":\"stream read error: %s\"}\n\n", err.Error())
		flusher.Flush()
	}
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

// parseCodexUsage extracts token usage from a response.completed SSE event
func parseCodexUsage(data []byte) (inputTokens, outputTokens int64) {
	var event struct {
		Type     string `json:"type"`
		Response struct {
			Usage struct {
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return 0, 0
	}
	return event.Response.Usage.InputTokens, event.Response.Usage.OutputTokens
}

// Placeholder for token estimation
func EstimateCodexInputTokens(body []byte) int {
	return len(body) / 4
}

// -- Helpers for OAuth login via Wails --

func StartCodexLoginWithURL(ctx context.Context, proxyURL string) (string, func() (*codexAuth.CodexLoginResult, error), func(), error) {
	return codexAuth.StartCodexLoginWithURL(ctx, proxyURL)
}
