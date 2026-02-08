package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"clisimplehub/internal/transformer"
	grokAuth "clisimplehub/internal/transformer/grok"
	grokShared "clisimplehub/internal/transformer/grok/shared"
)

type grokAttemptKey struct{}

func grokFailoverAttempt(ctx context.Context) int {
	if v, ok := ctx.Value(grokAttemptKey{}).(int); ok {
		return v
	}
	return 0
}

func (c *ExecutionContext) tryGrokFailoverRetry(ctx context.Context, tr transformer.Transformer, retryFn func(context.Context) *ForwardResult) *ForwardResult {
	pool := grokAuth.GetPool()
	if pool == nil || pool.Mode() == grokShared.RotationFixed {
		return nil
	}
	rebinder, ok := tr.(interface{ RebindAccount() bool })
	if !ok || !rebinder.RebindAccount() {
		return nil
	}
	attempt := grokFailoverAttempt(ctx)
	maxAttempts := pool.TotalCount()
	if attempt >= maxAttempts {
		return nil
	}
	nextCtx := context.WithValue(ctx, grokAttemptKey{}, attempt+1)
	fmt.Fprintf(os.Stderr, "Info: grok failover attempt %d/%d, switching account\n", attempt+1, maxAttempts)
	return retryFn(nextCtx)
}

func handleGrokErrorStatus(statusCode int, tr transformer.Transformer) (grokShared.GrokAccountStatus, bool) {
	if tr == nil {
		return "", false
	}
	var status grokShared.GrokAccountStatus
	if grokShared.IsRateLimitError(statusCode) {
		status = grokShared.GrokStatusCooling
	} else if grokShared.IsAuthError(statusCode) {
		status = grokShared.GrokStatusInvalid
	} else {
		return "", false
	}
	if t, ok := tr.(interface{ CurrentAccountSsoToken() string }); ok {
		token := t.CurrentAccountSsoToken()
		if token != "" {
			pool := grokAuth.GetPool()
			if pool != nil {
				_ = pool.MarkFailed(token, status)
				fmt.Fprintf(os.Stderr, "Info: grok pool marked account %s… as %s (HTTP %d)\n", token[:min(8, len(token))], status, statusCode)
			}
		}
	}
	return status, true
}

// ExecuteGrokTransformer handles the complete grok forwarding lifecycle.
// Grok uses its own execution path independent of the generic transformer pipeline.
func (c *ExecutionContext) ExecuteGrokTransformer(ctx context.Context, endpoint *EndpointConfig, req *ForwardRequest, w http.ResponseWriter, tr transformer.Transformer) *ForwardResult {
	result := &ForwardResult{}
	debugLogger := DebugLoggerFromContext(ctx)

	originalBody := req.Body
	requestModel := extractModelFromBody(originalBody)
	upstreamModel := ResolveUpstreamModel(requestModel, endpoint)

	targetPath := tr.TargetPath(req.IsStreaming, upstreamModel)
	if strings.TrimSpace(targetPath) == "" {
		c.DebugLog(ctx, 3, fmt.Sprintf("[Grok] 目标路径为空: endpoint=%s transformer=%q", endpoint.Name, endpoint.Transformer))
		result.StatusCode = http.StatusBadRequest
		result.Error = fmt.Errorf("empty transformer target path: transformer=%q", endpoint.Transformer)
		return result
	}

	transformedBody, err := tr.TransformRequest(upstreamModel, originalBody, req.IsStreaming)
	if err != nil {
		c.DebugLog(ctx, 3, fmt.Sprintf("[Grok] 请求转换失败: endpoint=%s transformer=%q err=%v", endpoint.Name, endpoint.Transformer, err))
		result.StatusCode = http.StatusBadRequest
		result.Error = err
		return result
	}

	if debugLogger != nil {
		debugLogger.SetSection("TransformedRequest", string(transformedBody))
	}

	// Build target URL — grok transformer provides its own base URL
	baseURL := endpoint.APIURL
	if urlProvider, ok := tr.(interface{ GetAPIURL() string }); ok && urlProvider != nil {
		if apiURL := strings.TrimSpace(urlProvider.GetAPIURL()); apiURL != "" {
			baseURL = apiURL
		}
	}
	targetURL, err := BuildTargetURL(baseURL, targetPath, "")
	if err != nil {
		c.DebugLog(ctx, 3, fmt.Sprintf("[Grok] 目标URL构造失败: endpoint=%s apiUrl=%s path=%s err=%v", endpoint.Name, baseURL, targetPath, err))
		result.Error = err
		return result
	}
	result.TargetURL = targetURL

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
	c.DebugLog(ctx, 1, fmt.Sprintf("转发: endpoint=%s interface=chat transformer=%q target=%s model(client=%q upstream=%q final=%q mapped=%v) clientStream=%v", endpoint.Name, endpoint.Transformer, targetURL, requestModel, upstreamModel, finalModel, modelMapped, req.IsStreaming))

	buildProxyReq := func() (*http.Request, error) {
		proxyReq, err := http.NewRequestWithContext(ctx, req.Method, targetURL, bytes.NewReader(requestBody))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		src, ok := tr.(grokAuth.GrokAuthSource)
		if !ok || src == nil {
			return nil, fmt.Errorf("grok auth source not available for transformer=%T", tr)
		}
		authApplier := grokAuth.NewAuthApplier(src)
		if err := authApplier.Apply(proxyReq); err != nil {
			return nil, fmt.Errorf("grok auth failed: %w", err)
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

	proxyURL := ""
	if proxyProvider, ok := tr.(interface{ GrokProxyURL() string }); ok && proxyProvider != nil {
		proxyURL = proxyProvider.GrokProxyURL()
	}
	client := NewHTTPClientForcedProxyURL(proxyURL, 0)

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

	// Grok: on 401/403/429, mark account failed and attempt failover.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		reader := getResponseReader(resp)
		if closer, ok := reader.(io.Closer); ok && reader != resp.Body {
			defer closer.Close()
		}
		errBody, _ := io.ReadAll(reader)
		_ = resp.Body.Close()
		if debugLogger != nil {
			debugLogger.SetSection("UpstreamResponseHeaders", formatHTTPHeaders(resp.Status, resp.Header))
			if len(errBody) > 0 {
				debugLogger.SetSection("UpstreamResponseBody", bytesToSafeText(errBody))
				debugLogger.SetRawSection("UpstreamResponseRaw", errBody)
			}
		}
		handleGrokErrorStatus(resp.StatusCode, tr)
		if out := c.tryGrokFailoverRetry(ctx, tr, func(nextCtx context.Context) *ForwardResult {
			return c.ExecuteGrokTransformer(nextCtx, endpoint, req, w, tr)
		}); out != nil {
			return out
		}
		result.StatusCode = resp.StatusCode
		result.Headers = resp.Header.Clone()
		result.Body = errBody
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
		c.DebugLog(ctx, level, fmt.Sprintf("[Grok] 响应片段: endpoint=%s status=%d body=%s", endpoint.Name, out.StatusCode, truncateForLog(out.Body, 2048)))
	}
	return out
}
