package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"clisimplehub/internal/executor"

	"github.com/google/uuid"
)

// handleProxy handles the main proxy logic
// Requirements: 3.1, 3.2, 3.3, 3.4, 3.5, 4.1, 4.2, 4.3, 4.4, 4.5, 4.6
func (p *ProxyServer) handleProxy(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	requestID := uuid.New().String()

	reqHeaders := sanitizeHeadersForLog(r.Header)
	interfaceType := p.router.DetectInterfaceType(r.URL.Path)

	isRetryable := IsRetryablePath(r.URL.Path)
	shouldRecordStats := ShouldRecordVendorStats(interfaceType, r.URL.Path)
	fallbackEnabled := p.IsFallbackEnabled()

	if required := p.getAuthKey(); required != "" && !isAuthorized(r, required) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		detail := &RequestDetail{Method: r.Method, StatusCode: http.StatusUnauthorized, RequestHeaders: reqHeaders}
		runTime := time.Since(startTime).Milliseconds()
		p.recordRequestWithDetail(requestID, interfaceType, nil, r.URL.Path, startTime, "error_401", runTime, detail)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	isStreaming := isStreamRequested(bodyBytes)

	exec := p.ensureExecutor()
	forwardReq := executor.ForwardRequestFromHTTP(r, bodyBytes, isStreaming)
	endpoint, resolvedType := exec.ctx.ResolveEndpoint(forwardReq.Path)
	if resolvedType != "" {
		interfaceType = InterfaceType(resolvedType)
	}
	if endpoint == nil {
		http.Error(w, "No enabled endpoints available", http.StatusServiceUnavailable)
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
	p.recordRequestWithDetail(requestID, interfaceType, endpoint, r.URL.Path, startTime, "in_progress", 0, detail)

	enableRetry := isRetryable && fallbackEnabled
	execResult := exec.retry.Execute(r.Context(), forwardReq, w, enableRetry)
	result := execResult.Result

	if result != nil {
		detail.TargetURL = result.TargetURL
		detail.StatusCode = result.StatusCode
		detail.ResponseStream = result.ResponseStream
	}

	runTime := time.Since(startTime).Milliseconds()
	status := statusFromExecuteResult(result)
	p.recordRequestWithDetail(requestID, interfaceType, execResult.Endpoint, r.URL.Path, startTime, status, runTime, detail)

	if isRetryable {
		p.recordTokens(execResult.Endpoint, result)
		if shouldRecordStats {
			p.insertVendorStat(r.Context(), interfaceType, execResult.Endpoint, r.URL.Path, targetHeadersFromResult(result), runTime, statusCodeFromResult(result), status, tokensFromResult(result))
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
		http.Error(w, fmt.Sprintf("Request failed: %v", result.Error), http.StatusBadGateway)
		return
	}
	writeResponseWithHeaders(w, result.StatusCode, result.Headers, result.Body)
}

func isStreamRequested(body []byte) bool {
	var streamReq struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &streamReq)
	return streamReq.Stream
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
