package kiroplugin

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"clisimplehub/internal/executor"
	kiroapi "clisimplehub/internal/kiro"
	kiro_claude "clisimplehub/internal/kiro/claude"
	kiroShared "clisimplehub/internal/kiro/shared"
	"clisimplehub/internal/transformer"
	"clisimplehub/internal/usage"
)

func buildNoAccountsError(mode string) (int, []byte) {
	var message string
	switch mode {
	case kiroShared.RotationFixed:
		message = "No available Kiro accounts in fixed mode. The active account may be banned, exhausted, or unavailable."
	case kiroShared.RotationFailover:
		message = "No available Kiro accounts in failover mode. All accounts may be banned, exhausted, or unavailable."
	case kiroShared.RotationLoadBalance:
		message = "No available Kiro accounts in load balance mode. All accounts may be banned, exhausted, or unavailable."
	default:
		message = "No available Kiro accounts."
	}

	errJSON, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"type":    "no_available_accounts",
			"message": message,
			"code":    "kiro_account_unavailable",
			"mode":    mode,
		},
	})
	return http.StatusServiceUnavailable, errJSON
}

// Forward preserves the legacy full-lifecycle Kiro forwarding path.
func (s *KiroService) Forward(ctx context.Context, body []byte, model string, isStreaming bool, w http.ResponseWriter, requestPath string) *executor.ForwardResult {
	_ = requestPath // Kiro always uses /generateAssistantResponse, ignore client path
	result := &executor.ForwardResult{}

	// Get debug logger from context
	debugLogger := executor.DebugLoggerFromContext(ctx)
	if debugLogger != nil {
		debugLogger.Log("Kiro 请求开始")
		debugLogger.SetSection("OriginalRequest", string(body))
	}

	tr := s.Transformer()
	if tr == nil {
		result.StatusCode = http.StatusInternalServerError
		result.Error = fmt.Errorf("kiro transformer not initialized")
		return result
	}

	// Check if any accounts are available before proceeding
	pool := kiroapi.GetPool()
	if pool != nil {
		mode := pool.Mode()
		noAvailableAccounts := false
		switch mode {
		case kiroShared.RotationFixed:
			// Fixed mode is governed by the active account only.
			noAvailableAccounts = pool.Select() == nil
		default:
			noAvailableAccounts = pool.AvailableCount() == 0
		}
		if noAvailableAccounts {
			// No accounts available - return structured error immediately
			result.StatusCode = http.StatusServiceUnavailable
			result.Error = fmt.Errorf("no available kiro accounts in %s mode", mode)
			statusCode, errJSON := buildNoAccountsError(mode)
			result.StatusCode = statusCode
			result.Body = errJSON
			result.Headers = http.Header{"Content-Type": []string{"application/json"}}
			return result
		}
	}

	// Web search short-circuit
	if out := s.tryWebSearchShortCircuit(ctx, w, tr, model, body, isStreaming); out != nil {
		return out
	}

	originalBody := body
	upstreamModel := model

	targetPath := tr.TargetPath(isStreaming, upstreamModel)
	if strings.TrimSpace(targetPath) == "" {
		result.StatusCode = http.StatusBadRequest
		result.Error = fmt.Errorf("empty transformer target path")
		return result
	}

	transformedBody, err := tr.TransformRequest(upstreamModel, originalBody, isStreaming)
	if err != nil {
		result.StatusCode = http.StatusBadRequest
		result.Error = err
		return result
	}

	if debugLogger != nil {
		debugLogger.SetSection("TransformedRequest", string(transformedBody))
	}

	baseURL := strings.TrimSpace(tr.GetAPIURL())
	if baseURL == "" {
		baseURL = kiroapi.KiroGenerateURL(tr.GetRegion())
	}
	targetURL, err := executor.BuildTargetURL(baseURL, targetPath, "")
	if err != nil {
		result.Error = err
		return result
	}
	result.TargetURL = targetURL

	requestBody := transformedBody

	buildProxyReq := func() (*http.Request, error) {
		proxyReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(requestBody))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		var src kiroapi.KiroAuthSource = tr
		authApplier := kiroapi.NewAuthApplier(src)
		if err := authApplier.Apply(proxyReq); err != nil {
			return nil, fmt.Errorf("kiro auth failed: %w", err)
		}
		return proxyReq, nil
	}

	proxyReq, err := buildProxyReq()
	if err != nil {
		result.Error = err
		return result
	}

	if debugLogger != nil {
		debugLogger.SetMetadata("UpstreamURL", targetURL)
		debugLogger.Log("发送上游请求")
		// 记录请求头（脱敏）
		var headerLines []string
		for k, vals := range proxyReq.Header {
			if k == "Authorization" {
				if len(vals) > 0 && len(vals[0]) > 8 {
					headerLines = append(headerLines, fmt.Sprintf("%s: Bear****%s", k, vals[0][len(vals[0])-4:]))
				} else {
					headerLines = append(headerLines, fmt.Sprintf("%s: ****", k))
				}
			} else {
				for _, v := range vals {
					headerLines = append(headerLines, fmt.Sprintf("%s: %s", k, v))
				}
			}
		}
		debugLogger.SetSection("UpstreamRequestHeaders", strings.Join(headerLines, "\n"))
	}

	client := executor.NewHTTPClientForcedProxyURL(tr.KiroProxyURL(), 0)
	resp, err := client.Do(proxyReq)
	if err != nil && isRetryableEOF(err) {
		sleepWithContext(ctx, eofBackoffDuration(2))
		if retryReq, buildErr := buildProxyReq(); buildErr == nil {
			resp, err = client.Do(retryReq)
		}
	}
	if err != nil {
		if debugLogger != nil {
			debugLogger.Log("上游请求失败: %v", err)
		}
		result.Error = fmt.Errorf("request failed: %w", err)
		return result
	}

	// Handle 401/403: force-refresh token and retry once
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		authErrBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if debugLogger != nil {
			debugLogger.SetMetadata("StatusCode", fmt.Sprintf("%d", resp.StatusCode))
			debugLogger.Log("收到 %d 响应，尝试刷新 token", resp.StatusCode)
			debugLogger.SetSection("UpstreamResponseBody", string(authErrBody))
		}

		refreshOK := false
		if refreshErr := tr.ForceRefreshKiroToken(); refreshErr == nil {
			refreshOK = true
			time.Sleep(50 * time.Millisecond)
			retryReq, buildErr := buildProxyReq()
			if buildErr != nil {
				result.Error = fmt.Errorf("rebuild request after kiro token refresh: %w", buildErr)
				result.StatusCode = http.StatusInternalServerError
				return result
			}
			resp, err = client.Do(retryReq)
			if err != nil {
				result.Error = fmt.Errorf("request failed after kiro token refresh: %w", err)
				return result
			}
		}
		if !refreshOK {
			handleKiroErrorStatus(resp.StatusCode, authErrBody, tr)
			if out := s.tryFailoverRetry(ctx, tr, originalBody, model, isStreaming, w); out != nil {
				return out
			}
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
		debugLogger.SetMetadata("StatusCode", fmt.Sprintf("%d", resp.StatusCode))
		debugLogger.Log("收到上游响应: %d", resp.StatusCode)
		// 记录响应头
		var respHeaderLines []string
		respHeaderLines = append(respHeaderLines, fmt.Sprintf("Status: %d %s", resp.StatusCode, http.StatusText(resp.StatusCode)))
		for k, vals := range resp.Header {
			for _, v := range vals {
				respHeaderLines = append(respHeaderLines, fmt.Sprintf("%s: %s", k, v))
			}
		}
		debugLogger.SetSection("UpstreamResponseHeaders", strings.Join(respHeaderLines, "\n"))
	}

	// Non-200: pass through raw body; check account status
	if resp.StatusCode != http.StatusOK {
		reader := responseReader(resp)
		if closer, ok := reader.(io.Closer); ok && reader != resp.Body {
			defer closer.Close()
		}
		if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
			result.Headers.Del("Content-Encoding")
			result.Headers.Del("Content-Length")
		}
		errBody, readErr := io.ReadAll(reader)
		if readErr != nil {
			result.Error = fmt.Errorf("failed to read response: %w", readErr)
			return result
		}
		if debugLogger != nil {
			debugLogger.Log("非 200 响应，Body 长度: %d bytes", len(errBody))
			debugLogger.SetSection("UpstreamResponseBody", string(errBody))
			debugLogger.SetRawSection("UpstreamResponseRaw", errBody)
		}
		_, canFailover := handleKiroErrorStatus(resp.StatusCode, errBody, tr)
		if canFailover {
			if out := s.tryFailoverRetry(ctx, tr, originalBody, model, isStreaming, w); out != nil {
				return out
			}
		}
		result.Body = errBody
		return result
	}

	// 200 OK
	if isStreaming {
		return handleKiroStreamingResponse(ctx, w, resp, result, tr, model, originalBody, requestBody)
	}
	return handleKiroNonStreamingResponse(ctx, resp, result, tr, model, originalBody, requestBody)
}

