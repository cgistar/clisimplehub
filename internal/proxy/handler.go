package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"clisimplehub/internal/executor"
	"clisimplehub/internal/transformer"

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
				if target, err := executor.BuildTargetURL(endpoint.APIURL, targetPath, r.URL.RawQuery); err == nil && target != "" {
					detail.TargetURL = target
				}
			}
		}
	}
	p.recordRequestWithDetail(requestID, interfaceType, endpoint, r.URL.Path, startTime, "in_progress", 0, detail)

	enableRetry := isRetryable && fallbackEnabled
	execResult := exec.retry.Execute(executor.WithRequestID(r.Context(), requestID), forwardReq, w, enableRetry)
	result := execResult.Result

	if result != nil {
		detail.TargetURL = result.TargetURL
		detail.StatusCode = result.StatusCode
		detail.ResponseStream = result.ResponseStream
		if detail.ResponseStream == "" && shouldCaptureErrorResponse(result) {
			if len(result.Body) > 0 {
				detail.ResponseStream = truncateResponseBodyForLog(result.Body, 50*1024)
			} else if result.Error != nil {
				detail.ResponseStream = truncateResponseBodyForLog([]byte(result.Error.Error()), 8*1024)
			}
		}
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

func truncateResponseBodyForLog(body []byte, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 1024
	}
	raw := bytes.TrimSpace(body)
	raw = bytes.ReplaceAll(raw, []byte("\r"), []byte("\\r"))
	raw = bytes.ReplaceAll(raw, []byte("\n"), []byte("\\n"))
	if len(raw) <= maxLen {
		return string(raw)
	}
	return string(raw[:maxLen]) + "...(truncated)"
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
