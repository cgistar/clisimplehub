package codexplugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	codex "clisimplehub/internal/codex"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/executor"
	"clisimplehub/internal/plugin"
	chat_responses "clisimplehub/internal/transformer/chat/openai/responses"

	"github.com/tidwall/gjson"
)

// Forward preserves the legacy full-lifecycle Codex forwarding path.
// It handles the complete request lifecycle including account selection, authentication,
// HTTP forwarding, and response streaming.
func (s *CodexService) Forward(ctx context.Context, body []byte, model string, isStreaming bool, w http.ResponseWriter, requestPath string) (ret *executor.ForwardResult) {
	startTime := time.Now()
	var usedAccount *codexShared.CodexAccount
	requestModel := model
	if requestModel == "" {
		requestModel = extractModelFromBody(body)
	}

	defer func() {
		pool := codex.GetPool()
		if pool == nil || ret == nil || usedAccount == nil {
			return
		}
		store := pool.Store()
		if store == nil {
			return
		}
		now := time.Now()
		stat := &codexShared.CodexAccountStat{
			AccountID:    usedAccount.ID,
			AccountEmail: usedAccount.Email,
			Model:        requestModel,
			Date:         now.Format("2006-01-02"),
			Hour:         now.Hour(),
			StatusCode:   ret.StatusCode,
			DurationMs:   time.Since(startTime).Milliseconds(),
			RequestPath:  requestPath,
		}
		if ret.Error != nil {
			stat.Status = "error"
			stat.ErrorType = ret.Error.Error()
		} else {
			stat.Status = "success"
		}
		if ret.Tokens != nil {
			stat.InputTokens = ret.Tokens.InputTokens
			stat.OutputTokens = ret.Tokens.OutputTokens
			stat.TotalTokens = ret.Tokens.InputTokens + ret.Tokens.OutputTokens
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = store.InsertStat(ctx, stat)
		}()
	}()

	result := &executor.ForwardResult{}
	debugLogger := executor.DebugLoggerFromContext(ctx)

	// Transformer forward path now receives original request headers via context.
	userAgent := ""
	clientHeaders := executor.ForwardRequestHeadersFromContext(ctx)
	if clientHeaders != nil {
		userAgent = clientHeaders.Get("User-Agent")
	}

	prepared, prepErr := s.prepareCodexRequest(ctx, body, requestPath, userAgent, model, clientHeaders, isStreaming)
	if prepErr != nil {
		return prepErr
	}
	processedBody := prepared.body
	isStreaming = prepared.isStreaming
	requestModel = prepared.requestModel
	chatConv := prepared.chatConv

	pool := codex.GetPool()
	if pool == nil {
		return buildInternalError(fmt.Errorf("codex pool not initialized"))
	}

	// Load config once for the entire request lifecycle
	configPath := pool.ConfigPath()
	config, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil && !os.IsNotExist(err) {
		if debugLogger != nil {
			debugLogger.Log("配置加载失败: %v", err)
		}
		return buildInternalError(fmt.Errorf("config load failed: %v", err))
	}
	if config == nil {
		config = &codexShared.CodexMultiConfig{}
	}

	// Check if any accounts are available before retrying
	mode := pool.Mode()
	firstAccount := pool.Select()
	if firstAccount == nil {
		if debugLogger != nil {
			debugLogger.Log("%s 模式下无可用账号（账号可能已弃用或冷却中）", mode)
		}
		statusCode, errBody := buildNoAccountsError(mode)
		result.StatusCode = statusCode
		result.Body = errBody
		result.Headers = http.Header{"Content-Type": []string{"application/json"}}
		result.Error = fmt.Errorf("no available codex accounts in %s mode", mode)
		return result
	}

	// Retry loop with account rotation
	var lastErr error
	for attempt := 0; attempt < maxRetryAccounts; attempt++ {
		// Check context before each attempt
		select {
		case <-ctx.Done():
			return &executor.ForwardResult{
				StatusCode: 499, // Client Closed Request
				Error:      ctx.Err(),
				Body:       []byte(`{"error":{"type":"request_cancelled","message":"Request cancelled by client"}}`),
				Headers:    http.Header{"Content-Type": []string{"application/json"}},
			}
		default:
		}
		var account *codexShared.CodexAccount
		if attempt == 0 {
			account = firstAccount
		} else {
			account = pool.Select()
			if account == nil {
				if debugLogger != nil {
					debugLogger.Log("第 %d 次尝试：无可用账号", attempt+1)
				}
				break
			}
		}
		usedAccount = account

		if debugLogger != nil {
			debugLogger.Log("尝试账号 %s (attempt %d)", maskToken(account.RefreshToken), attempt+1)
		}

		// Forward to upstream with this account
		fwdResult, retryable := s.forwardWithAccount(ctx, account, processedBody, isStreaming, w, pool, config, prepared.clientHeaders, requestPath, chatConv)
		if fwdResult == nil {
			// Should not happen - forwardWithAccount always returns a result now
			if debugLogger != nil {
				debugLogger.Log("意外的 nil 结果")
			}
			return &executor.ForwardResult{
				StatusCode: http.StatusInternalServerError,
				Error:      fmt.Errorf("unexpected nil result"),
			}
		}

		if fwdResult.StatusCode == http.StatusOK {
			if debugLogger != nil {
				if fwdResult.Streamed {
					debugLogger.Log("流式响应已完成")
				} else {
					debugLogger.Log("请求成功")
				}
			}
			return fwdResult
		}

		if !retryable {
			if debugLogger != nil {
				debugLogger.Log("请求完成（不可重试）")
			}
			return fwdResult
		}

		lastErr = fwdResult.Error
		if debugLogger != nil {
			debugLogger.Log("账号失败，准备重试: %v", lastErr)
		}
	}

	// All accounts failed
	if debugLogger != nil {
		debugLogger.Log("所有账号均失败")
	}

	statusCode, errBody := buildAllFailedError(lastErr)
	result.StatusCode = statusCode
	result.Body = errBody
	result.Headers = http.Header{"Content-Type": []string{"application/json"}}
	result.Error = lastErr
	return result
}

