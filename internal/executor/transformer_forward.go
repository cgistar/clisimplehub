package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"clisimplehub/internal/transformer"
	"clisimplehub/internal/usage"
)

func (c *ExecutionContext) executeWithTransformer(ctx context.Context, interfaceType string, endpoint *EndpointConfig, req *ForwardRequest, w http.ResponseWriter) *ForwardResult {
	result := &ForwardResult{}

	tr, err := transformer.Get(interfaceType, endpoint.Transformer)
	if err != nil {
		c.DebugLog(ctx, 3, fmt.Sprintf("[Transformer] 解析失败: interfaceType=%s transformer=%q err=%v", interfaceType, endpoint.Transformer, err))
		result.StatusCode = http.StatusBadRequest
		result.Error = err
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

	transformedBody, err := tr.TransformRequest(requestModel, originalBody, req.IsStreaming)
	if err != nil {
		c.DebugLog(ctx, 3, fmt.Sprintf("[Transformer] 请求转换失败: endpoint=%s transformer=%q err=%v", endpoint.Name, endpoint.Transformer, err))
		result.StatusCode = http.StatusBadRequest
		result.Error = err
		return result
	}

	targetURL, err := BuildTargetURL(endpoint.APIURL, targetPath, req.RawQuery)
	if err != nil {
		c.DebugLog(ctx, 3, fmt.Sprintf("[Transformer] 目标URL构造失败: endpoint=%s apiUrl=%s path=%s err=%v", endpoint.Name, endpoint.APIURL, targetPath, err))
		result.Error = err
		return result
	}
	result.TargetURL = targetURL

	requestBody := applyModelMapping(transformedBody, endpoint)
	finalModel := extractModelFromBody(requestBody)
	if finalModel == "" {
		finalModel = upstreamModel
	}
	modelMapped := requestModel != "" && finalModel != "" && requestModel != finalModel
	c.DebugLog(ctx, 1, fmt.Sprintf("转发: endpoint=%s interface=%s transformer=%q target=%s model(client=%q upstream=%q final=%q mapped=%v) stream=%v", endpoint.Name, interfaceType, endpoint.Transformer, targetURL, requestModel, upstreamModel, finalModel, modelMapped, req.IsStreaming))
	proxyReq, err := http.NewRequestWithContext(ctx, req.Method, targetURL, bytes.NewReader(requestBody))
	if err != nil {
		result.Error = fmt.Errorf("failed to create request: %w", err)
		return result
	}

	copyRequestHeaders(proxyReq, req.Headers)
	ApplyAuthForInterfaceType(proxyReq, endpoint.APIKey, tr.TargetInterfaceType(), req.IsStreaming)
	ApplyEndpointHeaders(proxyReq, endpoint)
	result.TargetHeaders = sanitizeHeaders(proxyReq.Header)

	client := NewHTTPClient(endpoint, 0)
	resp, err := client.Do(proxyReq)
	if err != nil {
		result.Error = fmt.Errorf("request failed: %w", err)
		return result
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	result.Headers = resp.Header.Clone()

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

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var capture strings.Builder
	const maxCaptureSize = 50 * 1024

	var state any

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			result.Error = ctx.Err()
			result.Streamed = true
			result.ResponseStream = capture.String()
			return result
		default:
		}

		line := scanner.Bytes()
		if capture.Len() < maxCaptureSize {
			capture.Write(line)
			capture.WriteByte('\n')
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
				result.Error = context.Canceled
				break
			}
			flusher.Flush()
		}
	}

	if err := scanner.Err(); err != nil {
		result.Error = err
	}

	result.ResponseStream = capture.String()
	result.Streamed = true
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
