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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"clisimplehub/internal/transformer"
	kiroAuth "clisimplehub/internal/transformer/kiro"
	kiro_claude "clisimplehub/internal/transformer/kiro/claude"
	kiroShared "clisimplehub/internal/transformer/kiro/shared"
	"clisimplehub/internal/usage"
)

// kiroAttemptKey is the context key for tracking kiro failover attempts.
type kiroAttemptKey struct{}

func kiroFailoverAttempt(ctx context.Context) int {
	if v, ok := ctx.Value(kiroAttemptKey{}).(int); ok {
		return v
	}
	return 0
}

// tryKiroFailoverRetry attempts to rebind to a different kiro account and retry.
// The caller must have already called handleKiroErrorStatus (or pool.MarkFailed) to persist the failure.
// Returns non-nil if failover was executed; nil if no failover is possible.
func (c *ExecutionContext) tryKiroFailoverRetry(ctx context.Context, tr transformer.Transformer, retryFn func(context.Context) *ForwardResult) *ForwardResult {
	pool := kiroAuth.GetPool()
	if pool == nil || pool.Mode() == kiroShared.RotationFixed {
		return nil
	}
	kt, ok := tr.(*kiro_claude.Transformer)
	if !ok {
		return nil
	}
	if !kt.RebindAccount() {
		return nil
	}
	attempt := kiroFailoverAttempt(ctx)
	maxAttempts := pool.TotalCount()
	if attempt >= maxAttempts {
		return nil
	}
	nextCtx := context.WithValue(ctx, kiroAttemptKey{}, attempt+1)
	fmt.Fprintf(os.Stderr, "Info: kiro failover attempt %d/%d, switching account\n", attempt+1, maxAttempts)
	return retryFn(nextCtx)
}

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

	return c.executeWithResolvedTransformer(ctx, interfaceType, endpoint, req, w, tr)
}

