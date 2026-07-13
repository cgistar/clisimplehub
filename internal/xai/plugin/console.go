package xaiplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"clisimplehub/internal/executor"
	xai "clisimplehub/internal/xai"
	xaiBackend "clisimplehub/internal/xai/backend"
	xaiShared "clisimplehub/internal/xai/shared"
)

// consoleOutFmt 控制上游 SSE 如何转回客户端协议。
type consoleOutFmt int

const (
	consoleOutChat consoleOutFmt = iota
	consoleOutResponses
	consoleOutAnthropic
)

// HandleConsoleChatCompletions OpenAI chat.completions 统一入口。
// - 文本模型 → console.x.ai/v1/responses
// - 图片模型（含 lite / image_config）→ grok.com Imagine reverse
// - 视频模型 → 异步视频任务说明 / 创建
func (s *XaiService) HandleConsoleChatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "failed to read body"})
		return
	}
	_ = r.Body.Close()

	var peek chatPeek
	_ = json.Unmarshal(body, &peek)
	model := strings.TrimSpace(peek.Model)

	// 图片模型 / 显式 image_config → 生图（grok.com reverse，不走 console.x.ai）
	if xaiBackend.IsConsoleImageModel(model) || peek.ImageConfig != nil {
		s.handleConsoleChatAsImage(w, r, body, &peek)
		return
	}
	// 视频模型
	if xaiBackend.IsConsoleVideoModel(model) || peek.VideoConfig != nil {
		s.handleConsoleChatAsVideo(w, r, body, &peek)
		return
	}

	payload, requestModel, stream, err := xaiBackend.BuildConsolePayload(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if !stream && strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		stream = true
		payload["stream"] = true
	}
	s.handleConsoleCommon(w, r, payload, requestModel, stream, consoleOutChat)
}

