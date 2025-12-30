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
	"time"

	"clisimplehub/internal/transformer"
	kiroAuth "clisimplehub/internal/transformer/kiro"
	kiro_claude "clisimplehub/internal/transformer/kiro/claude"
	"clisimplehub/internal/usage"
)

func (c *ExecutionContext) executeWithTransformer(ctx context.Context, interfaceType string, endpoint *EndpointConfig, req *ForwardRequest, w http.ResponseWriter) *ForwardResult {
	result := &ForwardResult{}
	if endpoint == nil || req == nil {
		result.StatusCode = http.StatusBadRequest
		result.Error = fmt.Errorf("nil endpoint or request")
		return result
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
	// (after applying endpoint default / alias mapping). Some transformers (e.g. Kiro)
	// do not carry a top-level `model` field in the transformed payload, so applying
	// model mapping after transformation would be ineffective (or even inject an
	// invalid `model` field into the upstream request).
	transformedBody, err := tr.TransformRequest(upstreamModel, originalBody, req.IsStreaming)
	if err != nil {
		c.DebugLog(ctx, 3, fmt.Sprintf("[Transformer] 请求转换失败: endpoint=%s transformer=%q err=%v", endpoint.Name, endpoint.Transformer, err))
		result.StatusCode = http.StatusBadRequest
		result.Error = err
		return result
	}

	targetInterface := strings.ToLower(strings.TrimSpace(tr.TargetInterfaceType()))

	// Build target URL - special handling for kiro transformer (region-specific URL)
	var targetURL string
	baseURL := endpoint.APIURL
	if targetInterface == "kiro" {
		if urlProvider, ok := tr.(interface{ GetAPIURL() string }); ok && urlProvider != nil {
			if apiURL := strings.TrimSpace(urlProvider.GetAPIURL()); apiURL != "" {
				baseURL = apiURL
			}
		}
	}
	rawQuery := req.RawQuery
	// Kiro endpoints don't accept arbitrary client query parameters (e.g. Anthropic-style `?beta=true`).
	// The reference implementation always calls the Kiro API without query parameters.
	if targetInterface == "kiro" {
		rawQuery = ""
	}
	targetURL, err = BuildTargetURL(baseURL, targetPath, rawQuery)
	if err != nil {
		c.DebugLog(ctx, 3, fmt.Sprintf("[Transformer] 目标URL构造失败: endpoint=%s apiUrl=%s path=%s err=%v", endpoint.Name, baseURL, targetPath, err))
		result.Error = err
		return result
	}
	result.TargetURL = targetURL

	requestBody := transformedBody
	if shouldCaptureUpstreamRequestBody(ctx) {
		result.UpstreamRequestBody = capturedUpstreamRequestBody(requestBody)
	}
	finalModel := extractModelFromBody(requestBody)
	if strings.TrimSpace(finalModel) == "" {
		finalModel = upstreamModel
	}

	modelMapped := strings.TrimSpace(requestModel) != "" && strings.TrimSpace(upstreamModel) != "" && !strings.EqualFold(requestModel, upstreamModel)

	// For Kiro, the effective model is `userInputMessage.modelId`, not a top-level `model`.
	// Log the resolved Kiro model id to make 403 diagnostics easier.
	if targetInterface == "kiro" {
		kiroModelID := kiro_claude.GetKiroModelID(upstreamModel)
		c.DebugLog(ctx, 1, fmt.Sprintf("转发: endpoint=%s interface=%s transformer=%q target=%s model(client=%q upstream=%q final=%q mapped=%v kiroModelId=%q) clientStream=%v", endpoint.Name, interfaceType, endpoint.Transformer, targetURL, requestModel, upstreamModel, finalModel, modelMapped, kiroModelID, req.IsStreaming))
	} else {
		c.DebugLog(ctx, 1, fmt.Sprintf("转发: endpoint=%s interface=%s transformer=%q target=%s model(client=%q upstream=%q final=%q mapped=%v) clientStream=%v", endpoint.Name, interfaceType, endpoint.Transformer, targetURL, requestModel, upstreamModel, finalModel, modelMapped, req.IsStreaming))
	}

	buildProxyReq := func() (*http.Request, error) {
		proxyReq, err := http.NewRequestWithContext(ctx, req.Method, targetURL, bytes.NewReader(requestBody))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		// Apply authentication - special handling for kiro transformer
		if targetInterface == "kiro" {
			// Do NOT forward headers to the AWS Kiro/CodeWhisperer API.
			// Some upstream paths are sensitive to unrelated client/vendor/custom headers and can return 403.
			src, ok := tr.(kiroAuth.KiroAuthSource)
			if !ok || src == nil {
				return nil, fmt.Errorf("kiro auth source not available for transformer=%T", tr)
			}
			authApplier := kiroAuth.NewAuthApplier(src)
			if err := authApplier.Apply(proxyReq); err != nil {
				return nil, fmt.Errorf("kiro auth failed: %w", err)
			}
		} else {
			copyRequestHeaders(proxyReq, req.Headers)
			ApplyAuthForInterfaceType(proxyReq, endpoint.APIKey, tr.TargetInterfaceType(), req.IsStreaming)
			ApplyEndpointHeaders(proxyReq, endpoint)
		}

		return proxyReq, nil
	}

	proxyReq, err := buildProxyReq()
	if err != nil {
		result.Error = err
		return result
	}
	result.TargetHeaders = sanitizeHeaders(proxyReq.Header)

	client := NewHTTPClient(endpoint, 0)
	if targetInterface == "kiro" {
		proxyURL := ""
		if proxyProvider, ok := tr.(interface{ KiroProxyURL() string }); ok && proxyProvider != nil {
			proxyURL = proxyProvider.KiroProxyURL()
		}
		client = NewHTTPClientForcedProxyURL(proxyURL, 0)
	}
	resp, err := client.Do(proxyReq)
	if err != nil {
		result.Error = fmt.Errorf("request failed: %w", err)
		return result
	}
	// when backend returns 401/403, force-refresh token and retry once.
	if targetInterface == "kiro" && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if refresher, ok := tr.(interface{ ForceRefreshKiroToken() error }); ok && refresher != nil {
			if refreshErr := refresher.ForceRefreshKiroToken(); refreshErr == nil {
				time.Sleep(50 * time.Millisecond) // small jitter to avoid immediate retry collisions
				if retryReq, err := buildProxyReq(); err == nil {
					resp, err = client.Do(retryReq)
					if err != nil {
						result.Error = fmt.Errorf("request failed after kiro token refresh: %w", err)
						return result
					}
				}
			}
		}
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	result.Headers = resp.Header.Clone()

	// For Kiro upstream errors, pass through the raw upstream body to aid debugging.
	// Transforming non-200 responses often hides the real error payload.
	if targetInterface == "kiro" && resp.StatusCode != http.StatusOK {
		reader := getResponseReader(resp)
		if closer, ok := reader.(io.Closer); ok && reader != resp.Body {
			defer closer.Close()
		}

		if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
			result.Headers.Del("Content-Encoding")
			result.Headers.Del("Content-Length")
		}

		body, readErr := io.ReadAll(reader)
		if readErr != nil {
			result.Error = fmt.Errorf("failed to read response: %w", readErr)
			return result
		}

		if shouldCaptureUpstreamResponseBody(ctx) {
			result.UpstreamResponseBody = capturedUpstreamResponseBody(body)
		}

		result.Body = body
		return result
	}

	// Kiro upstream uses eventstream semantics even for non-stream callers.
	// always parse upstream stream; for non-stream callers
	// collect the stream into a single Claude message response.
	if targetInterface == "kiro" && resp.StatusCode == http.StatusOK {
		if req.IsStreaming {
			c.DebugLog(ctx, 1, fmt.Sprintf("响应: endpoint=%s status=%d content-type=%s (kiro stream)", endpoint.Name, resp.StatusCode, resp.Header.Get("Content-Type")))
			return handleKiroStreamingResponse(ctx, w, resp, result, tr, requestModel, originalBody, requestBody)
		}
		c.DebugLog(ctx, 1, fmt.Sprintf("响应: endpoint=%s status=%d content-type=%s (kiro non-stream)", endpoint.Name, resp.StatusCode, resp.Header.Get("Content-Type")))
		return handleTransformedNonStreamingResponse(ctx, resp, result, tr, requestModel, originalBody, requestBody)
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

func handleKiroStreamingResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response, result *ForwardResult, tr transformer.Transformer, modelName string, originalRequestRawJSON, requestRawJSON []byte) *ForwardResult {
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

	var state any

	var capture strings.Builder

	captureUpstream := shouldCaptureUpstreamResponseBody(ctx)
	var upstream limitedByteBuffer

	// 连续超时容错机制
	const (
		readTimeout            = 30 * time.Second
		maxConsecutiveTimeouts = 3
	)
	consecutiveTimeouts := 0

	buf := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			result.Error = ctx.Err()
			result.Streamed = true
			result.ResponseStream = capture.String()
			if captureUpstream {
				result.UpstreamResponseBody = capturedUpstreamResponseBody(upstream.Bytes())
			}
			return result
		default:
		}

		// 使用带超时的读取
		type readResult struct {
			n   int
			err error
		}
		readChan := make(chan readResult, 1)
		go func() {
			n, err := reader.Read(buf)
			readChan <- readResult{n: n, err: err}
		}()

		var n int
		var err error
		select {
		case res := <-readChan:
			n, err = res.n, res.err
			consecutiveTimeouts = 0 // 成功读取，重置计数器
		case <-time.After(readTimeout):
			consecutiveTimeouts++
			if consecutiveTimeouts <= maxConsecutiveTimeouts {
				// 记录警告但继续
				continue
			}
			// 超过最大连续超时次数，退出
			result.Error = fmt.Errorf("kiro stream: consecutive read timeout (%d times)", maxConsecutiveTimeouts)
			result.Streamed = true
			result.ResponseStream = capture.String()
			if captureUpstream {
				result.UpstreamResponseBody = capturedUpstreamResponseBody(upstream.Bytes())
			}
			return result
		}

		if n > 0 {
			chunk := buf[:n]
			if captureUpstream {
				upstream.Append(chunk)
			}
			outs, trErr := tr.TransformResponseStream(ctx, modelName, originalRequestRawJSON, requestRawJSON, chunk, &state)
			if trErr != nil {
				result.Error = trErr
				break
			}
			for _, out := range outs {
				if out == "" {
					continue
				}
				if _, err := w.Write([]byte(out)); err != nil {
					result.Error = context.Canceled
					break
				}
				capture.WriteString(out)
				flusher.Flush()
			}

			if tok, ok := state.(interface{ TokenUsage() (int, int) }); ok && tok != nil {
				in, out := tok.TokenUsage()
				if in != 0 || out != 0 {
					result.Tokens = &TokenUsage{InputTokens: int64(in), OutputTokens: int64(out)}
				}
			}
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			result.Error = err
			break
		}
	}

	// Give the transformer a chance to emit a final end marker when upstream ends (best-effort).
	if state != nil {
		if outs, trErr := tr.TransformResponseStream(ctx, modelName, originalRequestRawJSON, requestRawJSON, nil, &state); trErr == nil {
			for _, out := range outs {
				if out == "" {
					continue
				}
				if _, err := w.Write([]byte(out)); err != nil {
					result.Error = context.Canceled
					break
				}
				capture.WriteString(out)
				flusher.Flush()
			}
		}
	}

	// If upstream ended without an explicit end marker, finish the Claude SSE stream.
	if s, ok := state.(*kiro_claude.StreamState); ok && s != nil && !s.Finished {
		for _, out := range kiro_claude.FinishStream(s) {
			if out == "" {
				continue
			}
			if _, err := w.Write([]byte(out)); err != nil {
				result.Error = context.Canceled
				break
			}
			capture.WriteString(out)
			flusher.Flush()
		}
	}

	result.Streamed = true
	result.ResponseStream = capture.String()
	if captureUpstream {
		result.UpstreamResponseBody = capturedUpstreamResponseBody(upstream.Bytes())
	}
	return result
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

	captureUpstream := shouldCaptureUpstreamResponseBody(ctx)
	var upstream limitedByteBuffer

	var state any

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			result.Error = ctx.Err()
			result.Streamed = true
			result.ResponseStream = capture.String()
			if captureUpstream {
				result.UpstreamResponseBody = capturedUpstreamResponseBody(upstream.Bytes())
			}
			return result
		default:
		}

		line := scanner.Bytes()
		capture.Write(line)
		capture.WriteByte('\n')
		if captureUpstream {
			upstream.Append(line)
			upstream.AppendByte('\n')
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