// forwardWithAccount forwards a single request using the specified account.
// Returns (result, retryable) where retryable indicates if another account should be tried.
func (s *CodexService) forwardWithAccount(ctx context.Context, account *codexShared.CodexAccount, body []byte, isStreaming bool, w http.ResponseWriter, pool *codex.CodexAccountPool, config *codexShared.CodexMultiConfig, clientHeaders http.Header, requestPath string, chatConv *chatCompletionsConversion) (*executor.ForwardResult, bool) {
	debugLogger := executor.DebugLoggerFromContext(ctx)
	configPath := pool.ConfigPath()

	// Resolve proxy: appConfig.proxyUrl > account > plugin
	proxyURL := plugin.GetAppProxyURL()
	if proxyURL == "" {
		proxyURL = strings.TrimSpace(account.ProxyUrl)
	}
	if proxyURL == "" {
		proxyURL = pool.ProxyURL()
	}

	if debugLogger != nil {
		debugLogger.SetMetadata("AccountEmail", account.Email)
		debugLogger.SetMetadata("ProxyURL", proxyURL)
	}

	upstreamURL := getCodexUpstreamURL(config, requestPath)

	// Get access token
	authMgr := s.GetOrCreateAuthManager(account.ID, configPath, proxyURL)
	var accessToken string
	var accountID string
	var err error
	for authAttempt := 1; authAttempt <= codexNetworkRetryAttempts; authAttempt++ {
		accessToken, accountID, err = authMgr.GetAccessToken()
		if err == nil {
			break
		}
		if debugLogger != nil {
			debugLogger.Log("认证失败: %v", err)
		}
		errStr := err.Error()
		if strings.Contains(errStr, "refresh_token_reused") {
			pool.MarkFailed(account.ID, codexShared.CodexStatusReused, 0, "refresh_token_reused")
			return &executor.ForwardResult{
				StatusCode: http.StatusUnauthorized,
				Error:      fmt.Errorf("auth failed: %v", err),
			}, true
		} else if strings.Contains(errStr, "invalid_grant") || strings.Contains(errStr, "HTTP 401") || strings.Contains(errStr, "HTTP 403") {
			pool.MarkFailed(account.ID, codexShared.CodexStatusBanned, 0, "auth_failed")
			return &executor.ForwardResult{
				StatusCode: http.StatusUnauthorized,
				Error:      fmt.Errorf("auth failed: %v", err),
			}, true
		}
		if authAttempt < codexNetworkRetryAttempts {
			if debugLogger != nil {
				debugLogger.Log("认证瞬时失败，%d 秒后进行第 %d/%d 次重试", int(codexNetworkRetryDelay/time.Second), authAttempt+1, codexNetworkRetryAttempts)
			}
			if waitErr := waitForRetry(ctx, codexNetworkRetryDelay); waitErr != nil {
				return buildCancelledExecutorError(waitErr), false
			}
			continue
		}
		return buildGatewayExecutorError(
			"auth_transient",
			"Codex authentication temporarily unavailable after retries",
			map[string]any{
				"accountId":         account.AccountID,
				"category":          "auth_transient",
				"reason":            errStr,
				"attempts":          codexNetworkRetryAttempts,
				"retryDelaySeconds": int(codexNetworkRetryDelay / time.Second),
			},
			fmt.Errorf("auth failed: %v", err),
			upstreamURL,
		), false
	}

	// Create HTTP client
	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 0)

	// 派生稳定的 prompt_cache_key / Session_id，让同一个 (账号, 客户端) 组合稳定命中上游 prompt 缓存。
	// 客户端已显式携带 Session_id 或 body.prompt_cache_key 时保持不变。
	body, clientHeaders = ensureCodexPromptCacheKey(body, clientHeaders, account.ID)

	buildRequest := func() (*http.Request, error) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(body))
		if reqErr != nil {
			return nil, reqErr
		}
		applyCodexHeaders(req, accessToken, accountID, isStreaming, config, clientHeaders)
		return req, nil
	}

	var resp *http.Response
	for requestAttempt := 1; requestAttempt <= codexNetworkRetryAttempts; requestAttempt++ {
		req, reqErr := buildRequest()
		if reqErr != nil {
			if debugLogger != nil {
				debugLogger.Log("创建请求失败: %v", reqErr)
			}
			return buildInternalError(reqErr), false
		}

		if debugLogger != nil {
			debugLogger.SetMetadata("UpstreamURL", upstreamURL)
			debugLogger.Log("发送上游请求")
			var headerLines []string
			for k, vals := range req.Header {
				if strings.EqualFold(k, "Authorization") {
					headerLines = append(headerLines, fmt.Sprintf("%s: Bearer ***", k))
					continue
				}
				for _, v := range vals {
					headerLines = append(headerLines, fmt.Sprintf("%s: %s", k, v))
				}
			}
			debugLogger.SetSection("UpstreamRequestHeaders", strings.Join(headerLines, "\n"))
		}

		resp, err = client.Do(req)
		if err == nil {
			break
		}
		if debugLogger != nil {
			debugLogger.Log("上游请求失败: %v", err)
		}
		if requestAttempt < codexNetworkRetryAttempts {
			if debugLogger != nil {
				debugLogger.Log("网络请求失败，%d 秒后进行第 %d/%d 次重试", int(codexNetworkRetryDelay/time.Second), requestAttempt+1, codexNetworkRetryAttempts)
			}
			if waitErr := waitForRetry(ctx, codexNetworkRetryDelay); waitErr != nil {
				return buildCancelledExecutorError(waitErr), false
			}
			continue
		}
		return buildGatewayExecutorError(
			"transport_error",
			"Codex upstream network request failed after retries",
			map[string]any{
				"accountId":         account.AccountID,
				"category":          "transport_error",
				"reason":            err.Error(),
				"attempts":          codexNetworkRetryAttempts,
				"retryDelaySeconds": int(codexNetworkRetryDelay / time.Second),
			},
			fmt.Errorf("upstream error: %v", err),
			upstreamURL,
		), false
	}
	defer resp.Body.Close()

	if debugLogger != nil {
		debugLogger.SetMetadata("StatusCode", fmt.Sprintf("%d", resp.StatusCode))
		debugLogger.Log("收到上游响应: %d", resp.StatusCode)
	}

	// Handle error status codes
	if resp.StatusCode == http.StatusUnauthorized {
		respBody, _ := io.ReadAll(resp.Body)
		if debugLogger != nil {
			debugLogger.Log("401 未授权，尝试强制刷新 token")
		}
		// Try force refresh
		if refreshErr := authMgr.ForceRefresh(); refreshErr == nil {
			newToken, newAccountID, tokenErr := authMgr.GetAccessToken()
			if tokenErr == nil && newToken != "" {
				if debugLogger != nil {
					debugLogger.Log("Token 刷新成功，重试请求")
				}
				retryReq, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(body))
				if reqErr == nil {
					applyCodexHeaders(retryReq, newToken, newAccountID, isStreaming, config, clientHeaders)
					retryResp, retryErr := client.Do(retryReq)
					if retryErr == nil {
						defer retryResp.Body.Close()
						if retryResp.StatusCode == http.StatusOK {
							if debugLogger != nil {
								debugLogger.Log("重试成功")
							}
							return s.handleSuccessResponse(ctx, retryResp, isStreaming, w, pool, account, upstreamURL, chatConv)
						}
					}
				}
			}
		}
		pool.MarkFailed(account.ID, codexShared.CodexStatusBanned, 24*time.Hour, "unauthorized")
		return &executor.ForwardResult{
			StatusCode: resp.StatusCode,
			Body:       respBody,
			Error:      fmt.Errorf("unauthorized"),
		}, true
	}

	if resp.StatusCode == http.StatusForbidden {
		respBody, _ := io.ReadAll(resp.Body)
		if debugLogger != nil {
			debugLogger.Log("403 禁止访问")
		}
		pool.MarkFailed(account.ID, codexShared.CodexStatusBanned, 24*time.Hour, "suspended")
		return &executor.ForwardResult{
			StatusCode: resp.StatusCode,
			Body:       respBody,
			Error:      fmt.Errorf("forbidden"),
		}, true
	}

	if resp.StatusCode == http.StatusPaymentRequired {
		respBody, _ := io.ReadAll(resp.Body)
		if debugLogger != nil {
			debugLogger.Log("402 配额耗尽")
		}
		pool.MarkFailed(account.ID, codexShared.CodexStatusExhausted, 0, "quota_exhausted")
		return &executor.ForwardResult{
			StatusCode: resp.StatusCode,
			Body:       respBody,
			Error:      fmt.Errorf("payment required"),
		}, false
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		respBody, _ := io.ReadAll(resp.Body)
		cooldown := parseCooldownFromBody(respBody)
		if cooldown <= 0 {
			cooldown = parseCooldownDuration(resp)
		}
		if debugLogger != nil {
			debugLogger.Log("429 速率限制，冷却时间: %v", cooldown)
		}
		if snapshot := extractCodexUsageHeaders(resp.Header); snapshot != nil {
			pool.UpdateUsageSnapshot(account.ID, snapshot)
			if debugLogger != nil {
				debugLogger.Log("提取使用率快照: primary=%.1f%%, secondary=%.1f%%",
					snapshot.PrimaryUsedPercent, snapshot.SecondaryUsedPercent)
			}
		}
		pool.MarkFailed(account.ID, codexShared.CodexStatusValid, cooldown, "rate_limit")
		return &executor.ForwardResult{
			StatusCode: resp.StatusCode,
			Body:       respBody,
			Error:      fmt.Errorf("rate limited"),
		}, true
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		if debugLogger != nil {
			debugLogger.Log("非 200 响应: %d", resp.StatusCode)
		}
		return &executor.ForwardResult{
			StatusCode: resp.StatusCode,
			Headers:    resp.Header.Clone(),
			Body:       respBody,
			Error:      fmt.Errorf("upstream returned %d", resp.StatusCode),
		}, false
	}

	// Success
	pool.ReportSuccess(account.ID)
	if debugLogger != nil {
		debugLogger.Log("请求成功")
	}

	return s.handleSuccessResponse(ctx, resp, isStreaming, w, pool, account, upstreamURL, chatConv)
}