type chatPeek struct {
	Model       string `json:"model"`
	Stream      *bool  `json:"stream"`
	ImageConfig *struct {
		N              int    `json:"n"`
		Size           string `json:"size"`
		ResponseFormat string `json:"response_format"`
	} `json:"image_config"`
	VideoConfig *struct {
		Seconds int    `json:"seconds"`
		Size    string `json:"size"`
	} `json:"video_config"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

func extractLastUserText(messages []struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		var s string
		if json.Unmarshal(messages[i].Content, &s) == nil && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
		// content blocks
		var blocks []map[string]any
		if json.Unmarshal(messages[i].Content, &blocks) == nil {
			var b strings.Builder
			for _, bl := range blocks {
				if t, _ := bl["type"].(string); t == "text" || t == "" {
					if tx, ok := bl["text"].(string); ok {
						b.WriteString(tx)
					}
				}
			}
			if t := strings.TrimSpace(b.String()); t != "" {
				return t
			}
		}
	}
	return ""
}

func (s *XaiService) handleConsoleChatAsImage(w http.ResponseWriter, r *http.Request, body []byte, peek *chatPeek) {
	if peek == nil {
		peek = &chatPeek{}
		_ = json.Unmarshal(body, peek)
	}
	model := strings.TrimSpace(peek.Model)
	if model == "" {
		model = "grok-imagine-image"
	}
	prompt := extractLastUserText(peek.Messages)
	if prompt == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "prompt required in last user message"})
		return
	}
	n, size, fmtResp := 1, "1024x1024", "url"
	if peek.ImageConfig != nil {
		if peek.ImageConfig.N > 0 {
			n = peek.ImageConfig.N
		}
		if peek.ImageConfig.Size != "" {
			size = peek.ImageConfig.Size
		}
		if peek.ImageConfig.ResponseFormat != "" {
			fmtResp = peek.ImageConfig.ResponseFormat
		}
	}
	stream := false
	if peek.Stream != nil {
		stream = *peek.Stream
	}
	if !stream && strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		stream = true
	}

	s.withConsoleAccount(w, r, func(ctx context.Context, pool *xai.XaiAccountPool, acc *xaiShared.XaiAccount, proxyURL string, dynamicStatsig bool) (int, bool, error) {
		// 全部图片模型统一走 wss://grok.com/ws/imagine/listen：
		// - lite / 默认：enable_pro=false
		// - pro / quality：enable_pro=true
		// app-chat Drawing 路径易被 anti-bot 拦截，不作为 chat 主路径。
		enablePro := xaiBackend.IsConsoleImageProModel(model)
		out, err := xaiBackend.ImagineGenerate(ctx, acc.SSO, proxyURL, prompt, size, fmtResp, n, dynamicStatsig, enablePro)
		if err != nil {
			status, retryable := classifyConsoleMediaErr(pool, acc, err)
			return status, retryable, err
		}
		pool.ReportSuccess(acc.ID)
		// chat_format：包装成 chat.completions（markdown 图片）
		if stream {
			writeChatImageStream(w, model, out)
			return 0, false, nil
		}
		writeJSON(w, http.StatusOK, imagesToChatCompletion(model, out))
		return 0, false, nil
	})
}

func (s *XaiService) handleConsoleChatAsVideo(w http.ResponseWriter, r *http.Request, body []byte, peek *chatPeek) {
	if peek == nil {
		peek = &chatPeek{}
		_ = json.Unmarshal(body, peek)
	}
	// 视频仍走异步 POST /videos 语义：在 chat 入口创建 job 并返回 job id 文本
	prompt := extractLastUserText(peek.Messages)
	if prompt == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "prompt required"})
		return
	}
	// 复用 create handler 逻辑：构造最小 JSON 再内部调用
	seconds, size := 6, "720x1280"
	if peek.VideoConfig != nil {
		if peek.VideoConfig.Seconds > 0 {
			seconds = peek.VideoConfig.Seconds
		}
		if peek.VideoConfig.Size != "" {
			size = peek.VideoConfig.Size
		}
	}
	model := strings.TrimSpace(peek.Model)
	if model == "" {
		model = "grok-imagine-video"
	}
	// 注入到 Create 流程：直接调 HandleConsoleVideosCreate 不方便，内联创建
	jobBody, _ := json.Marshal(map[string]any{
		"model":   model,
		"prompt":  prompt,
		"seconds": seconds,
		"size":    size,
	})
	// 伪造请求体
	nr := r.Clone(r.Context())
	nr.Body = io.NopCloser(bytes.NewReader(jobBody))
	nr.Header = r.Header.Clone()
	nr.Header.Set("Content-Type", "application/json")
	// 用缓冲 ResponseWriter 拿 job JSON
	bw := &bufResponse{h: http.Header{}}
	s.HandleConsoleVideosCreate(bw, nr)
	if bw.code == 0 {
		bw.code = 200
	}
	if bw.code != 200 {
		for k, vv := range bw.h {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(bw.code)
		_, _ = w.Write(bw.buf.Bytes())
		return
	}
	var job map[string]any
	_ = json.Unmarshal(bw.buf.Bytes(), &job)
	id, _ := job["id"].(string)
	msg := fmt.Sprintf("video job created: %s (poll GET /xai/console/v1/videos/%s , content GET /xai/console/v1/videos/%s/content)", id, id, id)
	stream := peek.Stream != nil && *peek.Stream
	if stream {
		writeChatTextStream(w, model, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      "chatcmpl-video-job",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": msg,
			},
			"finish_reason": "stop",
		}},
		"video_job": job,
	})
}

type bufResponse struct {
	h    http.Header
	buf  bytes.Buffer
	code int
}

func (b *bufResponse) Header() http.Header         { return b.h }
func (b *bufResponse) Write(p []byte) (int, error) { return b.buf.Write(p) }
func (b *bufResponse) WriteHeader(statusCode int)  { b.code = statusCode }

func imagesToChatCompletion(model string, images map[string]any) map[string]any {
	var parts []string
	if data, ok := images["data"].([]map[string]any); ok {
		for _, d := range data {
			if u, ok := d["url"].(string); ok && u != "" {
				parts = append(parts, "![]("+u+")")
			} else if b64, ok := d["b64_json"].(string); ok && b64 != "" {
				parts = append(parts, "![image](data:image/jpeg;base64,"+b64+")")
			}
		}
	} else if arr, ok := images["data"].([]any); ok {
		for _, it := range arr {
			d, _ := it.(map[string]any)
			if d == nil {
				continue
			}
			if u, ok := d["url"].(string); ok && u != "" {
				parts = append(parts, "![]("+u+")")
			}
		}
	}
	content := strings.Join(parts, "\n\n")
	if content == "" {
		content = "(no image)"
	}
	return map[string]any{
		"id":      "chatcmpl-image",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": content,
			},
			"finish_reason": "stop",
		}},
		"images": images,
	}
}

func writeChatImageStream(w http.ResponseWriter, model string, images map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	chat := imagesToChatCompletion(model, images)
	// role
	writeSSEData(w, map[string]any{
		"id": "chatcmpl-image", "object": "chat.completion.chunk", "model": model,
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant"}}},
	})
	if choices, ok := chat["choices"].([]map[string]any); ok && len(choices) > 0 {
		if msg, ok := choices[0]["message"].(map[string]any); ok {
			if c, ok := msg["content"].(string); ok {
				writeSSEData(w, map[string]any{
					"id": "chatcmpl-image", "object": "chat.completion.chunk", "model": model,
					"choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": c}}},
				})
			}
		}
	}
	writeSSEData(w, map[string]any{
		"id": "chatcmpl-image", "object": "chat.completion.chunk", "model": model,
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
	})
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func writeChatTextStream(w http.ResponseWriter, model, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	writeSSEData(w, map[string]any{
		"id": "chatcmpl-text", "object": "chat.completion.chunk", "model": model,
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant"}}},
	})
	writeSSEData(w, map[string]any{
		"id": "chatcmpl-text", "object": "chat.completion.chunk", "model": model,
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": text}}},
	})
	writeSSEData(w, map[string]any{
		"id": "chatcmpl-text", "object": "chat.completion.chunk", "model": model,
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
	})
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func writeSSEData(w http.ResponseWriter, obj any) {
	b, _ := json.Marshal(obj)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// HandleConsoleResponses OpenAI Responses → console.x.ai/v1/responses。
func (s *XaiService) HandleConsoleResponses(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "failed to read body"})
		return
	}
	_ = r.Body.Close()
	payload, requestModel, stream, err := xaiBackend.BuildConsolePayloadFromResponses(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if !stream && strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		stream = true
		payload["stream"] = true
	}
	s.handleConsoleCommon(w, r, payload, requestModel, stream, consoleOutResponses)
}

// HandleConsoleMessages Anthropic Messages → console.x.ai。
func (s *XaiService) HandleConsoleMessages(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "failed to read body"})
		return
	}
	_ = r.Body.Close()
	payload, requestModel, stream, err := xaiBackend.BuildConsolePayloadFromAnthropic(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if !stream && strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		stream = true
		payload["stream"] = true
	}
	s.handleConsoleCommon(w, r, payload, requestModel, stream, consoleOutAnthropic)
}

func (s *XaiService) handleConsoleCommon(
	w http.ResponseWriter,
	r *http.Request,
	payload map[string]any,
	requestModel string,
	stream bool,
	outFmt consoleOutFmt,
) {
	pool := xai.GetPool()
	if pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]any{"type": "no_accounts", "message": "xai pool not initialized"},
		})
		return
	}
	mode := pool.Mode()
	first := pool.SelectConsole()
	if first == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]any{
				"type":    "no_accounts",
				"message": "no available basic SSO accounts for /xai/console",
				"mode":    mode,
			},
		})
		return
	}

	ctx := r.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	excluded := make(map[string]bool)
	var lastErr error
	var lastStatus int
	var lastBody []byte

	for attempt := 0; attempt < maxRetryAccounts; attempt++ {
		select {
		case <-ctx.Done():
			writeJSON(w, 499, map[string]any{"error": "request cancelled"})
			return
		default:
		}

		var account *xaiShared.XaiAccount
		if attempt == 0 {
			account = first
		} else {
			if mode == xaiShared.RotationFixed {
				break
			}
			account = pool.SelectConsoleExcluding(excluded)
			if account == nil {
				break
			}
		}

		status, respBody, retryable, err := s.consoleRoundTrip(ctx, pool, account, payload, stream, requestModel, outFmt, w)
		if err == nil && status >= 200 && status < 300 {
			pool.ReportSuccess(account.ID)
			if stream {
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write(respBody)
			return
		}

		lastErr = err
		lastStatus = status
		lastBody = respBody
		if !retryable {
			if lastBody != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write(lastBody)
				return
			}
			writeJSON(w, statusOr(status, http.StatusBadGateway), map[string]any{
				"error": map[string]any{"type": "upstream_error", "message": errString(err)},
			})
			return
		}
		excluded[strings.TrimSpace(account.ID)] = true
	}

	if lastBody != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusOr(lastStatus, http.StatusBadGateway))
		_, _ = w.Write(lastBody)
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]any{
		"error": map[string]any{
			"type":    "all_accounts_failed",
			"message": errString(lastErr),
		},
	})
}

func (s *XaiService) consoleRoundTrip(
	ctx context.Context,
	pool *xai.XaiAccountPool,
	account *xaiShared.XaiAccount,
	payload map[string]any,
	stream bool,
	requestModel string,
	outFmt consoleOutFmt,
	w http.ResponseWriter,
) (status int, body []byte, retryable bool, err error) {
	if account == nil {
		return http.StatusServiceUnavailable, nil, true, fmt.Errorf("nil account")
	}
	sso := strings.TrimSpace(account.SSO)
	if sso == "" {
		return http.StatusUnauthorized, mustJSON(map[string]any{
			"error": map[string]any{"type": "authentication_error", "message": "sso required"},
		}), true, fmt.Errorf("sso required")
	}

	// 统一强制上游 stream，便于统一聚合/转协议
	payload["stream"] = true
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return http.StatusInternalServerError, nil, false, err
	}

	proxyURL := resolveAccountProxy(pool, account)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, xaiBackend.ConsoleResponsesURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return http.StatusInternalServerError, nil, false, err
	}
	for k, v := range xaiBackend.BuildConsoleHeaders(sso) {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "text/event-stream")

	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 0)
	resp, doErr := client.Do(req)
	if doErr != nil {
		pool.MarkFailed(account.ID, xaiShared.XaiStatusUnknown, 30*time.Second, "console_transport")
		return http.StatusBadGateway, mustJSON(map[string]any{
			"error": map[string]any{"type": "transport_error", "message": doErr.Error()},
		}), true, doErr
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		// free-usage 用尽：长冷却后换号继续；普通 429 短冷却
		if isFreeUsageExhaustedBody(raw) {
			pool.MarkFailed(account.ID, xaiShared.XaiStatusValid, 24*time.Hour, "console_free_usage_exhausted")
			return resp.StatusCode, raw, true, fmt.Errorf("console free usage exhausted")
		}
		// 短冷却即可；勿进 failed map / exhausted，否则单账号池后续全 503
		pool.CooldownConsoleAccount(account.ID, 15*time.Second, "console_rate_limited")
		return resp.StatusCode, raw, true, fmt.Errorf("console rate limited")
	}
	if resp.StatusCode == http.StatusPaymentRequired {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		pool.MarkFailed(account.ID, xaiShared.XaiStatusExhausted, 0, "console_quota_exhausted")
		return resp.StatusCode, raw, true, fmt.Errorf("console quota exhausted")
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		// 403 也可能是额度/订阅，勿一律 ban 后停掉 failover
		if isQuotaLikeBody(raw) || isFreeUsageExhaustedBody(raw) {
			pool.MarkFailed(account.ID, xaiShared.XaiStatusExhausted, 0, "console_quota_or_subscription")
			return resp.StatusCode, raw, true, fmt.Errorf("console quota/subscription: %d", resp.StatusCode)
		}
		pool.MarkFailed(account.ID, xaiShared.XaiStatusBanned, 0, "console_auth_failed")
		return resp.StatusCode, raw, true, fmt.Errorf("console auth failed: %d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		// 其它 4xx 若 body 表明额度用尽，同样换号继续（failover）
		if isQuotaLikeBody(raw) || isFreeUsageExhaustedBody(raw) {
			pool.MarkFailed(account.ID, xaiShared.XaiStatusExhausted, 0, "console_quota_exhausted")
			return resp.StatusCode, raw, true, fmt.Errorf("console quota exhausted: %d", resp.StatusCode)
		}
		return resp.StatusCode, raw, resp.StatusCode >= 500, fmt.Errorf("console HTTP %d", resp.StatusCode)
	}

	raw, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return http.StatusBadGateway, nil, true, readErr
	}
	reader := bytes.NewReader(raw)

	if stream {
		// 流式：一旦写了 200 头就无法换号。先窥探是否为错误 SSE / 限流 JSON，再决定是否提交响应。
		if retryable, markFn, peekErr := peekConsoleUpstreamFailure(raw); peekErr != nil {
			if markFn != nil {
				markFn(pool, account)
			}
			return http.StatusTooManyRequests, raw, retryable, peekErr
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		var convErr error
		switch outFmt {
		case consoleOutResponses:
			convErr = xaiBackend.ConsoleStreamToResponses(reader, w, requestModel)
		case consoleOutAnthropic:
			convErr = xaiBackend.ConsoleStreamToAnthropic(reader, w, requestModel)
		default:
			convErr = xaiBackend.ConsoleStreamToChatCompletions(reader, w, requestModel)
		}
		if convErr != nil {
			// 已向客户端写流，不可换号
			return http.StatusBadGateway, nil, false, convErr
		}
		return http.StatusOK, nil, false, nil
	}

	// 非流式
	var out []byte
	var convErr error
	switch outFmt {
	case consoleOutResponses:
		out, convErr = xaiBackend.AggregateConsoleStreamToResponses(reader, requestModel)
	case consoleOutAnthropic:
		out, convErr = xaiBackend.AggregateConsoleStreamToAnthropic(reader, requestModel)
	default:
		out, convErr = xaiBackend.AggregateConsoleStream(reader, requestModel)
		if convErr != nil {
			// 兼容上游直接 JSON
			out, convErr = consoleNonStreamJSONToChat(raw, requestModel)
		}
	}
	if convErr != nil {
		retryable, markFn, _ := peekConsoleUpstreamFailure(raw)
		if !retryable {
			// 聚合错误文案也可能带 rate limit
			low := strings.ToLower(convErr.Error())
			if strings.Contains(low, "rate limit") || strings.Contains(low, "too many") ||
				strings.Contains(low, "free usage") || strings.Contains(low, "quota") {
				retryable = true
				markFn = func(p *xai.XaiAccountPool, a *xaiShared.XaiAccount) {
					if p == nil || a == nil {
						return
					}
					p.CooldownConsoleAccount(a.ID, 15*time.Second, "console_stream_rate_limited")
				}
			}
		}
		if markFn != nil {
			markFn(pool, account)
		}
		return http.StatusBadGateway, raw, retryable, convErr
	}
	return http.StatusOK, out, false, nil
}

// peekConsoleUpstreamFailure 识别 HTTP 200 但 body 为限流/额度错误的情况（JSON 错误体或 SSE error 事件）。
// 对正常 SSE 文本流不做全文 quota 扫描，避免误伤。
func peekConsoleUpstreamFailure(raw []byte) (retryable bool, markFn func(*xai.XaiAccountPool, *xaiShared.XaiAccount), err error) {
	trim := bytes.TrimSpace(raw)
	if len(trim) == 0 {
		return false, nil, nil
	}
	low := strings.ToLower(string(trim))

	// JSON 错误体（非 event-stream）
	if trim[0] == '{' {
		if isFreeUsageExhaustedBody(trim) {
			return true, func(p *xai.XaiAccountPool, a *xaiShared.XaiAccount) {
				if p == nil || a == nil {
					return
				}
				p.MarkFailed(a.ID, xaiShared.XaiStatusValid, 24*time.Hour, "console_free_usage_exhausted")
			}, fmt.Errorf("console free usage exhausted")
		}
		if isQuotaLikeBody(trim) {
			return true, func(p *xai.XaiAccountPool, a *xaiShared.XaiAccount) {
				if p == nil || a == nil {
					return
				}
				p.MarkFailed(a.ID, xaiShared.XaiStatusExhausted, 0, "console_quota_exhausted")
			}, fmt.Errorf("console quota exhausted")
		}
		if strings.Contains(low, "rate limit") || strings.Contains(low, "too many requests") {
			return true, func(p *xai.XaiAccountPool, a *xaiShared.XaiAccount) {
				if p == nil || a == nil {
					return
				}
				p.CooldownConsoleAccount(a.ID, 15*time.Second, "console_rate_limited")
			}, fmt.Errorf("console rate limited")
		}
		return false, nil, nil
	}

	// SSE：仅在出现 error 事件时判定
	if !strings.Contains(low, "event: error") && !strings.Contains(low, "event:error") {
		return false, nil, nil
	}
	msg := "console api error"
	if i := strings.Index(low, "data:"); i >= 0 {
		msg = strings.TrimSpace(string(trim[i+5:]))
		if len(msg) > 300 {
			msg = msg[:300]
		}
	}
	retryable = strings.Contains(low, "rate") || strings.Contains(low, "quota") ||
		strings.Contains(low, "limit") || strings.Contains(low, "exhaust") ||
		strings.Contains(low, "429")
	if retryable {
		return true, func(p *xai.XaiAccountPool, a *xaiShared.XaiAccount) {
			if p == nil || a == nil {
				return
			}
			p.CooldownConsoleAccount(a.ID, 15*time.Second, "console_sse_error")
		}, fmt.Errorf("%s", msg)
	}
	return false, nil, fmt.Errorf("%s", msg)
}

func consoleNonStreamJSONToChat(raw []byte, model string) ([]byte, error) {
	if bytes.Contains(raw, []byte(`"chat.completion"`)) {
		return raw, nil
	}
	text := extractOutputText(raw)
	if text == "" {
		return nil, fmt.Errorf("console response missing output text")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "grok-4.3-console"
	}
	out := map[string]any{
		"id":      "chatcmpl-console",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": text,
			},
			"finish_reason": "stop",
		}},
	}
	return json.Marshal(out)
}

func extractOutputText(raw []byte) string {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return ""
	}
	var b strings.Builder
	walkOutputText(root, &b)
	return b.String()
}

func walkOutputText(v any, b *strings.Builder) {
	switch t := v.(type) {
	case map[string]any:
		if typ, _ := t["type"].(string); typ == "output_text" {
			if text, ok := t["text"].(string); ok {
				b.WriteString(text)
			}
		}
		for _, child := range t {
			walkOutputText(child, b)
		}
	case []any:
		for _, child := range t {
			walkOutputText(child, b)
		}
	}
}

func statusOr(code, fallback int) int {
	if code > 0 {
		return code
	}
	return fallback
}

func errString(err error) string {
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}
