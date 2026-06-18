package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"clisimplehub/internal/executor"
	"clisimplehub/internal/logger"
	appmiddleware "clisimplehub/internal/middleware"
	"clisimplehub/internal/plugin"
	"clisimplehub/internal/transformer"

	"github.com/google/uuid"
)

const maxResponseBodyLogBytes = 1 << 20

type anthropicErrorResponse struct {
	Error anthropicErrorDetail `json:"error"`
}

type anthropicErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func writeAnthropicError(w http.ResponseWriter, statusCode int, errorType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(anthropicErrorResponse{
		Error: anthropicErrorDetail{
			Type:    strings.TrimSpace(errorType),
			Message: message,
		},
	})
}

func responseBodyForDetailLog(result *executor.ForwardResult) string {
	if result == nil || result.Streamed || len(result.Body) == 0 {
		return ""
	}
	if !utf8.Valid(result.Body) {
		return ""
	}

	body := string(result.Body)
	if len(result.Body) <= maxResponseBodyLogBytes {
		return body
	}

	truncated := string(result.Body[:maxResponseBodyLogBytes])
	truncated = strings.ToValidUTF8(truncated, "")
	return truncated + "\n\n[response body truncated for request log]"
}

// logRequestToConsole 输出请求日志到控制台（无头模式）
func logRequestToConsole(requestID, method, path string, interfaceType InterfaceType, endpoint *executor.EndpointConfig, statusCode int, status string, runTime int64) {
	// now := time.Now()
	// timestamp := fmt.Sprintf("[%04d%02d%02d %02d:%02d:%02d]",
	// 	now.Year(), now.Month(), now.Day(),
	// 	now.Hour(), now.Minute(), now.Second())

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

	// 构建端点信息
	endpointInfo := "no-endpoint"
	if endpoint != nil {
		endpointInfo = endpoint.Name
		if endpoint.ProviderName != "" {
			endpointInfo = endpoint.ProviderName + "/" + endpoint.Name
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

	// 输出日志：[时间] [级别] 方法 路径 | 接口类型 | 端点 | 状态 | 用时
	log.Printf("[%s] [%s] %s %s | %s | %s | %s | %.3fs",
		level, shortID, method, path, interfaceType, endpointInfo, statusInfo, float64(runTime)/1000.0)
}

// logDebugToConsole 输出调试日志到控制台（无头模式）
// level: 0=DEBUG, 1=INFO, 2=WARN, 3=ERROR
func logDebugToConsole(requestID string, level int, message string) {
	// now := time.Now()
	// timestamp := fmt.Sprintf("%04d%02d%02d %02d:%02d:%02d",
	// 	now.Year(), now.Month(), now.Day(),
	// 	now.Hour(), now.Minute(), now.Second())

	// 日志级别映射
	levelNames := []string{"DEBUG", "INFO", "WARN", "ERROR"}
	levelIcons := []string{"🔍", "ℹ️", "⚠️", "❌"}

	levelName := "INFO"
	levelIcon := "ℹ️"
	if level >= 0 && level < len(levelNames) {
		levelName = levelNames[level]
		levelIcon = levelIcons[level]
	}

	// 格式化请求ID（取前8位）
	shortID := requestID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	// 输出格式：20260128 12:52:02 ℹ️ INFO  [requestID] message
	if shortID != "" {
		log.Printf("%s %-5s [%s] %s", levelIcon, levelName, shortID, message)
	} else {
		log.Printf("%s %-5s %s", levelIcon, levelName, message)
	}
}

func (p *ProxyServer) detectInterfaceTypeForRequest(r *http.Request) InterfaceType {
	if r == nil {
		return InterfaceTypeClaude
	}
	if forcedType, ok := gatewayInterfaceOverrideFromContext(r.Context()); ok {
		return forcedType
	}
	return p.router.DetectInterfaceType(r.URL.Path)
}

func isAnthropicRequest(interfaceType InterfaceType, path string) bool {
	return interfaceType == InterfaceTypeClaude || IsAnthropicCompatiblePath(path)
}

func (p *ProxyServer) handleV1ResponsesWebsocketRoute(w http.ResponseWriter, r *http.Request) {
	if r == nil || !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		p.handleGatewayFallback(w, r)
		return
	}

	exec := p.ensureExecutor()
	forwardReq := executor.ForwardRequestFromHTTP(r, nil, true)
	endpoint, resolvedType := p.resolveEndpointForRequest(exec, r, forwardReq)
	if resolvedType != "" && !strings.EqualFold(resolvedType, string(InterfaceTypeCodex)) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "responses websocket requires codex endpoint",
		})
		return
	}
	if endpoint == nil || !strings.EqualFold(strings.TrimSpace(endpoint.Transformer), "openai/codex") {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "responses websocket requires active codex endpoint with openai/codex transformer",
		})
		return
	}

	provider, ok := plugin.ByName("codex-accounts").(plugin.CodexResponsesWebsocketProvider)
	if !ok || provider == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "codex websocket provider not available",
		})
		return
	}
	provider.HandleResponsesWebsocket(w, r)
}