// handleSuccessResponse processes a successful (200 OK) response.
func (s *CodexService) handleSuccessResponse(ctx context.Context, resp *http.Response, isStreaming bool, w http.ResponseWriter, pool *codex.CodexAccountPool, account *codexShared.CodexAccount, upstreamURL string, chatConv *chatCompletionsConversion) (*executor.ForwardResult, bool) {
	debugLogger := executor.DebugLoggerFromContext(ctx)

	// Update usage snapshot if available
	if snapshot := extractCodexUsageHeaders(resp.Header); snapshot != nil {
		pool.UpdateUsageSnapshot(account.ID, snapshot)
		if debugLogger != nil {
			debugLogger.Log("提取使用率快照: primary=%.1f%%, secondary=%.1f%%",
				snapshot.PrimaryUsedPercent, snapshot.SecondaryUsedPercent)
		}
	}

	if isStreaming {
		if debugLogger != nil {
			debugLogger.Log("开始流式响应")
		}
		result := s.streamResponseToWriter(ctx, resp, w, upstreamURL, chatConv)
		return result, false
	}

	// Non-streaming response
	// Limit reading to 10MB to prevent OOM
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		if debugLogger != nil {
			debugLogger.Log("读取响应体失败: %v", err)
		}
		return buildInternalError(fmt.Errorf("read response: %v", err)), false
	}

	// Convert Responses API → Chat Completions if needed
	headers := resp.Header.Clone()
	if chatConv != nil && resp.StatusCode == http.StatusOK && len(respBody) > 0 {
		tr := chat_responses.Transformer{}
		requestModel := extractModelFromBody(chatConv.originalBody)
		if converted, convErr := tr.TransformResponseNonStream(ctx, requestModel, chatConv.originalBody, nil, respBody, nil); convErr == nil {
			respBody = converted
			headers.Del("Content-Length")
			headers.Set("Content-Type", "application/json")
			if debugLogger != nil {
				debugLogger.Log("Responses API → Chat Completions 非流式响应转换完成")
			}
		}
	}

	if debugLogger != nil {
		debugLogger.Log("读取响应体成功，长度: %d bytes", len(respBody))
	}

	return &executor.ForwardResult{
		StatusCode: resp.StatusCode,
		Headers:    headers,
		Body:       respBody,
		Tokens:     extractTokensFromBody(respBody),
		TargetURL:  upstreamURL,
	}, false
}

