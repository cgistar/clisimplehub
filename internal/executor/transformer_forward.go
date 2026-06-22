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

	appmiddleware "clisimplehub/internal/middleware"
	"clisimplehub/internal/usage"
)

func (c *ExecutionContext) executeWithTransformer(ctx context.Context, interfaceType string, endpoint *EndpointConfig, req *ForwardRequest, w http.ResponseWriter) *ForwardResult {
	plan, out := c.BuildTransformationPlan(ctx, interfaceType, endpoint, req)
	if out != nil {
		return out
	}
	if plan == nil {
		return &ForwardResult{
			StatusCode: http.StatusBadRequest,
			Error:      fmt.Errorf("nil transformation plan"),
		}
	}

	upstreamReq := &UpstreamRequest{
		Method:              req.Method,
		TargetPath:          plan.TargetPath,
		RawQuery:            plan.RawQuery,
		Headers:             planRequestHeaders(req.Headers, plan),
		Body:                plan.RequestBody,
		IsStreaming:         plan.IsStreaming,
		RequestModel:        planRequestModel(plan),
		OriginalPath:        req.Path,
		TargetInterfaceType: plan.TargetInterfaceType,
		Endpoint:            endpoint,
		Transformer:         plan.Transformer,
		TransformContext:    plan.Context,
	}

	if rt := c.getTransformerRoundTripper(endpoint.Transformer); rt != nil {
		return c.FinalizeTransformation(ctx, w, plan, rt.RoundTrip(ctx, upstreamReq))
	}
	return c.FinalizeTransformation(ctx, w, plan, c.standardTransformerRoundTrip(ctx, upstreamReq))
}

func (c *ExecutionContext) FinalizeTransformation(ctx context.Context, w http.ResponseWriter, plan *TransformationPlan, upstream *UpstreamRoundTripResult) *ForwardResult {
	result := &ForwardResult{}
	debugLogger := DebugLoggerFromContext(ctx)

	if plan == nil {
		result.StatusCode = http.StatusBadRequest
		result.Error = fmt.Errorf("nil transformation plan")
		return result
	}
	if upstream == nil {
		result.StatusCode = http.StatusBadGateway
		result.Error = fmt.Errorf("nil upstream result")
		return result
	}

	result.StatusCode = upstream.StatusCode
	result.Headers = cloneHTTPHeader(upstream.Headers)
	result.TargetURL = strings.TrimSpace(upstream.TargetURL)
	result.TargetHeaders = cloneStringMap(upstream.TargetHeaders)
	result.Tokens = upstream.Tokens

	requestBody := plan.RequestBody
	if len(upstream.RequestBody) > 0 {
		requestBody = upstream.RequestBody
	}
	if len(requestBody) > 0 && shouldCaptureUpstreamRequestBody(ctx) {
		result.UpstreamRequestBody = capturedUpstreamRequestBody(requestBody)
	}

	if debugLogger != nil {
		if result.TargetURL != "" {
			debugLogger.SetMetadata("UpstreamURL", result.TargetURL)
		}
		if len(result.TargetHeaders) > 0 {
			debugLogger.SetSection("UpstreamRequestHeaders", formatHeaderMap(result.TargetHeaders))
		}
		if len(requestBody) > 0 {
			debugLogger.SetSection("UpstreamRequestBody", string(requestBody))
		}
		if result.StatusCode > 0 || len(result.Headers) > 0 {
			debugLogger.SetSection("UpstreamResponseHeaders", formatHTTPHeaders(httpStatusLine(result.StatusCode), result.Headers))
		}
	}

	if upstream.Error != nil && upstream.StatusCode == 0 && upstream.Stream == nil && len(upstream.Body) == 0 {
		result.Error = upstream.Error
		if debugLogger != nil {
			debugLogger.SetSection("UpstreamError", formatErrorChain(upstream.Error))
		}
		return result
	}

	if shouldTreatAsStreaming(upstream, plan) {
		return handleTransformedStreamingResponse(ctx, w, upstream, result, plan)
	}
	return handleTransformedNonStreamingResponse(ctx, upstream, result, plan)
}

