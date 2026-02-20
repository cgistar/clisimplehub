package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"clisimplehub/internal/transformer"
	"clisimplehub/internal/usage"
)

func (c *ExecutionContext) executeWithTransformer(ctx context.Context, interfaceType string, endpoint *EndpointConfig, req *ForwardRequest, w http.ResponseWriter) *ForwardResult {
	result := &ForwardResult{}
	debugLogger := DebugLoggerFromContext(ctx)

	if endpoint == nil || req == nil {
		result.StatusCode = http.StatusBadRequest
		result.Error = fmt.Errorf("nil endpoint or request")
		return result
	}

	// Delegate to registered TransformerForwarder if available.
	if fwd := c.getTransformerForwarder(endpoint.Transformer); fwd != nil {
		upstreamModel := ResolveUpstreamModel(extractModelFromBody(req.Body), endpoint)
		return fwd.Forward(ctx, req.Body, upstreamModel, req.IsStreaming, w, req.Path)
	}

	tr, err := transformer.Get(interfaceType, endpoint.Transformer)
	if err != nil {
		c.DebugLog(ctx, 3, fmt.Sprintf("[Transformer] 解析失败: interfaceType=%s transformer=%q err=%v", interfaceType, endpoint.Transformer, err))
		result.StatusCode = http.StatusBadRequest
		result.Error = err
		return result
	}
	if tr == nil {
		c.DebugLog(ctx, 3, fmt.Sprintf("[Transformer] 解析失败: interfaceType=%s transformer=%q err=nil transformer", interfaceType, endpoint.Transformer))
		result.StatusCode = http.StatusBadRequest
		result.Error = fmt.Errorf("nil transformer: interfaceType=%s transformer=%q", interfaceType, endpoint.Transformer)
		return result
	}

	originalBody := req.Body
	requestModel := extractModelFromBody(originalBody)
	upstreamModel := ResolveUpstreamModel(requestModel, endpoint)

	targetPath := tr.TargetPath(req.IsStreaming, upstreamModel)
	if strings.TrimSpace(targetPath) == "" {
		c.DebugLog(ctx, 3, fmt.Sprintf("[Transformer] 目标路径为空: endpoint=%s transformer=%q", endpoint.Name, endpoint.Transformer))
		result.StatusCode = http.StatusBadRequest
		result.Error = fmt.Errorf("empty transformer target path: transformer=%q", endpoint.Transformer)
		return result
	}

	// NOTE: `modelName` passed into the transformer must be the *upstream* model
	// (after applying endpoint default / alias mapping).
	transformedBody, err := tr.TransformRequest(upstreamModel, originalBody, req.IsStreaming)
	if err != nil {
		c.DebugLog(ctx, 3, fmt.Sprintf("[Transformer] 请求转换失败: endpoint=%s transformer=%q err=%v", endpoint.Name, endpoint.Transformer, err))
		result.StatusCode = http.StatusBadRequest
		result.Error = err
		return result
	}

	// 记录转换后的请求体
	if debugLogger != nil {
		debugLogger.SetSection("TransformedRequest", string(transformedBody))
	}

	// Build target URL
	baseURL := endpoint.APIURL
	targetURL, err := BuildTargetURL(baseURL, targetPath, req.RawQuery)
	if err != nil {
		c.DebugLog(ctx, 3, fmt.Sprintf("[Transformer] 目标URL构造失败: endpoint=%s apiUrl=%s path=%s err=%v", endpoint.Name, baseURL, targetPath, err))
		result.Error = err
		return result
	}
	result.TargetURL = targetURL

	// 记录上游URL
	if debugLogger != nil {
		debugLogger.SetMetadata("UpstreamURL", targetURL)
	}

	requestBody := transformedBody
	if shouldCaptureUpstreamRequestBody(ctx) {
		result.UpstreamRequestBody = capturedUpstreamRequestBody(requestBody)
	}
	finalModel := extractModelFromBody(requestBody)
	if strings.TrimSpace(finalModel) == "" {
		finalModel = upstreamModel
	}

	modelMapped := strings.TrimSpace(requestModel) != "" && strings.TrimSpace(upstreamModel) != "" && !strings.EqualFold(requestModel, upstreamModel)

	c.DebugLog(ctx, 1, fmt.Sprintf("转发: endpoint=%s interface=%s transformer=%q target=%s model(client=%q upstream=%q final=%q mapped=%v) clientStream=%v", endpoint.Name, interfaceType, endpoint.Transformer, targetURL, requestModel, upstreamModel, finalModel, modelMapped, req.IsStreaming))

	buildProxyReq := func() (*http.Request, error) {
		proxyReq, err := http.NewRequestWithContext(ctx, req.Method, targetURL, bytes.NewReader(requestBody))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		copyRequestHeaders(proxyReq, req.Headers)
		ApplyAuthForInterfaceType(proxyReq, endpoint.APIKey, tr.TargetInterfaceType(), req.IsStreaming)
		ApplyEndpointHeaders(proxyReq, endpoint)

		return proxyReq, nil
	}

	proxyReq, err := buildProxyReq()
	if err != nil {
		result.Error = err
		return result
	}
	result.TargetHeaders = sanitizeHeaders(proxyReq.Header)
	if debugLogger != nil {
		debugLogger.SetSection("UpstreamRequestHeaders", formatHeaderMap(result.TargetHeaders))
	}

	clientTimeout := DefaultHTTPTimeout
	if req.IsStreaming {
		clientTimeout = DisableHTTPClientTimeout
	}
	client := NewHTTPClient(endpoint, clientTimeout)
	resp, err := client.Do(proxyReq)
	if err != nil && isRetryableEOF(err) {
		if debugLogger != nil {
			debugLogger.Log("上游请求 EOF，重试一次: err=%T %v", err, err)
		}
		sleepWithContext(ctx, eofBackoffDuration(2))
		if retryReq, buildErr := buildProxyReq(); buildErr == nil {
			resp, err = client.Do(retryReq)
			if debugLogger != nil {
				if err == nil {
					debugLogger.Log("上游请求 EOF 重试成功")
				} else {
					debugLogger.Log("上游请求 EOF 重试失败: err=%T %v", err, err)
				}
			}
		}
	}
	if err != nil {
		result.Error = fmt.Errorf("request failed: %w", err)
		if debugLogger != nil {
			debugLogger.SetSection("UpstreamError", formatErrorChain(result.Error))
		}
		return result
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	result.Headers = resp.Header.Clone()
	if debugLogger != nil {
		debugLogger.SetSection("UpstreamResponseHeaders", formatHTTPHeaders(resp.Status, resp.Header))
	}

	if req.IsStreaming && resp.StatusCode == http.StatusOK && shouldTreatAsStreaming(resp, tr) {
		c.DebugLog(ctx, 1, fmt.Sprintf("响应: endpoint=%s status=%d content-type=%s (stream)", endpoint.Name, resp.StatusCode, resp.Header.Get("Content-Type")))
		return handleTransformedStreamingResponse(ctx, w, resp, result, tr, requestModel, originalBody, requestBody)
	}

	c.DebugLog(ctx, 1, fmt.Sprintf("响应: endpoint=%s status=%d content-type=%s", endpoint.Name, resp.StatusCode, resp.Header.Get("Content-Type")))
	out := handleTransformedNonStreamingResponse(ctx, resp, result, tr, requestModel, originalBody, requestBody)
	if out != nil && (out.Error != nil || out.StatusCode >= 400) && len(out.Body) > 0 {
		level := 2
		if out.Error != nil || out.StatusCode >= 500 {
			level = 3
		}
		c.DebugLog(ctx, level, fmt.Sprintf("[Transformer] 响应片段: endpoint=%s status=%d body=%s", endpoint.Name, out.StatusCode, truncateForLog(out.Body, 2048)))
	}
	return out
}

func shouldTreatAsStreaming(resp *http.Response, tr transformer.Transformer) bool {
	if resp == nil || tr == nil {
		return false
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "text/event-stream") {
		return true
	}
	// Gemini often streams as JSON lines (not SSE) depending on gateway; treat it as stream when requested.
	return strings.EqualFold(strings.TrimSpace(tr.TargetInterfaceType()), "gemini")
}

func handleTransformedStreamingResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response, result *ForwardResult, tr transformer.Transformer, modelName string, originalRequestRawJSON, requestRawJSON []byte) *ForwardResult {
	debugLogger := DebugLoggerFromContext(ctx)

	// Force Claude streaming semantics to the caller.
	for key, values := range resp.Header {
		switch strings.ToLower(key) {
		case "content-length", "content-encoding":
			continue
		case "content-type":
			continue
		}
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.Header().Set("Content-Type", tr.OutputContentType(true))
	w.WriteHeader(resp.StatusCode)

	flusher, ok := w.(http.Flusher)
	if !ok {
		result.Error = fmt.Errorf("response writer does not support flushing")
		return result
	}

	reader := getResponseReader(resp)
	if closer, ok := reader.(io.Closer); ok && reader != resp.Body {
		defer closer.Close()
	}
	reader = NewIdleTimeoutReader(reader, resp.Body, DefaultStreamReadIdleTimeout)

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var capture strings.Builder

	captureUpstream := shouldCaptureUpstreamResponseBody(ctx)
	var upstream limitedByteBuffer

	// 调试日志：捕获上游原始响应
	var debugUpstreamRaw strings.Builder

	var state any

readLoop:
	for scanner.Scan() {
		if ctx.Err() != nil {
			result.Error = ctx.Err()
			break
		}

		line := scanner.Bytes()
		capture.Write(line)
		capture.WriteByte('\n')
		if captureUpstream {
			upstream.Append(line)
			upstream.AppendByte('\n')
		}
		// 调试日志：追加原始数据
		if debugLogger != nil {
			debugUpstreamRaw.Write(line)
			debugUpstreamRaw.WriteByte('\n')
		}

		if tokens := extractStreamTokensFromLine(line); tokens != nil {
			result.Tokens = tokens
		}

		outs, err := tr.TransformResponseStream(ctx, modelName, originalRequestRawJSON, requestRawJSON, line, &state)
		if err != nil {
			continue
		}
		for _, out := range outs {
			if out == "" {
				continue
			}
			if _, err := w.Write([]byte(out)); err != nil {
				result.Error = fmt.Errorf("downstream write failed: %w", err)
				break readLoop
			}
			flusher.Flush()
		}
	}

	if err := scanner.Err(); err != nil && result.Error == nil {
		result.Error = err
	}

	// 记录上游原始响应
	if debugLogger != nil && debugUpstreamRaw.Len() > 0 {
		debugLogger.SetSection("UpstreamResponseRaw", debugUpstreamRaw.String())
	}

	result.ResponseStream = capture.String()
	result.Streamed = true
	if captureUpstream {
		result.UpstreamResponseBody = capturedUpstreamResponseBody(upstream.Bytes())
	}
	return result
}

func truncateForLog(body []byte, maxLen int) string {
	raw := strings.TrimSpace(string(body))
	raw = strings.ReplaceAll(raw, "\r", "\\r")
	raw = strings.ReplaceAll(raw, "\n", "\\n")
	if maxLen <= 0 || len(raw) <= maxLen {
		return raw
	}
	return raw[:maxLen] + "...(truncated)"
}

func handleTransformedNonStreamingResponse(ctx context.Context, resp *http.Response, result *ForwardResult, tr transformer.Transformer, modelName string, originalRequestRawJSON, requestRawJSON []byte) *ForwardResult {
	debugLogger := DebugLoggerFromContext(ctx)

	reader := getResponseReader(resp)
	if closer, ok := reader.(io.Closer); ok && reader != resp.Body {
		defer closer.Close()
	}

	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		result.Headers.Del("Content-Encoding")
		result.Headers.Del("Content-Length")
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		result.Error = fmt.Errorf("failed to read response: %w", err)
		return result
	}

	if shouldCaptureUpstreamResponseBody(ctx) {
		result.UpstreamResponseBody = capturedUpstreamResponseBody(body)
	}

	// 记录上游原始响应
	if debugLogger != nil && len(body) > 0 {
		debugLogger.SetSection("UpstreamResponseRaw", string(body))
	}

	if isLikelyHTMLResponse(resp.StatusCode, resp.Header.Get("Content-Type"), body) {
		result.StatusCode = http.StatusServiceUnavailable
		result.Error = fmt.Errorf("upstream returned HTML with HTTP 200")
		result.Body = body
		return result
	}

	converted, err := tr.TransformResponseNonStream(ctx, modelName, originalRequestRawJSON, requestRawJSON, body, nil)
	if err != nil {
		result.Error = err
		result.Body = body
		return result
	}

	result.Body = converted
	result.Headers.Set("Content-Type", tr.OutputContentType(false))
	result.Tokens = usageTokens(converted)
	return result
}