// streamResponseToWriter streams the response to the http.ResponseWriter.
// Returns a ForwardResult with captured stream content for logging.
func (s *CodexService) streamResponseToWriter(ctx context.Context, resp *http.Response, w http.ResponseWriter, upstreamURL string, chatConv *chatCompletionsConversion) *executor.ForwardResult {
	debugLogger := executor.DebugLoggerFromContext(ctx)

	flusher, ok := w.(http.Flusher)
	if !ok {
		if debugLogger != nil {
			debugLogger.Log("ResponseWriter 不支持 Flusher，回退到非流式")
		}
		respBody, _ := io.ReadAll(resp.Body)
		if chatConv != nil && resp.StatusCode == http.StatusOK && len(respBody) > 0 {
			tr := chat_responses.Transformer{}
			requestModel := extractModelFromBody(chatConv.originalBody)
			if converted, convErr := tr.TransformResponseNonStream(ctx, requestModel, chatConv.originalBody, nil, respBody, nil); convErr == nil {
				respBody = converted
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(respBody)
		return &executor.ForwardResult{
			StatusCode: resp.StatusCode,
			Headers:    resp.Header.Clone(),
			Body:       respBody,
			Tokens:     extractTokensFromBody(respBody),
			TargetURL:  upstreamURL,
		}
	}

	// Copy headers; override Content-Type for chat conversion
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	if chatConv != nil {
		w.Header().Set("Content-Type", "text/event-stream")
	}
	w.WriteHeader(resp.StatusCode)

	if debugLogger != nil {
		debugLogger.Log("开始流式传输")
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	eventCount := 0
	var totalBytes int64
	var capture strings.Builder
	var tokens *executor.TokenUsage

	for scanner.Scan() {
		line := scanner.Bytes()

		// Capture for logging
		capture.Write(line)
		capture.WriteByte('\n')

		if chatConv != nil {
			// Convert Responses API SSE → Chat Completions SSE
			tr := chat_responses.Transformer{}
			requestModel := extractModelFromBody(chatConv.originalBody)
			outs, _ := tr.TransformResponseStream(ctx, requestModel, chatConv.originalBody, nil, line, &chatConv.streamState)
			for _, out := range outs {
				if _, err := w.Write([]byte(out)); err != nil {
					if debugLogger != nil {
						debugLogger.Log("写入转换响应失败: %v", err)
					}
					return &executor.ForwardResult{
						StatusCode:     resp.StatusCode,
						Headers:        resp.Header.Clone(),
						ResponseStream: capture.String(),
						Streamed:       true,
						TargetURL:      upstreamURL,
						Error:          err,
					}
				}
				flusher.Flush()
			}
		} else {
			// Original passthrough logic
			if _, err := w.Write(line); err != nil {
				if debugLogger != nil {
					debugLogger.Log("写入响应失败（客户端可能已断开）: %v", err)
				}
				return &executor.ForwardResult{
					StatusCode:     resp.StatusCode,
					Headers:        resp.Header.Clone(),
					ResponseStream: capture.String(),
					Streamed:       true,
					TargetURL:      upstreamURL,
					Error:          err,
				}
			}
			if _, err := w.Write([]byte("\n")); err != nil {
				if debugLogger != nil {
					debugLogger.Log("写入换行符失败: %v", err)
				}
				return &executor.ForwardResult{
					StatusCode:     resp.StatusCode,
					Headers:        resp.Header.Clone(),
					ResponseStream: capture.String(),
					Streamed:       true,
					TargetURL:      upstreamURL,
					Error:          err,
				}
			}
			flusher.Flush()
		}

		eventCount++
		totalBytes += int64(len(line)) + 1

		// Parse usage info for logging
		if streamTokens := tokensFromCodexStreamLine(line); streamTokens != nil {
			tokens = streamTokens
			if debugLogger != nil {
				debugLogger.Log("Token 使用: input=%d, output=%d", streamTokens.InputTokens, streamTokens.OutputTokens)
			}
		}
	}

	var streamErr error
	if err := scanner.Err(); err != nil {
		streamErr = err
		if debugLogger != nil {
			debugLogger.Log("流式传输错误: %v", err)
		}
		// Only write error if context is still valid
		if ctx.Err() == nil {
			_, _ = fmt.Fprintf(w, "data: {\"error\":\"stream read error: %s\"}\n\n", err.Error())
			flusher.Flush()
		}
	}

	// Write [DONE] marker for chat conversion (only after successful stream)
	if chatConv != nil && streamErr == nil {
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}

	if debugLogger != nil {
		debugLogger.Log("流式传输完成: %d 事件, %d bytes", eventCount, totalBytes)
	}

	return &executor.ForwardResult{
		StatusCode:     resp.StatusCode,
		Headers:        resp.Header.Clone(),
		ResponseStream: capture.String(),
		Tokens:         tokens,
		Streamed:       true,
		TargetURL:      upstreamURL,
		Error:          streamErr,
	}
}

// chatCompletionsConversion holds state for Chat Completions ↔ Responses API conversion.
type chatCompletionsConversion struct {
	originalBody []byte
	streamState  any
}

// isChatCompletionsFormat detects if the body is a Chat Completions request (has "messages" but not "input").
func isChatCompletionsFormat(body []byte) bool {
	return gjson.GetBytes(body, "messages").Exists() && !gjson.GetBytes(body, "input").Exists()
}

func applyResolvedModelToBody(body []byte, resolvedModel string) ([]byte, bool) {
	resolvedModel = baseModelName(strings.TrimSpace(resolvedModel))

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, false
	}

	currentModel, _ := payload["model"].(string)
	targetModel := resolvedModel
	if targetModel == "" {
		targetModel = baseModelName(currentModel)
	}
	if strings.TrimSpace(targetModel) == "" {
		return body, false
	}
	if strings.EqualFold(strings.TrimSpace(currentModel), targetModel) {
		return body, false
	}

	payload["model"] = targetModel
	updated, err := json.Marshal(payload)
	if err != nil {
		return body, false
	}
	return updated, true
}
