package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"clisimplehub/internal/executor"
	"clisimplehub/internal/logger"
	"clisimplehub/internal/transformer"
	kiroclaude "clisimplehub/internal/transformer/kiro/claude"

	"github.com/google/uuid"
)

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

func isAnthropicClaudeCompatiblePath(path string) bool {
	lower := strings.ToLower(strings.TrimSpace(path))
	return strings.HasPrefix(lower, "/v1/messages") || lower == "/v1/models"
}

// handleProxy handles the main proxy logic
// Requirements: 3.1, 3.2, 3.3, 3.4, 3.5, 4.1, 4.2, 4.3, 4.4, 4.5, 4.6
func (p *ProxyServer) handleProxy(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	requestID := uuid.New().String()

	reqHeaders := sanitizeHeadersForLog(r.Header)
	interfaceType := p.router.DetectInterfaceType(r.URL.Path)
	isAnthropic := isAnthropicClaudeCompatiblePath(r.URL.Path)

	isRetryable := IsRetryablePath(r.URL.Path)
	shouldRecordStats := ShouldRecordUsageStats(interfaceType, r.URL.Path)
	fallbackEnabled := p.IsFallbackEnabled()

	if required := p.getAuthKey(); required != "" && !isAuthorized(r, required) {
		if isAnthropic {
			writeAnthropicError(w, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		} else {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		}
		detail := &RequestDetail{Method: r.Method, StatusCode: http.StatusUnauthorized, RequestHeaders: reqHeaders}
		runTime := time.Since(startTime).Milliseconds()
		p.recordRequestWithDetail(requestID, interfaceType, nil, r.URL.Path, startTime, "error_401", runTime, detail)
		return
	}

	// Anthropic compatibility endpoints (Claude Code relies on these).
	if strings.EqualFold(strings.TrimSpace(r.URL.Path), "/v1/models") && strings.EqualFold(r.Method, http.MethodGet) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []any{
				map[string]any{
					"id":           "claude-sonnet-4-5-20250929",
					"object":       "model",
					"created":      1727568000,
					"owned_by":     "anthropic",
					"display_name": "Claude Sonnet 4.5",
					"type":         "chat",
					"max_tokens":   32000,
				},
				map[string]any{
					"id":           "claude-opus-4-5-20251101",
					"object":       "model",
					"created":      1730419200,
					"owned_by":     "anthropic",
					"display_name": "Claude Opus 4.5",
					"type":         "chat",
					"max_tokens":   32000,
				},
				map[string]any{
					"id":           "claude-haiku-4-5-20251001",
					"object":       "model",
					"created":      1727740800,
					"owned_by":     "anthropic",
					"display_name": "Claude Haiku 4.5",
					"type":         "chat",
					"max_tokens":   32000,
				},
			},
		})
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

	if strings.EqualFold(strings.TrimSpace(r.URL.Path), "/v1/messages/count_tokens") && strings.EqualFold(r.Method, http.MethodPost) {
		if !json.Valid(bodyBytes) {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON")
			return
		}
		tokens := kiroclaude.EstimateClaudeInputTokens(bodyBytes)
		if tokens < 1 {
			tokens = 1
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"input_tokens": tokens,
		})
		return
	}

	isStreaming := isStreamRequested(r, bodyBytes)

	exec := p.ensureExecutor()
	forwardReq := executor.ForwardRequestFromHTTP(r, bodyBytes, isStreaming)
	endpoint, resolvedType := exec.ctx.ResolveEndpoint(forwardReq.Path)
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
		return
	}

	detail := &RequestDetail{
		Method:         r.Method,
		TargetURL:      strings.TrimSuffix(endpoint.APIURL, "/") + r.URL.Path,
		RequestHeaders: reqHeaders,
		RequestStream:  string(bodyBytes),
		UpstreamAuth:   formatUpstreamAuthForLogConfig(endpoint.InterfaceType, endpoint.APIKey),
	}
	if target, err := executor.BuildTargetURL(endpoint.APIURL, r.URL.Path, r.URL.RawQuery); err == nil && target != "" {
		detail.TargetURL = target
	}

	// 如果配置了 transformer，提前计算实际转发目标 URL（用于 started 日志/控制台展示）。
	if endpoint != nil && strings.TrimSpace(endpoint.Transformer) != "" {
		if tr, err := transformer.Get(strings.TrimSpace(string(interfaceType)), endpoint.Transformer); err == nil && tr != nil {
			requestModel := extractModelFromBody(bodyBytes)
			upstreamModel := executor.ResolveUpstreamModel(requestModel, endpoint)
			targetPath := tr.TargetPath(isStreaming, upstreamModel)
			if strings.TrimSpace(targetPath) != "" {
				rawQuery := r.URL.RawQuery
				if strings.EqualFold(strings.TrimSpace(tr.TargetInterfaceType()), "kiro") {
					rawQuery = ""
				}
				if target, err := executor.BuildTargetURL(endpoint.APIURL, targetPath, rawQuery); err == nil && target != "" {
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
		if endpoint != nil {
			debugLogger.SetMetadata("Endpoint", endpoint.Name)
			debugLogger.SetMetadata("Transformer", endpoint.Transformer)
		}
		debugLogger.Log("请求开始")
		// 记录原始请求
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
	execResult := exec.retry.Execute(execCtx, forwardReq, w, enableRetry)
	result := execResult.Result

	if result != nil {
		detail.TargetURL = result.TargetURL
		detail.StatusCode = result.StatusCode
		detail.ResponseStream = result.ResponseStream
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
			if result.Error != nil {
				debugLogger.SetMetadata("UpstreamErrorStage", inferUpstreamErrorStage(result))
				debugLogger.SetMetadata("UpstreamErrorTypeChain", formatErrorTypeChain(result.Error))
				debugLogger.SetMetadata("UpstreamErrorIsEOF", fmt.Sprintf("%v", errors.Is(result.Error, io.EOF) || errors.Is(result.Error, io.ErrUnexpectedEOF)))
			}
		}
	}

	runTime := time.Since(startTime).Milliseconds()
	status := statusFromExecuteResult(result)
	p.recordRequestWithDetail(requestID, interfaceType, execResult.Endpoint, r.URL.Path, startTime, status, runTime, detail)

	// 写入调试日志文件
	if debugLogger != nil {
		debugLogger.Log("请求完成: status=%s, runTime=%dms", status, runTime)
		// 记录转换后的响应（SSE）
		if result != nil {
			debugLogger.Log("ResponseStream长度: %d, Streamed=%v", len(result.ResponseStream), result.Streamed)
			if result.ResponseStream != "" {
				debugLogger.SetSection("TransformedResponse", result.ResponseStream)
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
