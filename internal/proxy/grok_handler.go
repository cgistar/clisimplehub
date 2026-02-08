package proxy

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"clisimplehub/internal/executor"
	"clisimplehub/internal/logger"
	grokOpenai "clisimplehub/internal/transformer/grok/openai"

	"github.com/google/uuid"
)

// handleGrokProxy handles all /grok/... requests independently of the main proxy pipeline.
func (p *ProxyServer) handleGrokProxy(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	requestID := uuid.New().String()

	// Strip /grok prefix
	effectivePath := r.URL.Path[len("/grok"):]
	if effectivePath == "" {
		effectivePath = "/"
	}

	interfaceType := InterfaceType("chat")
	reqHeaders := sanitizeHeadersForLog(r.Header)

	// Auth
	if required := p.getAuthKey(); required != "" && !isAuthorized(r, required) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		detail := &RequestDetail{Method: r.Method, StatusCode: http.StatusUnauthorized, RequestHeaders: reqHeaders}
		runTime := time.Since(startTime).Milliseconds()
		p.recordRequestWithDetail(requestID, interfaceType, nil, r.URL.Path, startTime, "error_401", runTime, detail)
		logRequestToConsole(requestID, r.Method, r.URL.Path, interfaceType, nil, http.StatusUnauthorized, "error_401", runTime)
		return
	}

	// /grok/models
	if (strings.EqualFold(effectivePath, "/v1/models") || strings.EqualFold(effectivePath, "/v1/models/")) && strings.EqualFold(r.Method, http.MethodGet) {
		p.serveGrokModels(w)
		return
	}

	// Read body
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	// Grok image endpoints
	if grokOpenai.IsGrokImagePath(effectivePath) {
		grokOpenai.HandleGrokImage(w, r, effectivePath, bodyBytes)
		return
	}

	isStreaming := isStreamRequested(r, bodyBytes)
	isRetryable := IsRetryablePath(effectivePath)
	shouldRecordStats := ShouldRecordUsageStats(interfaceType, effectivePath)

	exec := p.ensureExecutor()
	forwardReq := executor.ForwardRequestFromHTTP(r, bodyBytes, isStreaming)
	forwardReq.Path = effectivePath

	endpoint := &executor.EndpointConfig{
		Name:          "Grok (virtual)",
		InterfaceType: "chat",
		Transformer:   "grok/chat",
		Enabled:       true,
	}

	detail := &RequestDetail{
		Method:         r.Method,
		TargetURL:      r.URL.Path,
		RequestHeaders: reqHeaders,
		RequestStream:  string(bodyBytes),
	}
	p.recordRequestWithDetail(requestID, interfaceType, endpoint, r.URL.Path, startTime, "in_progress", 0, detail)

	captureUpstreamReq := p.isDebugModeAll() && isRetryable && shouldRecordStats
	captureUpstreamResp := p.isDebugModeAll() && isRetryable && shouldRecordStats

	var debugLogger *logger.RequestDebugLogger
	if logger.IsDebugFileModeEnabled() {
		debugLogger = logger.NewRequestDebugLogger(requestID)
		debugLogger.SetMetadata("InterfaceType", string(interfaceType))
		debugLogger.SetMetadata("Path", r.URL.Path)
		debugLogger.SetMetadata("Method", r.Method)
		debugLogger.SetMetadata("Endpoint", endpoint.Name)
		debugLogger.SetMetadata("Transformer", endpoint.Transformer)
		debugLogger.Log("请求开始")
		debugLogger.SetSection("OriginalRequest", string(bodyBytes))
	}

	execCtx := executor.WithRequestID(r.Context(), requestID)
	if captureUpstreamReq {
		execCtx = executor.WithCaptureUpstreamRequestBody(execCtx)
	}
	if captureUpstreamResp {
		execCtx = executor.WithCaptureUpstreamResponseBody(execCtx)
	}
	if debugLogger != nil {
		execCtx = executor.WithDebugLogger(execCtx, debugLogger)
	}

	grokTr := grokOpenai.NewTransformer()
	result := exec.ctx.ExecuteGrokTransformer(execCtx, endpoint, forwardReq, w, grokTr)

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
		if debugLogger != nil {
			debugLogger.SetMetadata("UpstreamURL", result.TargetURL)
			debugLogger.SetMetadata("StatusCode", fmt.Sprintf("%d", result.StatusCode))
			if result.Error != nil {
				debugLogger.SetMetadata("UpstreamErrorMessage", formatErrorMessageForMetadata(result.Error))
			}
		}
	}

	runTime := time.Since(startTime).Milliseconds()
	status := statusFromExecuteResult(result)
	p.recordRequestWithDetail(requestID, interfaceType, endpoint, r.URL.Path, startTime, status, runTime, detail)
	logRequestToConsole(requestID, r.Method, r.URL.Path, interfaceType, endpoint, detail.StatusCode, status, runTime)

	if debugLogger != nil {
		debugLogger.Log("请求完成: status=%s, runTime=%dms", status, runTime)
		if result != nil {
			if result.Streamed && result.ResponseStream != "" {
				debugLogger.SetSection("TransformedResponse", result.ResponseStream)
			} else if len(result.Body) > 0 {
				debugLogger.SetSection("TransformedResponse", string(result.Body))
			}
		}
		_ = debugLogger.Flush()
	}

	if isRetryable {
		p.recordTokens(endpoint, result)
		if shouldRecordStats {
			requestBody := ""
			if captureUpstreamReq {
				requestBody = string(bodyBytes)
			}
			responseBody := ""
			if captureUpstreamResp && result != nil {
				responseBody = result.UpstreamResponseBody
			}
			p.insertUsageStat(r.Context(), interfaceType, endpoint, r.URL.Path, targetHeadersFromResult(result), requestBody, responseBody, runTime, statusCodeFromResult(result), status, tokensFromResult(result))
		}
	}

	if result == nil {
		http.Error(w, "Request failed", http.StatusBadGateway)
		return
	}
	if result.Streamed {
		return
	}
	if result.Error != nil && result.StatusCode == 0 {
		isEOFError := errors.Is(result.Error, io.EOF) || errors.Is(result.Error, io.ErrUnexpectedEOF)
		if isEOFError {
			http.Error(w, "Service temporarily unavailable. Please retry.", http.StatusServiceUnavailable)
		} else {
			http.Error(w, fmt.Sprintf("Request failed: %v", result.Error), http.StatusBadGateway)
		}
		return
	}
	writeResponseWithHeaders(w, result.StatusCode, result.Headers, result.Body)
}