// handleKiroNonStreamingResponse handles Kiro non-streaming 200 responses.
func handleKiroNonStreamingResponse(ctx context.Context, resp *http.Response, result *executor.ForwardResult, tr transformer.Transformer, modelName string, originalRequestRawJSON, requestRawJSON []byte) *executor.ForwardResult {
	debugLogger := executor.DebugLoggerFromContext(ctx)

	reader := responseReader(resp)
	if closer, ok := reader.(io.Closer); ok && reader != resp.Body {
		defer closer.Close()
	}
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		result.Headers.Del("Content-Encoding")
		result.Headers.Del("Content-Length")
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		if debugLogger != nil {
			debugLogger.Log("读取响应体失败: %v", err)
		}
		result.Error = fmt.Errorf("failed to read response: %w", err)
		return result
	}

	if debugLogger != nil {
		debugLogger.Log("读取响应体成功，长度: %d bytes", len(body))
		debugLogger.SetSection("UpstreamResponseBody", string(body))
		debugLogger.SetRawSection("UpstreamResponseRaw", body)
	}

	converted, err := tr.TransformResponseNonStream(ctx, modelName, originalRequestRawJSON, requestRawJSON, body, nil)
	if err != nil {
		if debugLogger != nil {
			debugLogger.Log("转换响应失败: %v", err)
		}
		result.Error = err
		result.Body = body
		return result
	}

	if debugLogger != nil {
		debugLogger.Log("响应转换成功，长度: %d bytes", len(converted))
		debugLogger.SetSection("TransformedResponse", string(converted))
	}

	result.Body = converted
	result.Headers.Set("Content-Type", tr.OutputContentType(false))
	result.Headers.Del("Content-Length")
	result.Headers.Del("Content-Encoding")
	result.Tokens = usageTokensFromBody(converted)
	return result
}