func (c *ExecutionContext) standardTransformerRoundTrip(ctx context.Context, req *UpstreamRequest) *UpstreamRoundTripResult {
	result := &UpstreamRoundTripResult{}
	if req == nil || req.Endpoint == nil {
		result.Error = fmt.Errorf("nil upstream request or endpoint")
		return result
	}

	targetURL, err := BuildTargetURL(req.Endpoint.APIURL, req.TargetPath, req.RawQuery)
	if err != nil {
		result.Error = err
		return result
	}
	result.TargetURL = targetURL
	result.RequestBody = append([]byte(nil), req.Body...)

	buildProxyReq := func() (*http.Request, error) {
		proxyReq, err := http.NewRequestWithContext(ctx, req.Method, targetURL, bytes.NewReader(req.Body))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		applyTransformerTargetHeaders(proxyReq, req.Headers, req.TargetInterfaceType, req.TargetPath, req.IsStreaming)
		ApplyAuthForEndpoint(proxyReq, req.Endpoint, req.IsStreaming)
		ApplyEndpointHeaders(proxyReq, req.Endpoint)
		return proxyReq, nil
	}

	proxyReq, err := buildProxyReq()
	if err != nil {
		result.Error = err
		return result
	}
	result.TargetHeaders = sanitizeHeaders(proxyReq.Header)

	clientTimeout := DefaultHTTPTimeout
	if req.IsStreaming {
		clientTimeout = DisableHTTPClientTimeout
	}
	client := NewHTTPClient(req.Endpoint, clientTimeout)
	resp, err := client.Do(proxyReq)
	if err != nil && isRetryableEOF(err) {
		sleepWithContext(ctx, eofBackoffDuration(2))
		if retryReq, buildErr := buildProxyReq(); buildErr == nil {
			resp, err = client.Do(retryReq)
		}
	}
	if err != nil {
		result.Error = fmt.Errorf("request failed: %w", err)
		return result
	}

	result.StatusCode = resp.StatusCode
	result.Headers = resp.Header.Clone()
	result.Stream = resp.Body
	return result
}

func shouldTreatAsStreaming(upstream *UpstreamRoundTripResult, plan *TransformationPlan) bool {
	if upstream == nil || plan == nil || upstream.Stream == nil {
		return false
	}
	if !plan.IsStreaming || upstream.StatusCode != http.StatusOK {
		return false
	}
	if plan.StreamInputMode == StreamInputModeChunk {
		return true
	}
	ct := strings.ToLower(upstream.Headers.Get("Content-Type"))
	if strings.Contains(ct, "text/event-stream") {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(plan.TargetInterfaceType), "gemini") {
		return true
	}
	return sniffLikelyEventStream(upstream)
}

func sniffLikelyEventStream(upstream *UpstreamRoundTripResult) bool {
	if upstream == nil || upstream.Stream == nil {
		return false
	}
	encoding := strings.TrimSpace(upstream.Headers.Get("Content-Encoding"))
	if encoding != "" && !strings.EqualFold(encoding, "identity") {
		return false
	}

	reader := bufio.NewReaderSize(upstream.Stream, 4096)
	sample, err := reader.Peek(512)
	upstream.Stream = wrapReadCloser(reader, upstream.Stream)
	if len(sample) == 0 {
		return false
	}
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return false
	}
	return looksLikeEventStream(sample)
}

func looksLikeEventStream(sample []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(sample))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		return bytes.HasPrefix(line, []byte("event:")) || bytes.HasPrefix(line, []byte("data:")) || bytes.HasPrefix(line, []byte(":"))
	}

	trimmed := bytes.TrimSpace(sample)
	return bytes.HasPrefix(trimmed, []byte("event:")) || bytes.HasPrefix(trimmed, []byte("data:")) || bytes.HasPrefix(trimmed, []byte(":"))
}

func handleTransformedStreamingResponse(ctx context.Context, w http.ResponseWriter, upstream *UpstreamRoundTripResult, result *ForwardResult, plan *TransformationPlan) *ForwardResult {
	if upstream == nil || upstream.Stream == nil {
		return handleTransformedNonStreamingResponse(ctx, upstream, result, plan)
	}
	defer upstream.Stream.Close()

	if shouldSkipResponseTransform(plan) {
		return handleLineStreamingResponse(ctx, w, upstream, result, plan)
	}
	if plan.StreamInputMode == StreamInputModeChunk {
		return handleChunkStreamingResponse(ctx, w, upstream, result, plan)
	}
	return handleLineStreamingResponse(ctx, w, upstream, result, plan)
}