// serveGrokModels returns the list of available Grok models in OpenAI-compatible format.
func (p *ProxyServer) serveGrokModels(w http.ResponseWriter) {
	models := []map[string]any{
		{"id": "grok-3", "object": "model", "created": 0, "owned_by": "grok"},
		{"id": "grok-3-mini", "object": "model", "created": 0, "owned_by": "grok"},
		{"id": "grok-3-thinking", "object": "model", "created": 0, "owned_by": "grok"},
		{"id": "grok-4", "object": "model", "created": 0, "owned_by": "grok"},
		{"id": "grok-4-mini", "object": "model", "created": 0, "owned_by": "grok"},
		{"id": "grok-4-thinking", "object": "model", "created": 0, "owned_by": "grok"},
		{"id": "grok-4-heavy", "object": "model", "created": 0, "owned_by": "grok"},
		{"id": "grok-4.1-mini", "object": "model", "created": 0, "owned_by": "grok"},
		{"id": "grok-4.1-fast", "object": "model", "created": 0, "owned_by": "grok"},
		{"id": "grok-4.1-expert", "object": "model", "created": 0, "owned_by": "grok"},
		{"id": "grok-4.1-thinking", "object": "model", "created": 0, "owned_by": "grok"},
		{"id": "grok-imagine-1.0", "object": "model", "created": 0, "owned_by": "grok"},
		{"id": "grok-imagine-1.0-edit", "object": "model", "created": 0, "owned_by": "grok"},
		{"id": "grok-imagine-1.0-video", "object": "model", "created": 0, "owned_by": "grok"},
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   models,
	})
}