func usageTokens(body []byte) *TokenUsage {
	stats := usage.ExtractFromResponse(body)
	if stats == nil || stats.IsEmpty() {
		return nil
	}
	return &TokenUsage{
		InputTokens:  stats.InputTokens,
		OutputTokens: stats.OutputTokens,
		CachedCreate: stats.CachedCreate,
		CachedRead:   stats.CachedRead,
		Reasoning:    stats.Reasoning,
	}
}

func extractStreamTokensFromLine(line []byte) *TokenUsage {
	payload := line
	if bytes.HasPrefix(payload, []byte("data:")) {
		payload = bytes.TrimSpace(payload[5:])
	}
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return nil
	}
	stats := usage.ExtractFromResponse(payload)
	if stats == nil || stats.IsEmpty() {
		return nil
	}
	return &TokenUsage{
		InputTokens:  stats.InputTokens,
		OutputTokens: stats.OutputTokens,
		CachedCreate: stats.CachedCreate,
		CachedRead:   stats.CachedRead,
		Reasoning:    stats.Reasoning,
	}
}

func extractModelFromBody(body []byte) string {
	var payload map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return ""
	}
	model, _ := payload["model"].(string)
	return strings.TrimSpace(model)
}

func formatHeaderMap(h map[string]string) string {
	if len(h) == 0 {
		return ""
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(h[k])
		b.WriteByte('\n')
	}
	return b.String()
}

func formatHTTPHeaders(statusLine string, headers http.Header) string {
	h := sanitizeHeaders(headers)
	var b strings.Builder
	if strings.TrimSpace(statusLine) != "" {
		b.WriteString("Status: ")
		b.WriteString(statusLine)
		b.WriteByte('\n')
	}
	b.WriteString(formatHeaderMap(h))
	return b.String()
}

func formatErrorChain(err error) string {
	if err == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%T: %v\n", err, err))

	visited := 0
	for unwrapped := errors.Unwrap(err); unwrapped != nil && visited < 16; unwrapped = errors.Unwrap(unwrapped) {
		visited++
		b.WriteString(fmt.Sprintf("caused by %T: %v\n", unwrapped, unwrapped))
	}
	return b.String()
}

func bytesToSafeText(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return strings.ToValidUTF8(string(b), "�")
}