func handleLineStreamingResponse(ctx context.Context, w http.ResponseWriter, upstream *UpstreamRoundTripResult, result *ForwardResult, plan *TransformationPlan) *ForwardResult {
	debugLogger := DebugLoggerFromContext(ctx)

	for key, values := range upstream.Headers {
		switch strings.ToLower(key) {
		case "content-length", "content-encoding", "content-type":
			continue
		}
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	if plan.OutputContentType != "" {
		w.Header().Set("Content-Type", plan.OutputContentType)
	}
	w.WriteHeader(upstream.StatusCode)

	flusher, ok := w.(http.Flusher)
	if !ok {
		result.Error = fmt.Errorf("response writer does not support flushing")
		return result
	}

	reader := getEncodedReader(upstream.Headers.Get("Content-Encoding"), upstream.Stream)
	reader = NewIdleTimeoutReader(reader, upstream.Stream, DefaultStreamReadIdleTimeout)

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var capture strings.Builder
	var rawCapture strings.Builder
	var upstreamCapture limitedByteBuffer

readLoop:
	for scanner.Scan() {
		if ctx.Err() != nil {
			result.Error = ctx.Err()
			break
		}

		line := scanner.Bytes()
		result.ObserveStreamLine(line)
		if plan.Transformer == nil && shouldReverseClaudeMessagesOAuthTools(plan) {
			line = appmiddleware.ReverseClaudeMessagesOAuthToolNamesFromStreamLineForExecutor(line)
		}
		rawCapture.Write(line)
		rawCapture.WriteByte('\n')
		if shouldCaptureUpstreamResponseBody(ctx) {
			upstreamCapture.Append(line)
			upstreamCapture.AppendByte('\n')
		}

		if plan.Transformer == nil {
			if _, err := w.Write(line); err != nil {
				result.Error = fmt.Errorf("downstream write failed: %w", err)
				break
			}
			if _, err := w.Write([]byte("\n")); err != nil {
				result.Error = fmt.Errorf("downstream write failed: %w", err)
				break
			}
			capture.Write(line)
			capture.WriteByte('\n')
			flusher.Flush()
			continue
		}

		outs, err := plan.Transformer.TransformResponseStream(ctx, planRequestModel(plan), plan.Context.OriginalRequestBody, plan.Context.TransformedRequestBody, line, &plan.Context.StreamState)
		if err != nil {
			result.Error = err
			break
		}
		for _, out := range outs {
			if out == "" {
				continue
			}
			if _, err := w.Write([]byte(out)); err != nil {
				result.Error = fmt.Errorf("downstream write failed: %w", err)
				break readLoop
			}
			capture.WriteString(out)
			flusher.Flush()
		}
	}

	if err := scanner.Err(); err != nil && result.Error == nil {
		result.Error = err
	}

	if debugLogger != nil && rawCapture.Len() > 0 {
		debugLogger.SetSection("UpstreamResponseRaw", rawCapture.String())
	}

	result.ResponseStream = capture.String()
	result.Streamed = true
	if shouldCaptureUpstreamResponseBody(ctx) {
		result.UpstreamResponseBody = capturedUpstreamResponseBody(upstreamCapture.Bytes())
	}
	if result.Tokens == nil {
		result.Tokens = extractTokensFromStreamCapture(rawCapture.String())
	}
	normalizeCompletedStreamError(result)
	return result
}

func handleChunkStreamingResponse(ctx context.Context, w http.ResponseWriter, upstream *UpstreamRoundTripResult, result *ForwardResult, plan *TransformationPlan) *ForwardResult {
	debugLogger := DebugLoggerFromContext(ctx)
	isOpenAIImages := isOpenAIImagesPlan(plan)
	captureUpstream := shouldCaptureUpstreamResponseBody(ctx) || debugLogger != nil
	captureResponse := !isOpenAIImages

	for key, values := range upstream.Headers {
		switch strings.ToLower(key) {
		case "content-length", "content-encoding", "content-type":
			continue
		}
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	if plan.OutputContentType != "" {
		w.Header().Set("Content-Type", plan.OutputContentType)
	}
	w.WriteHeader(upstream.StatusCode)

	flusher, ok := w.(http.Flusher)
	if !ok {
		result.Error = fmt.Errorf("response writer does not support flushing")
		return result
	}

	reader := getEncodedReader(upstream.Headers.Get("Content-Encoding"), upstream.Stream)
	reader = NewIdleTimeoutReader(reader, upstream.Stream, DefaultStreamReadIdleTimeout)

	buf := make([]byte, 32*1024)
	var capture strings.Builder
	rawCapture := limitedByteBuffer{}
	if isOpenAIImages {
		rawCapture.max = 1024 * 1024
	}

readLoop:
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			if captureUpstream {
				rawCapture.Append(chunk)
			}

			outs, trErr := transformChunkOrPassthrough(ctx, plan, chunk)
			if trErr != nil {
				result.Error = trErr
				break
			}
			for _, out := range outs {
				if out == "" {
					continue
				}
				if _, writeErr := w.Write([]byte(out)); writeErr != nil {
					result.Error = fmt.Errorf("downstream write failed: %w", writeErr)
					break readLoop
				}
				if captureResponse {
					capture.WriteString(out)
				}
				flusher.Flush()
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

	if plan.Transformer != nil {
		outs, trErr := plan.Transformer.TransformResponseStream(ctx, planRequestModel(plan), plan.Context.OriginalRequestBody, plan.Context.TransformedRequestBody, nil, &plan.Context.StreamState)
		if trErr == nil {
			for _, out := range outs {
				if out == "" {
					continue
				}
				if _, writeErr := w.Write([]byte(out)); writeErr != nil {
					result.Error = fmt.Errorf("downstream write failed: %w", writeErr)
					break
				}
				if captureResponse {
					capture.WriteString(out)
				}
				flusher.Flush()
			}
		}
	}

	rawCaptured := rawCapture.Bytes()
	if debugLogger != nil && len(rawCaptured) > 0 {
		debugLogger.SetSection("UpstreamResponseRaw", bytesToSafeText(rawCaptured))
	}

	if captureResponse {
		result.ResponseStream = capture.String()
	}
	result.Streamed = true
	if shouldCaptureUpstreamResponseBody(ctx) && len(rawCaptured) > 0 {
		result.UpstreamResponseBody = capturedUpstreamResponseBody(rawCaptured)
	}
	normalizeCompletedStreamError(result)
	return result
}

func isOpenAIImagesPlan(plan *TransformationPlan) bool {
	if plan == nil || plan.Context == nil || plan.Context.Metadata == nil {
		return false
	}
	openAIImages, _ := plan.Context.Metadata["openai_images"].(bool)
	return openAIImages
}

func transformChunkOrPassthrough(ctx context.Context, plan *TransformationPlan, chunk []byte) ([]string, error) {
	if plan == nil {
		return nil, fmt.Errorf("nil transformation plan")
	}
	if plan.Transformer == nil {
		return []string{string(chunk)}, nil
	}
	return plan.Transformer.TransformResponseStream(ctx, planRequestModel(plan), plan.Context.OriginalRequestBody, plan.Context.TransformedRequestBody, chunk, &plan.Context.StreamState)
}

func handleTransformedNonStreamingResponse(ctx context.Context, upstream *UpstreamRoundTripResult, result *ForwardResult, plan *TransformationPlan) *ForwardResult {
	debugLogger := DebugLoggerFromContext(ctx)

	body, readErr := readUpstreamBody(upstream)
	if readErr != nil {
		result.Error = fmt.Errorf("failed to read response: %w", readErr)
		return result
	}

	if shouldCaptureUpstreamResponseBody(ctx) {
		result.UpstreamResponseBody = capturedUpstreamResponseBody(body)
	}
	if debugLogger != nil && len(body) > 0 {
		debugLogger.SetSection("UpstreamResponseRaw", bytesToSafeText(body))
	}

	if result.Headers == nil {
		result.Headers = http.Header{}
	}
	if strings.EqualFold(result.Headers.Get("Content-Encoding"), "gzip") {
		result.Headers.Del("Content-Encoding")
		result.Headers.Del("Content-Length")
	}

	if isLikelyHTMLResponse(result.StatusCode, result.Headers.Get("Content-Type"), body) {
		result.StatusCode = http.StatusServiceUnavailable
		result.Error = fmt.Errorf("upstream returned HTML with HTTP 200")
		result.Body = body
		return result
	}

	if plan.Transformer == nil || !shouldTransformResponse(plan, result.StatusCode) {
		if plan.Transformer == nil && shouldReverseClaudeMessagesOAuthTools(plan) {
			body = appmiddleware.ReverseClaudeMessagesOAuthToolNamesForExecutor(body)
		}
		result.Body = body
		if result.Tokens == nil {
			result.Tokens = usageTokens(body)
		}
		return result
	}

	converted, err := plan.Transformer.TransformResponseNonStream(ctx, planRequestModel(plan), plan.Context.OriginalRequestBody, plan.Context.TransformedRequestBody, body, &plan.Context.StreamState)
	if err != nil {
		result.Error = err
		result.Body = body
		return result
	}

	result.Body = converted
	result.Headers.Set("Content-Type", plan.OutputContentType)
	result.Headers.Del("Content-Length")
	result.Headers.Del("Content-Encoding")
	if result.Tokens == nil {
		result.Tokens = usageTokens(converted)
	}
	return result
}

func shouldTransformResponse(plan *TransformationPlan, statusCode int) bool {
	if plan == nil || plan.Transformer == nil {
		return false
	}
	if shouldSkipResponseTransform(plan) {
		return false
	}
	if plan.Context == nil || plan.Context.Metadata == nil {
		return true
	}
	onSuccessOnly, _ := plan.Context.Metadata["response_transform_on_success_only"].(bool)
	if onSuccessOnly {
		return statusCode == http.StatusOK
	}
	return true
}

func shouldSkipResponseTransform(plan *TransformationPlan) bool {
	if plan == nil || plan.Context == nil || plan.Context.Metadata == nil {
		return false
	}
	skip, _ := plan.Context.Metadata["skip_response_transform"].(bool)
	return skip
}

func shouldReverseClaudeMessagesOAuthTools(plan *TransformationPlan) bool {
	if plan == nil || plan.Context == nil || plan.Context.Metadata == nil {
		return false
	}
	enabled, _ := plan.Context.Metadata["claude_messages_oauth_tools"].(bool)
	return enabled
}

func readUpstreamBody(upstream *UpstreamRoundTripResult) ([]byte, error) {
	if upstream == nil {
		return nil, fmt.Errorf("nil upstream result")
	}
	if len(upstream.Body) > 0 {
		return upstream.Body, nil
	}
	if upstream.Stream == nil {
		return upstream.Body, nil
	}
	defer upstream.Stream.Close()
	reader := getEncodedReader(upstream.Headers.Get("Content-Encoding"), upstream.Stream)
	return io.ReadAll(reader)
}

func httpStatusLine(statusCode int) string {
	if statusCode <= 0 {
		return ""
	}
	return fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode))
}

func planRequestModel(plan *TransformationPlan) string {
	if plan == nil || plan.Context == nil || plan.Context.Metadata == nil {
		return ""
	}
	if upstreamModel, _ := plan.Context.Metadata["upstream_model"].(string); strings.TrimSpace(upstreamModel) != "" {
		return upstreamModel
	}
	if requestModel, _ := plan.Context.Metadata["request_model"].(string); strings.TrimSpace(requestModel) != "" {
		return requestModel
	}
	return ""
}

func planRequestHeaders(headers http.Header, plan *TransformationPlan) http.Header {
	if plan != nil && plan.Context != nil && plan.Context.Metadata != nil {
		if targetHeaders, ok := plan.Context.Metadata["target_headers"].(http.Header); ok && targetHeaders != nil {
			return targetHeaders.Clone()
		}
	}
	if headers == nil {
		return http.Header{}
	}
	return headers.Clone()
}

func extractTokensFromStreamCapture(raw string) *TokenUsage {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var latest *TokenUsage
	for scanner.Scan() {
		if tokens := extractStreamTokensFromLine(scanner.Bytes()); tokens != nil {
			latest = tokens
		}
	}
	return latest
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

func usageTokens(body []byte) *TokenUsage {
	stats := usage.ExtractFromResponse(body)
	if stats == nil || stats.IsEmpty() {
		return nil
	}
	return &TokenUsage{
		InputTokens:  stats.InputTokens,
		OutputTokens: stats.OutputTokens,
		TotalTokens:  stats.Total(),
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
		TotalTokens:  stats.Total(),
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

type readerWithCloser struct {
	io.Reader
	io.Closer
}

func wrapReadCloser(reader io.Reader, closer io.Closer) io.ReadCloser {
	if rc, ok := reader.(io.ReadCloser); ok {
		return rc
	}
	return &readerWithCloser{
		Reader: reader,
		Closer: closer,
	}
}

func cloneHTTPHeader(src http.Header) http.Header {
	if src == nil {
		return http.Header{}
	}
	return src.Clone()
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