// --- helpers ---

func isRetryableEOF(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "EOF") || strings.Contains(msg, "connection reset")
}

func eofBackoffDuration(attempts int) time.Duration {
	d := time.Duration(1<<uint(attempts-1)) * time.Second
	if d > 4*time.Second {
		d = 4 * time.Second
	}
	return d
}

func sleepWithContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func responseReader(resp *http.Response) io.Reader {
	if resp == nil {
		return nil
	}
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return resp.Body
		}
		return gz
	}
	return resp.Body
}

func usageTokensFromBody(body []byte) *executor.TokenUsage {
	stats := usage.ExtractFromResponse(body)
	if stats == nil || stats.IsEmpty() {
		return nil
	}
	return &executor.TokenUsage{
		InputTokens:  stats.InputTokens,
		OutputTokens: stats.OutputTokens,
		CachedCreate: stats.CachedCreate,
		CachedRead:   stats.CachedRead,
		Reasoning:    stats.Reasoning,
	}
}

func extractModelFromBody(body []byte) string {
	var payload struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &payload)
	return strings.TrimSpace(payload.Model)
}

// kiroModelID returns the Kiro model ID for a given Claude model name (for logging).
func kiroModelID(upstreamModel string) string {
	return kiro_claude.GetKiroModelID(upstreamModel)
}