func (c *ExecutionContext) executeWithResolvedTransformer(ctx context.Context, interfaceType string, endpoint *EndpointConfig, req *ForwardRequest, w http.ResponseWriter, tr transformer.Transformer) *ForwardResult {
	result := &ForwardResult{}
	debugLogger := DebugLoggerFromContext(ctx)

	originalBody := req.Body
	requestModel := extractModelFromBody(originalBody)
	upstreamModel := ResolveUpstreamModel(requestModel, endpoint)

	targetInterface := strings.ToLower(strings.TrimSpace(tr.TargetInterfaceType()))
	if targetInterface == "kiro" && strings.EqualFold(strings.TrimSpace(interfaceType), "claude") {
		if out := c.tryHandleKiroWebSearchShortCircuit(ctx, w, interfaceType, endpoint, req, tr, requestModel, upstreamModel, originalBody); out != nil {
			return out
		}
	}

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

	// 记录转换后的请求体
	if debugLogger != nil {
		debugLogger.SetSection("TransformedRequest", string(transformedBody))
	}

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
	// Kiro endpoints don't accept arbitrary client query parameters.
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
		switch targetInterface {
		case "kiro":
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
		default:
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
	if debugLogger != nil {
		debugLogger.SetSection("UpstreamRequestHeaders", formatHeaderMap(result.TargetHeaders))
	}

	client := NewHTTPClient(endpoint, 0)
	if targetInterface == "kiro" {
		proxyURL := ""
		if proxyProvider, ok := tr.(interface{ KiroProxyURL() string }); ok && proxyProvider != nil {
			proxyURL = proxyProvider.KiroProxyURL()
		}
		client = NewHTTPClientForcedProxyURL(proxyURL, 0)
	}
	resp, err := client.Do(proxyReq)
	if err != nil && isRetryableEOF(err) {
		// 对于 AWS/Kiro 的偶发 EOF，做一次轻量级重试（不依赖 fallback 开关）
		// 目标：避免将 529/EOF 暴露给上层客户端（如 Claude Code），提升稳定性。
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
	// when backend returns 401/403, force-refresh token and retry once.
	if targetInterface == "kiro" && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			authErrBody, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if debugLogger != nil {
				debugLogger.SetSection("UpstreamResponseHeaders", formatHTTPHeaders(resp.Status, resp.Header))
				if len(authErrBody) > 0 {
					debugLogger.SetSection("UpstreamResponseBody", bytesToSafeText(authErrBody))
					debugLogger.SetRawSection("UpstreamResponseRaw", authErrBody)
				}
			}
		refreshOK := false
		if refresher, ok := tr.(interface{ ForceRefreshKiroToken() error }); ok && refresher != nil {
			refreshErr := refresher.ForceRefreshKiroToken()
			if debugLogger != nil {
				if refreshErr == nil {
					debugLogger.SetSection("KiroTokenRefresh", "forced refresh: ok; retrying once")
				} else {
					debugLogger.SetSection("KiroTokenRefresh", "forced refresh: failed: "+formatErrorChain(refreshErr))
				}
			}
			if refreshErr == nil {
				refreshOK = true
				time.Sleep(50 * time.Millisecond) // small jitter to avoid immediate retry collisions
				if retryReq, err := buildProxyReq(); err == nil {
					resp, err = client.Do(retryReq)
					if err != nil {
						result.Error = fmt.Errorf("request failed after kiro token refresh: %w", err)
						if debugLogger != nil {
							debugLogger.SetSection("UpstreamError", formatErrorChain(result.Error))
						}
						return result
					}
				}
			}
		}
		// Token refresh failed — try account failover before giving up
		if !refreshOK {
			handleKiroErrorStatus(resp.StatusCode, authErrBody, tr)
			if out := c.tryKiroFailoverRetry(ctx, tr, func(nextCtx context.Context) *ForwardResult {
				return c.executeWithResolvedTransformer(nextCtx, interfaceType, endpoint, req, w, tr)
			}); out != nil {
				return out
			}
			// No failover possible; return error with the captured body
			result.StatusCode = resp.StatusCode
			result.Headers = resp.Header.Clone()
			result.Body = authErrBody
			return result
		}
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	result.Headers = resp.Header.Clone()
	if debugLogger != nil {
		debugLogger.SetSection("UpstreamResponseHeaders", formatHTTPHeaders(resp.Status, resp.Header))
	}

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
			if debugLogger != nil {
				debugLogger.SetSection("UpstreamError", formatErrorChain(result.Error))
			}
			return result
		}

		if shouldCaptureUpstreamResponseBody(ctx) {
			result.UpstreamResponseBody = capturedUpstreamResponseBody(body)
		}

			if debugLogger != nil && len(body) > 0 {
				debugLogger.SetSection("UpstreamResponseBody", bytesToSafeText(body))
				debugLogger.SetRawSection("UpstreamResponseRaw", body)
			}

		// Check and update account status; attempt failover if applicable
		_, canFailover := handleKiroErrorStatus(resp.StatusCode, body, tr)
		if canFailover {
			if out := c.tryKiroFailoverRetry(ctx, tr, func(nextCtx context.Context) *ForwardResult {
				return c.executeWithResolvedTransformer(nextCtx, interfaceType, endpoint, req, w, tr)
			}); out != nil {
				return out
			}
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

// If the Claude request contains exactly one tool and it's `web_search`, do NOT call
// Kiro `/generateAssistantResponse`. Instead, call Kiro MCP (`/mcp`) and synthesize
// an Anthropic-compatible response directly.
func (c *ExecutionContext) tryHandleKiroWebSearchShortCircuit(
	ctx context.Context,
	w http.ResponseWriter,
	interfaceType string,
	endpoint *EndpointConfig,
	req *ForwardRequest,
	tr transformer.Transformer,
	requestModel string,
	upstreamModel string,
	originalBody []byte,
) *ForwardResult {
	result := &ForwardResult{}
	debugLogger := DebugLoggerFromContext(ctx)

	modelFromReq, query, ok := kiro_claude.ParseClaudeWebSearchOnlyRequest(originalBody)
	if !ok {
		return nil
	}

	model := strings.TrimSpace(modelFromReq)
	if model == "" {
		model = strings.TrimSpace(requestModel)
	}
	if model == "" {
		model = strings.TrimSpace(upstreamModel)
	}
	if model == "" {
		model = "claude-sonnet-4"
	}

	inputTokens := kiro_claude.EstimateClaudeInputTokens(originalBody)
	if inputTokens < 1 {
		inputTokens = 1
	}

	if debugLogger != nil {
		debugLogger.SetSection("TransformedRequest", "(web_search short-circuit: routed to Kiro MCP /mcp; no /generateAssistantResponse request)")
	}

	src, ok := tr.(kiroAuth.KiroAuthSource)
	if !ok || src == nil {
		result.StatusCode = http.StatusInternalServerError
		result.Error = fmt.Errorf("kiro auth source not available for transformer=%T", tr)
		return result
	}

	region := "us-east-1"
	if rg, ok := tr.(interface{ GetRegion() string }); ok && rg != nil {
		if v := strings.TrimSpace(rg.GetRegion()); v != "" {
			region = v
		}
	}

	mcpURL := kiroAuth.KiroMCPURL(region)
	result.TargetURL = mcpURL
	if debugLogger != nil {
		debugLogger.SetMetadata("UpstreamURL", mcpURL)
	}

	toolUseID, mcpBody, err := kiro_claude.BuildWebSearchMcpRequest(query)
	if err != nil {
		result.StatusCode = http.StatusBadRequest
		result.Error = err
		return result
	}

	buildMcpReq := func() (*http.Request, error) {
		proxyReq, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, bytes.NewReader(mcpBody))
		if err != nil {
			return nil, err
		}
		authApplier := kiroAuth.NewAuthApplier(src)
		if err := authApplier.Apply(proxyReq); err != nil {
			return nil, err
		}
		return proxyReq, nil
	}

	proxyReq, err := buildMcpReq()
	if err != nil {
		result.StatusCode = http.StatusInternalServerError
		result.Error = fmt.Errorf("build mcp request failed: %w", err)
		return result
	}
	result.TargetHeaders = sanitizeHeaders(proxyReq.Header)
	if debugLogger != nil {
		debugLogger.SetSection("UpstreamRequestHeaders", formatHeaderMap(result.TargetHeaders))
		debugLogger.SetSection("UpstreamRequestBody", string(mcpBody))
	}

	proxyURL := ""
	if proxyProvider, ok := tr.(interface{ KiroProxyURL() string }); ok && proxyProvider != nil {
		proxyURL = proxyProvider.KiroProxyURL()
	}
	client := NewHTTPClientForcedProxyURL(proxyURL, 0)

	resp, err := client.Do(proxyReq)
	if err != nil {
		// MCP failure should not crash the whole request; return "no results"
		if debugLogger != nil {
			debugLogger.SetSection("UpstreamError", formatErrorChain(err))
		}
		return c.writeKiroWebSearchResponse(ctx, w, req, result, model, query, toolUseID, nil, inputTokens, 0)
	}

	// handle 401/403: best-effort refresh and retry once; failover if refresh fails
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		refreshOK := false
		if refresher, ok := tr.(interface{ ForceRefreshKiroToken() error }); ok && refresher != nil {
			if refresher.ForceRefreshKiroToken() == nil {
				refreshOK = true
				time.Sleep(50 * time.Millisecond)
				retryReq, buildErr := buildMcpReq()
				if buildErr == nil {
					proxyReq = retryReq
					resp, err = client.Do(retryReq)
				}
			}
		}
		if !refreshOK {
			// Token refresh failed — try account failover (re-runs executeWithTransformer → re-enters web search)
			handleKiroErrorStatus(resp.StatusCode, nil, tr)
			if out := c.tryKiroFailoverRetry(ctx, tr, func(nextCtx context.Context) *ForwardResult {
				return c.executeWithResolvedTransformer(nextCtx, interfaceType, endpoint, req, w, tr)
			}); out != nil {
				return out
			}
			return c.writeKiroWebSearchResponse(ctx, w, req, result, model, query, toolUseID, nil, inputTokens, 0)
		}
		if err != nil {
			if debugLogger != nil {
				debugLogger.SetSection("UpstreamError", formatErrorChain(err))
			}
			return c.writeKiroWebSearchResponse(ctx, w, req, result, model, query, toolUseID, nil, inputTokens, 0)
		}
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	if debugLogger != nil {
		debugLogger.SetSection("UpstreamResponseHeaders", formatHTTPHeaders(resp.Status, resp.Header))
	}

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if readErr != nil {
		if debugLogger != nil {
			debugLogger.SetSection("UpstreamError", formatErrorChain(readErr))
		}
		return c.writeKiroWebSearchResponse(ctx, w, req, result, model, query, toolUseID, nil, inputTokens, 0)
	}
		if debugLogger != nil && len(body) > 0 {
			debugLogger.SetSection("UpstreamResponseBody", bytesToSafeText(body))
			debugLogger.SetRawSection("UpstreamResponseRaw", body)
		}

	// Failover for 402/403 errors (e.g., exhausted/banned discovered after refresh+retry)
	if resp.StatusCode == 402 || resp.StatusCode == 403 {
		_, canFailover := handleKiroErrorStatus(resp.StatusCode, body, tr)
		if canFailover {
			if out := c.tryKiroFailoverRetry(ctx, tr, func(nextCtx context.Context) *ForwardResult {
				return c.executeWithResolvedTransformer(nextCtx, interfaceType, endpoint, req, w, tr)
			}); out != nil {
				return out
			}
		}
	}

	var results *kiro_claude.WebSearchResults
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		if parsed, err := kiro_claude.ParseKiroMcpWebSearchResults(body); err == nil {
			results = parsed
		}
	}

	// MCP failures degrade to empty results.
	return c.writeKiroWebSearchResponse(ctx, w, req, result, model, query, toolUseID, results, inputTokens, 0)
}

func (c *ExecutionContext) writeKiroWebSearchResponse(
	ctx context.Context,
	w http.ResponseWriter,
	req *ForwardRequest,
	result *ForwardResult,
	model string,
	query string,
	toolUseID string,
	results *kiro_claude.WebSearchResults,
	inputTokens int,
	outputTokens int,
) *ForwardResult {
	_ = ctx

	if req == nil || w == nil || result == nil {
		return result
	}

	result.StatusCode = http.StatusOK

	if req.IsStreaming {
		events, outTokens := kiro_claude.BuildWebSearchSSEEvents(model, query, toolUseID, results, inputTokens)
		if outTokens > 0 {
			outputTokens = outTokens
		}
		var capture strings.Builder
		for _, e := range events {
			capture.WriteString(e)
		}
		result.ResponseStream = capture.String()
		result.Streamed = true
		result.Tokens = &TokenUsage{InputTokens: int64(inputTokens), OutputTokens: int64(outputTokens)}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		for _, e := range events {
			_, _ = io.WriteString(w, e)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if flusher != nil {
			flusher.Flush()
		}
		return result
	}

	body, outTokens, err := kiro_claude.BuildWebSearchNonStreamMessage(model, query, toolUseID, results, inputTokens)
	if err != nil {
		result.StatusCode = http.StatusInternalServerError
		result.Error = err
		return result
	}
	if outTokens > 0 {
		outputTokens = outTokens
	}
	result.Body = body
	result.Tokens = &TokenUsage{InputTokens: int64(inputTokens), OutputTokens: int64(outputTokens)}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	return result
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
	// 获取调试日志记录器
	debugLogger := DebugLoggerFromContext(ctx)
	if debugLogger != nil {
		debugLogger.Log("Kiro 流式响应开始: model=%s", modelName)
	}

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

	// 调试日志：捕获上游原始响应（二进制）
	var debugUpstreamRaw []byte

	// 连续超时容错机制
	const (
		readTimeout            = 30 * time.Second
		maxConsecutiveTimeouts = 3
	)
	consecutiveTimeouts := 0

	// 创建可取消的上下文用于控制读取 goroutine
	readCtx, cancelRead := context.WithCancel(ctx)
	defer cancelRead()

	// 单一读取 goroutine，避免泄漏
	type readResult struct {
		n    int
		err  error
		data []byte
	}
	readChan := make(chan readResult, 1)
	go func() {
		for {
			select {
			case <-readCtx.Done():
				return
			default:
			}

			buf := make([]byte, 32*1024)
			n, err := reader.Read(buf)

			// 复制数据以避免 buf 被重用
			var data []byte
			if n > 0 {
				data = make([]byte, n)
				copy(data, buf[:n])
			}

			select {
			case readChan <- readResult{n: n, err: err, data: data}:
				if err != nil {
					return
				}
			case <-readCtx.Done():
				return
			}
		}
	}()

	// stopAndDrain 确保 timer 在 Reset 前处于干净状态
	stopAndDrain := func(t *time.Timer) {
		if !t.Stop() {
			// timer 已触发，drain 掉可能残留的 tick
			select {
			case <-t.C:
			default:
			}
		}
	}

	// 使用可重置的 timer 避免重复分配
	timer := time.NewTimer(readTimeout)
	defer timer.Stop()

readLoop:
	for {
		// ====== WAIT_UPSTREAM: 只在这里计 read timeout ======
		// 每次循环开始前，确保 timer 干净并重新武装
		stopAndDrain(timer)
		timer.Reset(readTimeout)

		select {
		case <-ctx.Done():
			stopAndDrain(timer)
			result.Error = ctx.Err()
			result.Streamed = true
			result.ResponseStream = capture.String()
			if captureUpstream {
				result.UpstreamResponseBody = capturedUpstreamResponseBody(upstream.Bytes())
			}
			return result

		case <-timer.C:
			// 上游读取超时
			consecutiveTimeouts++
			if consecutiveTimeouts <= maxConsecutiveTimeouts {
				// 容忍超时，继续等待
				if debugLogger != nil {
					debugLogger.Log("Kiro 流式读取超时 %d/%d，继续等待", consecutiveTimeouts, maxConsecutiveTimeouts)
				}
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

		case res := <-readChan:
			// 收到上游数据，立即解除 timer 武装
			stopAndDrain(timer)

			n, err := res.n, res.err

			// 成功读取，重置超时计数器
			consecutiveTimeouts = 0

			// ====== PROCESS_AND_WRITE: 这里不触发 read timeout ======
			// 先处理数据（即使有错误，也要处理已读取的数据）
			if n > 0 {
				chunk := res.data
				if captureUpstream {
					upstream.Append(chunk)
				}
				// 调试日志：追加原始二进制数据
				if debugLogger != nil {
					debugUpstreamRaw = append(debugUpstreamRaw, chunk...)
				}

				// 在处理前快速检查 ctx，提高取消响应性
				if err := ctx.Err(); err != nil {
					result.Error = err
					break readLoop
				}

				outs, trErr := tr.TransformResponseStream(ctx, modelName, originalRequestRawJSON, requestRawJSON, chunk, &state)
				if trErr != nil {
					result.Error = trErr
					break readLoop
				}
				for _, out := range outs {
					if out == "" {
						continue
					}
					if _, err := w.Write([]byte(out)); err != nil {
						result.Error = context.Canceled
						break readLoop
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

			// 处理完数据后，再检查错误
			if err == io.EOF {
				break readLoop
			}
			if err != nil {
				result.Error = err
				break readLoop
			}
			// 回到循环顶部，重新进入 WAIT_UPSTREAM
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

		// 记录调试日志
		if debugLogger != nil {
			debugLogger.Log("Kiro 流式响应完成")
		}
	}

	// 记录上游原始响应（二进制，base64编码）
	if debugLogger != nil && len(debugUpstreamRaw) > 0 {
		debugLogger.SetRawSection("UpstreamResponseRaw", debugUpstreamRaw)
		// 同时保存可读文本版本，便于日志分析（不截断）。
		// debugLogger.SetSection("UpstreamResponseBody", bytesToSafeText(debugUpstreamRaw))
	}

	// 记录转换后的 Claude SSE 响应
	capturedSSE := capture.String()
	if debugLogger != nil {
		debugLogger.Log("Kiro capture长度: %d", len(capturedSSE))
		if capturedSSE != "" {
			debugLogger.SetSection("TransformedResponse", capturedSSE)
		}
	}

	result.Streamed = true
	result.ResponseStream = capturedSSE
	if captureUpstream {
		result.UpstreamResponseBody = capturedUpstreamResponseBody(upstream.Bytes())
	}
	return result
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

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var capture strings.Builder

	captureUpstream := shouldCaptureUpstreamResponseBody(ctx)
	var upstream limitedByteBuffer

	// 调试日志：捕获上游原始响应
	var debugUpstreamRaw strings.Builder

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
				result.Error = context.Canceled
				break
			}
			flusher.Flush()
		}
	}

	if err := scanner.Err(); err != nil {
		result.Error = err
	}

	// 记录上游原始响应
	if debugLogger != nil && debugUpstreamRaw.Len() > 0 {
		debugLogger.SetSection("UpstreamResponseRaw", debugUpstreamRaw.String())
		// 同时保存到 UpstreamResponseBody，保持字段一致（不截断）。
		// debugLogger.SetSection("UpstreamResponseBody", debugUpstreamRaw.String())
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
		targetInterface := strings.ToLower(strings.TrimSpace(tr.TargetInterfaceType()))
		if targetInterface == "kiro" {
			debugLogger.SetRawSection("UpstreamResponseRaw", body)
		} else {
			debugLogger.SetSection("UpstreamResponseRaw", string(body))
		}
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

// handleKiroErrorStatus checks and updates Kiro account status on HTTP errors.
// Returns the detected status and whether failover should be attempted.
func handleKiroErrorStatus(statusCode int, body []byte, tr transformer.Transformer) (kiroShared.KiroAccountStatus, bool) {
	if tr == nil {
		return "", false
	}

	bodyStr := string(body)
	var targetStatus kiroShared.KiroAccountStatus
	var errorType string

	if statusCode == 402 && kiroShared.IsMonthlyRequestCountError(bodyStr) {
		targetStatus = kiroShared.KiroStatusExhausted
		errorType = "MONTHLY_REQUEST_COUNT"
	} else if statusCode == 403 && kiroShared.IsTemporarilySuspendedError(bodyStr) {
		targetStatus = kiroShared.KiroStatusBanned
		errorType = "TEMPORARILY_SUSPENDED"
	} else if statusCode == 401 {
		targetStatus = kiroShared.KiroStatusUnknown
		errorType = "AUTH_FAILURE"
	} else {
		return "", false
	}

	// Determine refreshToken from the transformer's currently bound account
	refreshToken := ""
	if kt, ok := tr.(*kiro_claude.Transformer); ok {
		refreshToken = kt.CurrentAccountRefreshToken()
	}

	// Fallback: determine from kiro.json active account
	if refreshToken == "" {
		configPath := strings.TrimSpace(os.Getenv("CONFIG_PATH"))
		if configPath == "" {
			if v, err := kiro_claude.GetConfig("configPath"); err == nil {
				configPath = strings.TrimSpace(v)
			}
		}
		multiConfigPath := ""
		if configPath != "" {
			multiConfigPath = filepath.Join(filepath.Dir(kiroShared.ExpandTilde(configPath)), filepath.Base(kiroShared.GetDefaultKiroMultiConfigPath()))
		}
		if multiConfigPath == "" {
			multiConfigPath = kiroShared.GetDefaultKiroMultiConfigPath()
		}
		if multiConfig, err := kiroShared.LoadKiroMultiConfig(multiConfigPath); err == nil && multiConfig != nil {
			refreshToken = strings.TrimSpace(multiConfig.ActiveRefreshToken)
		}
	}

	if refreshToken == "" {
		fmt.Fprintf(os.Stderr, "Warning: cannot determine refreshToken to update account status\n")
		return targetStatus, true
	}

	// Update via pool if available
	pool := kiroAuth.GetPool()
	if pool != nil {
		if err := pool.MarkFailed(refreshToken, targetStatus); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to persist kiro account status: %v\n", err)
		}
		fmt.Fprintf(os.Stderr, "Info: pool marked account %s as %s due to %s\n", refreshToken[:min(8, len(refreshToken))], targetStatus, errorType)
		return targetStatus, true
	}

	// Fallback: direct update to kiro.json
	configPath := strings.TrimSpace(os.Getenv("CONFIG_PATH"))
	if configPath == "" {
		if v, err := kiro_claude.GetConfig("configPath"); err == nil {
			configPath = strings.TrimSpace(v)
		}
	}
	multiConfigPath := ""
	if configPath != "" {
		multiConfigPath = filepath.Join(filepath.Dir(kiroShared.ExpandTilde(configPath)), filepath.Base(kiroShared.GetDefaultKiroMultiConfigPath()))
	}
	if multiConfigPath == "" {
		multiConfigPath = kiroShared.GetDefaultKiroMultiConfigPath()
	}

	if err := kiroShared.UpdateAccountStatusByRefreshToken(refreshToken, targetStatus, multiConfigPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to update account status to %s (%s): %v\n", targetStatus, errorType, err)
	} else {
		fmt.Fprintf(os.Stderr, "Info: updated account status to %s due to %s error\n", targetStatus, errorType)
	}
	return targetStatus, true
}