func (p *ProxyServer) resolveEndpointForRequest(exec *proxyExecutor, r *http.Request, req *executor.ForwardRequest) (*executor.EndpointConfig, string) {
	if exec == nil || req == nil {
		return nil, ""
	}
	if forcedType, ok := gatewayInterfaceOverrideFromContext(r.Context()); ok {
		interfaceType := string(forcedType)
		var endpoint *executor.EndpointConfig
		if strings.TrimSpace(req.RequestModel) != "" {
			endpoint = exec.provider.GetEndpointByModel(interfaceType, req.RequestModel)
		}
		if endpoint == nil {
			endpoint = exec.provider.GetActiveEndpoint(interfaceType)
		}
		return endpoint, interfaceType
	}
	return exec.ctx.ResolveEndpoint(req.Path, req.RequestModel)
}

// handleProxy handles the main proxy logic
// Requirements: 3.1, 3.2, 3.3, 3.4, 3.5, 4.1, 4.2, 4.3, 4.4, 4.5, 4.6
func (p *ProxyServer) handleProxy(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	requestID := uuid.New().String()

	originalHeaders := r.Header.Clone()
	if inboundHeaders, ok := appmiddleware.OriginalHeadersFromContext(r.Context()); ok {
		originalHeaders = inboundHeaders
	}
	reqHeaders := sanitizeHeadersForLog(originalHeaders)
	interfaceType := p.detectInterfaceTypeForRequest(r)
	isAnthropic := isAnthropicRequest(interfaceType, r.URL.Path)

	isRetryable := IsRetryablePath(r.URL.Path)
	shouldRecordStats := ShouldRecordUsageStats(interfaceType, r.URL.Path)
	fallbackEnabled := p.IsFallbackEnabled()

	// Keep model-list compatibility for case-insensitive paths that may bypass explicit gateway routes.
	if handleUnifiedModelsRequest(w, r) {
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		if isAnthropic {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		} else {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
		}
		return
	}
	_ = r.Body.Close()

	if IsClaudeCountTokensPath(r.URL.Path) && strings.EqualFold(r.Method, http.MethodPost) {
		if !json.Valid(bodyBytes) {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON")
			return
		}
		tokens := 1
		for _, pl := range plugin.All() {
			if te, ok := pl.(plugin.TokenEstimator); ok {
				if t := te.EstimateInputTokens(bodyBytes); t > tokens {
					tokens = t
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"input_tokens": tokens,
		})
		return
	}

	bodyBytes, err = p.applyForwardRequestMiddlewares(r.Context(), r, bodyBytes)
	if err != nil {
		if isAnthropic {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		} else {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	interfaceType = p.detectInterfaceTypeForRequest(r)
	isAnthropic = isAnthropicRequest(interfaceType, r.URL.Path)
	isRetryable = IsRetryablePath(r.URL.Path)
	shouldRecordStats = ShouldRecordUsageStats(interfaceType, r.URL.Path)

	isStreaming := isStreamRequested(r, bodyBytes)

	exec := p.ensureExecutor()
	forwardReq := executor.ForwardRequestFromHTTP(r, bodyBytes, isStreaming)
	endpoint, resolvedType := p.resolveEndpointForRequest(exec, r, forwardReq)
	if resolvedType != "" {
		interfaceType = InterfaceType(resolvedType)
	}
	if endpoint == nil {
		if isAnthropic {
			writeAnthropicError(w, http.StatusServiceUnavailable, "service_unavailable", "No enabled endpoints available")
		} else {
			http.Error(w, "No enabled endpoints available", http.StatusServiceUnavailable)
		}
		detail := &RequestDetail{
			Method:         r.Method,
			StatusCode:     http.StatusServiceUnavailable,
			RequestHeaders: reqHeaders,
			RequestStream:  string(bodyBytes),
		}
		runTime := time.Since(startTime).Milliseconds()
		p.recordRequestWithDetail(requestID, interfaceType, nil, r.URL.Path, startTime, "error_503", runTime, detail)
		logRequestToConsole(requestID, r.Method, r.URL.Path, interfaceType, nil, http.StatusServiceUnavailable, "error_503", runTime)
		return
	}

	detail := &RequestDetail{
		Method:         r.Method,
		TargetURL:      strings.TrimSuffix(endpoint.APIURL, "/") + r.URL.Path,
		RequestHeaders: reqHeaders,
		RequestStream:  string(bodyBytes),
		UpstreamAuth:   formatUpstreamAuthForLogConfig(endpoint.InterfaceType, endpoint.APIKey),
		Model:          extractModelFromBody(bodyBytes),
	}
	if target, err := executor.BuildTargetURL(endpoint.APIURL, r.URL.Path, r.URL.RawQuery); err == nil && target != "" {
		detail.TargetURL = target
	}

	// 如果配置了 transformer，提前计算实际转发目标 URL（用于 started 日志/控制台展示）。
	if strings.TrimSpace(endpoint.Transformer) != "" {
		if tr, err := transformer.Get(strings.TrimSpace(string(interfaceType)), endpoint.Transformer); err == nil && tr != nil {
			requestModel := detail.Model
			upstreamModel := executor.ResolveUpstreamModel(requestModel, endpoint)
			targetPath := tr.TargetPath(isStreaming, upstreamModel)
			if strings.TrimSpace(targetPath) != "" {
				if target, err := executor.BuildTargetURL(endpoint.APIURL, targetPath, r.URL.RawQuery); err == nil && target != "" {
					detail.TargetURL = target
				}
			}
		}
	}
	p.recordRequestWithDetail(requestID, interfaceType, endpoint, r.URL.Path, startTime, "in_progress", 0, detail)

	enableRetry := isRetryable && fallbackEnabled
	captureUpstreamRequestBody := p.isDebugModeAll() && isRetryable && shouldRecordStats
	captureUpstreamResponseBody := p.isDebugModeAll() && isRetryable && shouldRecordStats

	// 创建请求级别的调试日志记录器（每次检查配置，支持热更新）
	var debugLogger *logger.RequestDebugLogger
	if logger.IsDebugFileModeEnabled() {
		debugLogger = logger.NewRequestDebugLogger(requestID)
		debugLogger.SetMetadata("InterfaceType", string(interfaceType))
		debugLogger.SetMetadata("Path", r.URL.Path)
		debugLogger.SetMetadata("Method", r.Method)
		debugLogger.SetMetadata("Endpoint", endpoint.Name)
		debugLogger.SetMetadata("Transformer", endpoint.Transformer)
		debugLogger.Log("请求开始")
		// 记录原始请求
		debugLogger.SetOriginalHeader(originalHeaders)
		debugLogger.SetSection("OriginalRequest", string(bodyBytes))
	}

	execCtx := executor.WithRequestID(r.Context(), requestID)
	if captureUpstreamRequestBody {
		execCtx = executor.WithCaptureUpstreamRequestBody(execCtx)
	}
	if captureUpstreamResponseBody {
		execCtx = executor.WithCaptureUpstreamResponseBody(execCtx)
	}
	if debugLogger != nil {
		execCtx = executor.WithDebugLogger(execCtx, debugLogger)
	}

	// compact 非流式：在等待上游（ChatGPT backend）返回期间向下游定期发 "\n"，
	// 防止客户端或中间 nginx 因空闲超时把连接断掉。仅在环境变量启用且非流式时生效。
	stopCompactKeepAlive := maybeStartCodexCompactKeepAlive(execCtx, w, interfaceType, r.URL.Path, isStreaming)

	execResult := exec.retry.Execute(execCtx, forwardReq, w, enableRetry)
	stopCompactKeepAlive()
	result := execResult.Result

	if result != nil {
		detail.TargetURL = result.TargetURL
		detail.StatusCode = result.StatusCode
		detail.ResponseStream = result.ResponseStream
		if detail.ResponseStream == "" {
			detail.ResponseStream = responseBodyForDetailLog(result)
		}
		if detail.ResponseStream == "" && shouldCaptureErrorResponse(result) {
			if len(result.Body) > 0 {
				detail.ResponseStream = string(result.Body)
			} else if result.Error != nil {
				detail.ResponseStream = result.Error.Error()
			}
		}
		// 更新调试日志元数据
		if debugLogger != nil {
			debugLogger.SetMetadata("UpstreamURL", result.TargetURL)
			debugLogger.SetMetadata("StatusCode", fmt.Sprintf("%d", result.StatusCode))
			if len(result.TargetHeaders) > 0 {
				debugLogger.SetSection("UpstreamRequestHeaders", formatTargetHeadersForDebug(result.TargetHeaders))
			}
			if shouldReportUpstreamError(result) {
				debugLogger.SetMetadata("UpstreamErrorStage", inferUpstreamErrorStage(result))
				debugLogger.SetMetadata("UpstreamErrorTypeChain", formatErrorTypeChain(result.Error))
				debugLogger.SetMetadata("UpstreamErrorIsEOF", fmt.Sprintf("%v", errors.Is(result.Error, io.EOF) || errors.Is(result.Error, io.ErrUnexpectedEOF)))
				debugLogger.SetMetadata("UpstreamErrorIsCanceled", fmt.Sprintf("%v", isClientCanceledError(result.Error)))
				debugLogger.SetMetadata("UpstreamErrorMessage", formatErrorMessageForMetadata(result.Error))
			} else if result.StreamCompleted && result.StreamTerminalEvent != "" {
				debugLogger.SetMetadata("StreamTerminalEvent", result.StreamTerminalEvent)
			}
		}
	}

	runTime := time.Since(startTime).Milliseconds()
	status := statusFromExecuteResult(result)
	p.recordRequestWithDetail(requestID, interfaceType, execResult.Endpoint, r.URL.Path, startTime, status, runTime, detail)

	// 输出请求日志到控制台
	logRequestToConsole(requestID, r.Method, r.URL.Path, interfaceType, execResult.Endpoint, detail.StatusCode, status, runTime)

	// 写入调试日志文件
	if debugLogger != nil {
		debugLogger.Log("请求完成: status=%s, runTime=%dms", status, runTime)
		// 记录转换后的响应
		if result != nil {
			if result.Streamed {
				debugLogger.Log("ResponseStream长度: %d, Streamed=%v", len(result.ResponseStream), result.Streamed)
				if result.ResponseStream != "" {
					debugLogger.SetSection("TransformedResponse", result.ResponseStream)
				}
			} else {
				debugLogger.Log("ResponseBody长度: %d, Streamed=%v", len(result.Body), result.Streamed)
				if len(result.Body) > 0 {
					debugLogger.SetSection("TransformedResponse", string(result.Body))
				}
			}
		}
		_ = debugLogger.Flush()
	}

	if isRetryable {
		p.recordTokens(execResult.Endpoint, result)
		if shouldRecordStats {
			requestBody := usageStatsRequestBody(captureUpstreamRequestBody, bodyBytes)
			responseBody := ""
			if captureUpstreamResponseBody && result != nil {
				responseBody = result.UpstreamResponseBody
			}
			p.insertUsageStat(r.Context(), interfaceType, execResult.Endpoint, r.URL.Path, targetHeadersFromResult(result), requestBody, responseBody, runTime, statusCodeFromResult(result), status, tokensFromResult(result))
		}
	}

	if result == nil {
		if isAnthropic {
			writeAnthropicError(w, http.StatusBadGateway, "api_error", "Upstream request failed")
		} else {
			http.Error(w, "Request failed", http.StatusBadGateway)
		}
		return
	}
	if result.Streamed {
		return
	}
	if result.Error != nil && result.StatusCode == 0 {
		// 检测 EOF 错误（连接在收到响应头之前就被关闭）
		isEOFError := errors.Is(result.Error, io.EOF) || errors.Is(result.Error, io.ErrUnexpectedEOF)

		if isAnthropic {
			if isEOFError {
				// EOF 错误返回 overloaded_error，提示 Claude Code 可以重试
				writeAnthropicError(w, 529, "overloaded_error", "The API is temporarily overloaded. Please retry your request after a brief wait.")
			} else {
				writeAnthropicError(w, http.StatusBadGateway, "api_error", fmt.Sprintf("Upstream request failed: %v", result.Error))
			}
		} else {
			if isEOFError {
				http.Error(w, "Service temporarily unavailable. Please retry.", http.StatusServiceUnavailable)
			} else {
				http.Error(w, fmt.Sprintf("Request failed: %v", result.Error), http.StatusBadGateway)
			}
		}
		return
	}
	writeResponseWithHeaders(w, result.StatusCode, result.Headers, result.Body)
}

func writeResponseWithHeaders(w http.ResponseWriter, statusCode int, headers http.Header, body []byte) {
	for key, values := range headers {
		if strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(statusCode)
	_, _ = w.Write(body)
}

// maybeStartCodexCompactKeepAlive 按需为 codex /responses/compact 非流式请求启动下游 keep-alive ping。
// 返回的 stop 函数必须在开始真正写响应前调用；条件不满足时返回 no-op。
func maybeStartCodexCompactKeepAlive(ctx context.Context, w http.ResponseWriter, interfaceType InterfaceType, path string, isStreaming bool) func() {
	if isStreaming {
		return func() {}
	}
	if interfaceType != InterfaceTypeCodex {
		return func() {}
	}
	if !IsCodexCompactResponsesPath(path) {
		return func() {}
	}
	interval := appmiddleware.CodexCompactKeepAliveInterval()
	if interval <= 0 {
		return func() {}
	}
	// compact 响应始终是 JSON。这里预先声明 Content-Type，
	// 若 ticker 触发前就收到上游响应，writeResponseWithHeaders 里上游 headers 仍会以 Add 方式补全；
	// 若 ticker 先触发写入 "\n"，状态码会被 commit 为 200，JSON 解析器会忽略领先空白。
	w.Header().Set("Content-Type", "application/json")
	return appmiddleware.StartNonStreamingKeepAlive(ctx, w, interval)
}

func inferUpstreamErrorStage(result *executor.ForwardResult) string {
	if result == nil || result.Error == nil {
		return ""
	}
	if result.Streamed {
		return "stream_body"
	}
	// 未收到 HTTP 响应（无 status code）时，通常意味着在“读响应头阶段”就失败了。
	if result.StatusCode == 0 {
		return "before_response_headers"
	}
	return "response_body"
}

func formatErrorTypeChain(err error) string {
	if err == nil {
		return ""
	}
	chain := []string{fmt.Sprintf("%T", err)}
	visited := 0
	for unwrapped := errors.Unwrap(err); unwrapped != nil && visited < 16; unwrapped = errors.Unwrap(unwrapped) {
		visited++
		chain = append(chain, fmt.Sprintf("%T", unwrapped))
	}
	return strings.Join(chain, " -> ")
}

func usageStatsRequestBody(capture bool, rawBody []byte) string {
	if !capture || len(rawBody) == 0 {
		return ""
	}
	return string(rawBody)
}

func isStreamRequested(r *http.Request, body []byte) bool {
	// Many clients rely on `Accept: text/event-stream` and omit `{"stream": true}`.
	// Treat SSE accept as an explicit streaming request.
	if r != nil {
		if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream") {
			return true
		}
		if v := strings.TrimSpace(r.URL.Query().Get("stream")); strings.EqualFold(v, "true") || v == "1" {
			return true
		}
	}

	var streamReq struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &streamReq)
	return streamReq.Stream
}

func extractModelFromBody(body []byte) string {
	var req struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &req)
	return strings.TrimSpace(req.Model)
}

func shouldCaptureErrorResponse(result *executor.ForwardResult) bool {
	if result == nil {
		return false
	}
	if result.Streamed {
		return false
	}
	return result.Error != nil || result.StatusCode != http.StatusOK
}

func statusFromExecuteResult(result *executor.ForwardResult) string {
	if result == nil {
		return "error"
	}
	if result.Error != nil {
		if isBenignCompletedStreamCancel(result) {
			return "success"
		}
		if isClientCanceledError(result.Error) {
			return "canceled"
		}
		if result.StatusCode > 0 {
			return fmt.Sprintf("error_%d", result.StatusCode)
		}
		return "error"
	}
	if result.StatusCode != http.StatusOK {
		return fmt.Sprintf("error_%d", result.StatusCode)
	}
	return "success"
}

func isClientCanceledError(err error) bool {
	return executor.IsClientCanceledError(err)
}

func shouldReportUpstreamError(result *executor.ForwardResult) bool {
	if result == nil || result.Error == nil {
		return false
	}
	return !isBenignCompletedStreamCancel(result)
}

func isBenignCompletedStreamCancel(result *executor.ForwardResult) bool {
	if result == nil || result.Error == nil {
		return false
	}
	return result.Streamed &&
		result.StreamCompleted &&
		result.StatusCode == http.StatusOK &&
		isClientCanceledError(result.Error)
}

func formatErrorMessageForMetadata(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	msg = strings.ReplaceAll(msg, "\r", "\\r")
	msg = strings.ReplaceAll(msg, "\n", "\\n")
	return msg
}

func statusCodeFromResult(result *executor.ForwardResult) int {
	if result == nil {
		return 0
	}
	return result.StatusCode
}

func tokensFromResult(result *executor.ForwardResult) *executor.TokenUsage {
	if result == nil {
		return nil
	}
	return result.Tokens
}

func targetHeadersFromResult(result *executor.ForwardResult) map[string]string {
	if result == nil {
		return nil
	}
	return result.TargetHeaders
}

func formatTargetHeadersForDebug(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, key := range keys {
		b.WriteString(fmt.Sprintf("%s: %s\n", key, headers[key]))
	}
	return strings.TrimRight(b.String(), "\n")
}
