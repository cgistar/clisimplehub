package proxy

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
	"sync"
	"time"

	"clisimplehub/internal/executor"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const codexLocalCompactionSummaryPrefixHTTP = "Another language model started to solve this problem and produced a summary of its thinking process. You also have access to the state of the tools that were used by that language model. Use this to build on the work that has already been done and avoid duplicating work. Here is the summary produced by the other language model, use the information in this summary to assist with your own analysis:"

var responsesHTTPWebsocketUpgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

type responsesWebsocketLogObserver func(endpoint *executor.EndpointConfig, model string)

type responsesWebsocketLogObserverKey struct{}

func withResponsesWebsocketLogObserver(ctx context.Context, observer responsesWebsocketLogObserver) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, responsesWebsocketLogObserverKey{}, observer)
}

func notifyResponsesWebsocketLogObserver(ctx context.Context, endpoint *executor.EndpointConfig, model string) {
	if ctx == nil {
		return
	}
	observer, _ := ctx.Value(responsesWebsocketLogObserverKey{}).(responsesWebsocketLogObserver)
	if observer != nil {
		observer(endpoint, model)
	}
}

// handleResponsesWebsocketHTTPBridge 对不支持上游 WSS 的端点做降级：
// 下游保持 Responses WebSocket，内部走现有 HTTP /v1/responses 代理，SSE 再转成 WS JSON 帧。
// 每个 turn 重新 ResolveEndpoint：UI 切换 active 端点后，下一请求会落到新端点。
func (p *ProxyServer) handleResponsesWebsocketHTTPBridge(w http.ResponseWriter, r *http.Request, endpoint *executor.EndpointConfig) {
	if p == nil || r == nil {
		return
	}
	conn, err := responsesHTTPWebsocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	sessionKey := responsesHTTPWebsocketSessionKey(r)
	if sessionKey == "" {
		sessionKey = uuid.NewString()
	}
	retainResponsesHTTPToolCaches(sessionKey)
	defer releaseResponsesHTTPToolCaches(sessionKey)

	clientHeaders := cloneHeaderForResponsesHTTPBridge(r.Header)
	path := "/v1/responses"
	if r.URL != nil {
		if pth := strings.TrimSpace(r.URL.Path); pth != "" && pth != "/" {
			path = pth
		}
	}
	if !strings.Contains(strings.ToLower(path), "/responses") {
		path = "/v1/responses"
	}

	var lastRequest []byte
	lastResponseOutput := []byte("[]")
	lastResponseID := ""
	var lastResponsePendingToolCallIDs []string
	forceTranscriptReplayNextRequest := false
	pinned := false
	lastEndpointKey := executor.EndpointKey(endpoint)

	for {
		msgType, payload, readErr := conn.ReadMessage()
		if readErr != nil {
			return
		}
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}

		// 每 turn 重解析：Routes 优先，否则当前 active；切换后 force full merge。
		turnEndpoint := p.resolveResponsesHTTPBridgeEndpoint(path, payload, lastRequest, endpoint)
		turnKey := executor.EndpointKey(turnEndpoint)
		if lastEndpointKey != "" && turnKey != "" && turnKey != lastEndpointKey {
			forceTranscriptReplayNextRequest = true
			pinned = false
		}
		if turnKey != "" {
			lastEndpointKey = turnKey
		}
		if turnEndpoint == nil {
			turnEndpoint = endpoint
		}
		turnModel := strings.TrimSpace(gjson.GetBytes(payload, "model").String())
		if turnModel == "" {
			turnModel = strings.TrimSpace(gjson.GetBytes(lastRequest, "model").String())
		}
		if turnEndpoint != nil {
			turnModel = strings.TrimSpace(executor.ResolveUpstreamModel(turnModel, turnEndpoint))
		}
		notifyResponsesWebsocketLogObserver(r.Context(), turnEndpoint, turnModel)

		// prewarm：首包 generate=false 本地合成，不打上游。
		if shouldHandleResponsesHTTPPrewarmLocally(payload, lastRequest) {
			prewarmBody, _, prewarmErr := normalizeResponsesWebsocketHTTPBody(payload, lastRequest, lastResponseOutput, lastResponseID, nil, true)
			if prewarmErr != nil {
				_ = writeResponsesWebsocketHTTPError(conn, http.StatusBadRequest, prewarmErr)
				continue
			}
			if turnEndpoint != nil {
				prewarmBody = applyEndpointModelToResponsesBody(prewarmBody, turnEndpoint)
			}
			if updated, errDelete := sjson.DeleteBytes(prewarmBody, "generate"); errDelete == nil {
				prewarmBody = updated
			}
			lastRequest = bytes.Clone(prewarmBody)
			lastResponseOutput = []byte("[]")
			lastResponseID = ""
			lastResponsePendingToolCallIDs = nil
			forceTranscriptReplayNextRequest = false
			if writeErr := writeResponsesHTTPSyntheticPrewarm(conn, prewarmBody); writeErr != nil {
				return
			}
			continue
		}

		previousLastRequest := bytes.Clone(lastRequest)
		previousLastResponseOutput := bytes.Clone(lastResponseOutput)
		previousLastResponseID := lastResponseID
		previousPending := append([]string(nil), lastResponsePendingToolCallIDs...)

		// HTTP 中介路径禁止 previous_response_id 增量；force replay 时强制 full merge。
		requestBody, nextLastRequest, normErr := normalizeResponsesWebsocketHTTPBody(
			payload,
			lastRequest,
			lastResponseOutput,
			lastResponseID,
			lastResponsePendingToolCallIDs,
			!forceTranscriptReplayNextRequest,
		)
		if normErr != nil {
			_ = writeResponsesWebsocketHTTPError(conn, http.StatusBadRequest, normErr)
			continue
		}
		requestBody = repairResponsesHTTPToolCalls(sessionKey, requestBody)
		requestBody = dedupeResponsesHTTPInput(requestBody)
		nextLastRequest = repairResponsesHTTPToolCalls(sessionKey, nextLastRequest)
		nextLastRequest = dedupeResponsesHTTPInput(nextLastRequest)
		// 不在此预做 model mapping：与 HTTP handleProxy 一致，由 Forward/Resolve 负责 Routes + mapping。
		requestBody = sanitizeResponsesHTTPUpstreamBody(requestBody)
		nextLastRequest = sanitizeResponsesHTTPUpstreamBody(nextLastRequest)

		// 预提交 lastRequest（失败时回滚）。
		lastRequest = bytes.Clone(nextLastRequest)
		if forceTranscriptReplayNextRequest {
			forceTranscriptReplayNextRequest = false
		}

		turnCtx, turnCancel := context.WithCancel(ctx)
		// 复用 HTTP 上游访问路径（retry.Execute → Forward），SSE/JSON 回写到下游 WS。
		completedOutput, completedID, pendingIDs, forwardErr := p.forwardResponsesWebsocketViaHTTP(turnCtx, conn, r, path, clientHeaders, requestBody, sessionKey)
		turnCancel()
		if ctx.Err() != nil {
			return
		}
		if forwardErr != nil {
			if errors.Is(forwardErr, errResponsesWebsocketHTTPClientGone) {
				return
			}
			if errors.Is(forwardErr, errResponsesWebsocketHTTPUpstreamEvent) {
				// 上游 error 帧已写出；仍按释放条件回滚会话。
				if shouldReleaseResponsesHTTPSession(forwardErr) || pinned {
					lastRequest = previousLastRequest
					lastResponseOutput = previousLastResponseOutput
					lastResponseID = previousLastResponseID
					lastResponsePendingToolCallIDs = previousPending
					forceTranscriptReplayNextRequest = true
					pinned = false
				} else {
					lastRequest = previousLastRequest
				}
				continue
			}
			if shouldReleaseResponsesHTTPSession(forwardErr) || pinned {
				lastRequest = previousLastRequest
				lastResponseOutput = previousLastResponseOutput
				lastResponseID = previousLastResponseID
				lastResponsePendingToolCallIDs = previousPending
				forceTranscriptReplayNextRequest = true
				pinned = false
			} else {
				lastRequest = previousLastRequest
			}
			_ = writeResponsesWebsocketHTTPError(conn, statusFromResponsesHTTPError(forwardErr), forwardErr)
			continue
		}

		lastResponseOutput = completedOutput
		if len(lastResponseOutput) == 0 {
			lastResponseOutput = []byte("[]")
		}
		lastResponseID = strings.TrimSpace(completedID)
		lastResponsePendingToolCallIDs = append([]string(nil), pendingIDs...)
		pinned = true
	}
}

// resolveResponsesHTTPBridgeEndpoint 按本 turn 模型解析端点（Routes → active），供 model mapping 与切换检测。
func (p *ProxyServer) resolveResponsesHTTPBridgeEndpoint(path string, payload, lastRequest []byte, fallback *executor.EndpointConfig) *executor.EndpointConfig {
	model := strings.TrimSpace(gjson.GetBytes(payload, "model").String())
	if model == "" {
		model = strings.TrimSpace(gjson.GetBytes(lastRequest, "model").String())
	}
	exec := p.ensureExecutor()
	if exec != nil && exec.ctx != nil {
		if ep, _ := exec.ctx.ResolveEndpoint(path, model); ep != nil {
			return ep
		}
	}
	return fallback
}

var (
	errResponsesWebsocketHTTPClientGone    = errors.New("responses websocket client gone")
	errResponsesWebsocketHTTPUpstreamEvent = errors.New("responses websocket upstream error event")
	errResponsesWebsocketHTTPCompleted     = errors.New("responses websocket http stream completed")
)

// forwardResponsesWebsocketViaHTTP 复用与 handleProxy 相同的上游访问：
// retry.Execute → ResolveEndpoint + Forward/transformer；流式写 ResponseWriter，非流式再 write body。
// 下游侧把写出的 SSE/JSON 解析成 Responses WS 帧。
func (p *ProxyServer) forwardResponsesWebsocketViaHTTP(
	ctx context.Context,
	conn *websocket.Conn,
	baseReq *http.Request,
	path string,
	clientHeaders http.Header,
	body []byte,
	sessionKey string,
) ([]byte, string, []string, error) {
	if p == nil || conn == nil {
		return nil, "", nil, errors.New("responses websocket http bridge is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	pr, pw := io.Pipe()
	writer := newResponsesSSEPipeWriter(pw)

	exec := p.ensureExecutor()
	headers := cloneHeaderForResponsesHTTPBridge(clientHeaders)
	// 与 HTTP 流式客户端一致：声明 SSE，强制 stream 语义由 body 保证。
	headers.Set("Accept", "text/event-stream")
	headers.Set("Content-Type", "application/json")
	headers.Del("Content-Length")

	forwardReq := &executor.ForwardRequest{
		Method:       http.MethodPost,
		Path:         path,
		RawQuery:     "",
		Headers:      headers,
		Body:         body,
		IsStreaming:  true,
		RequestModel: extractModelFromBody(body),
	}
	if baseReq != nil && baseReq.URL != nil {
		forwardReq.RawQuery = baseReq.URL.RawQuery
	}

	type execOutcome struct {
		result *executor.ExecuteResult
	}
	outcomeCh := make(chan execOutcome, 1)
	go func() {
		defer func() {
			_ = pw.Close()
		}()
		var result *executor.ExecuteResult
		if exec == nil || exec.retry == nil {
			result = &executor.ExecuteResult{
				Result: &executor.ForwardResult{
					Error:      errors.New("executor not initialized"),
					StatusCode: http.StatusServiceUnavailable,
				},
			}
			outcomeCh <- execOutcome{result: result}
			return
		}

		// 与 handleProxy 同源：模型路由 + active fallback + 可选故障转移。
		enableRetry := IsRetryablePath(path) && p.IsFallbackEnabled()
		result = exec.retry.Execute(ctx, forwardReq, writer, enableRetry)

		// handleProxy 对非流式会 writeResponseWithHeaders；流式已在 Execute 中写入 writer。
		// 成功 body 灌进 pipe 供 SSE/JSON 解析；4xx/5xx 不写 pipe，由 outcome 转 WS error 帧。
		if result != nil && result.Result != nil {
			res := result.Result
			if !res.Streamed && len(res.Body) > 0 && res.StatusCode > 0 && res.StatusCode < http.StatusBadRequest {
				writeResponseWithHeaders(writer, res.StatusCode, res.Headers, res.Body)
			}
		}
		outcomeCh <- execOutcome{result: result}
	}()

	collector := newResponsesWebsocketHTTPCollector()
	emitPayload := func(payload []byte) error {
		payload = normalizeResponsesHTTPDoneToCompleted(payload)
		payload = collector.Collect(payload)
		recordResponsesHTTPToolCallsFromPayload(sessionKey, payload)
		eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
		if writeErr := conn.WriteMessage(websocket.TextMessage, payload); writeErr != nil {
			return errResponsesWebsocketHTTPClientGone
		}
		if eventType == "error" {
			return errResponsesWebsocketHTTPUpstreamEvent
		}
		if eventType == "response.completed" || eventType == "response.done" {
			return errResponsesWebsocketHTTPCompleted
		}
		return nil
	}
	streamErr := decodeResponsesSSEStream(pr, emitPayload)
	_ = pr.Close()

	outcome := <-outcomeCh
	if errors.Is(streamErr, errResponsesWebsocketHTTPClientGone) {
		return nil, "", nil, errResponsesWebsocketHTTPClientGone
	}
	if errors.Is(streamErr, errResponsesWebsocketHTTPUpstreamEvent) {
		return nil, "", collector.PendingToolCallIDs(), errResponsesWebsocketHTTPUpstreamEvent
	}
	if errors.Is(streamErr, errResponsesWebsocketHTTPCompleted) || collector.completed {
		return collector.completedOutput, collector.completedResponseID, collector.PendingToolCallIDs(), nil
	}

	if outcome.result != nil && outcome.result.Result != nil {
		res := outcome.result.Result
		if res.StatusCode >= http.StatusBadRequest || res.Error != nil {
			msg := res.Error
			if msg == nil {
				msg = fmt.Errorf("upstream returned %d: %s", res.StatusCode, strings.TrimSpace(string(res.Body)))
			} else if len(res.Body) > 0 {
				snippet := strings.TrimSpace(string(res.Body))
				if snippet != "" && !strings.Contains(msg.Error(), snippet) {
					msg = fmt.Errorf("%w: %s", msg, truncateForError(snippet, 512))
				}
			}
			status := res.StatusCode
			if status <= 0 {
				status = http.StatusBadGateway
			}
			return nil, "", nil, &responsesWebsocketHTTPStatusError{status: status, err: msg}
		}
		// 兜底：若 body 未进 pipe（极少路径），直接转 WS 事件。
		if len(res.Body) > 0 {
			if emitErr := emitResponsesHTTPNonStreamBody(conn, collector, sessionKey, res.Body, emitPayload); emitErr != nil {
				if errors.Is(emitErr, errResponsesWebsocketHTTPClientGone) {
					return nil, "", nil, errResponsesWebsocketHTTPClientGone
				}
				if errors.Is(emitErr, errResponsesWebsocketHTTPUpstreamEvent) {
					return nil, "", collector.PendingToolCallIDs(), errResponsesWebsocketHTTPUpstreamEvent
				}
				if errors.Is(emitErr, errResponsesWebsocketHTTPCompleted) || collector.completed {
					return collector.completedOutput, collector.completedResponseID, collector.PendingToolCallIDs(), nil
				}
				return nil, "", nil, emitErr
			}
			if collector.completed {
				return collector.completedOutput, collector.completedResponseID, collector.PendingToolCallIDs(), nil
			}
		}
	}
	if streamErr != nil {
		return nil, "", nil, streamErr
	}
	return nil, "", nil, errors.New("upstream stream closed before response.completed")
}

func truncateForError(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// emitResponsesHTTPNonStreamBody 将 HTTP 非流式 body 转为下游 WS 帧。
// 支持：完整 Response 对象、已是 event 的 JSON、整段 SSE 文本。
func emitResponsesHTTPNonStreamBody(
	conn *websocket.Conn,
	collector *responsesWebsocketHTTPCollector,
	sessionKey string,
	body []byte,
	emit func([]byte) error,
) error {
	_ = conn
	_ = collector
	_ = sessionKey
	if len(body) == 0 {
		return nil
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil
	}

	// 上游偶发把 SSE 当普通 body 返回。
	if bytes.Contains(trimmed, []byte("data:")) || bytes.Contains(trimmed, []byte("\nevent:")) {
		return decodeResponsesSSEStream(bytes.NewReader(append(trimmed, '\n')), emit)
	}

	if !json.Valid(trimmed) {
		return fmt.Errorf("upstream non-stream body is not JSON: %s", truncateForError(string(trimmed), 256))
	}

	eventType := strings.TrimSpace(gjson.GetBytes(trimmed, "type").String())
	switch {
	case eventType == "error" || gjson.GetBytes(trimmed, "error").Exists() && eventType == "":
		// OpenAI 风格 {"error":{...}} 或 type=error
		payload := trimmed
		if eventType == "" {
			payload = []byte(`{"type":"error"}`)
			if errNode := gjson.GetBytes(trimmed, "error"); errNode.Exists() {
				payload, _ = sjson.SetRawBytes(payload, "error", []byte(errNode.Raw))
			} else {
				payload, _ = sjson.SetRawBytes(payload, "error", trimmed)
			}
		}
		return emit(payload)
	case eventType != "":
		// 已是 Responses 事件
		return emit(trimmed)
	case looksLikeResponsesAPIObject(trimmed):
		// 完整 Response 对象 → response.completed
		payload := []byte(`{"type":"response.completed"}`)
		var err error
		payload, err = sjson.SetRawBytes(payload, "response", trimmed)
		if err != nil {
			return err
		}
		if status := strings.TrimSpace(gjson.GetBytes(payload, "response.status").String()); status == "" {
			payload, _ = sjson.SetBytes(payload, "response.status", "completed")
		}
		// 补 output_item.done，方便下游客户端渲染增量与 transcript 收集。
		if output := gjson.GetBytes(trimmed, "output"); output.IsArray() {
			for i, item := range output.Array() {
				if !item.IsObject() {
					continue
				}
				itemEvent := []byte(`{"type":"response.output_item.done"}`)
				itemEvent, _ = sjson.SetBytes(itemEvent, "output_index", i)
				itemEvent, _ = sjson.SetRawBytes(itemEvent, "item", []byte(item.Raw))
				if err := emit(itemEvent); err != nil {
					return err
				}
			}
		}
		return emit(payload)
	default:
		return fmt.Errorf("upstream non-stream body is not a responses object: %s", truncateForError(string(trimmed), 256))
	}
}

func looksLikeResponsesAPIObject(body []byte) bool {
	if !json.Valid(body) {
		return false
	}
	parsed := gjson.ParseBytes(body)
	if !parsed.IsObject() {
		return false
	}
	object := strings.TrimSpace(parsed.Get("object").String())
	if strings.EqualFold(object, "response") {
		return true
	}
	// 部分兼容实现省略 object，但带 id + output/status。
	if strings.TrimSpace(parsed.Get("id").String()) == "" {
		return false
	}
	if parsed.Get("output").Exists() {
		return true
	}
	status := strings.TrimSpace(parsed.Get("status").String())
	return status == "completed" || status == "incomplete" || status == "failed" || status == "in_progress"
}

type responsesWebsocketHTTPStatusError struct {
	status int
	err    error
}

func (e *responsesWebsocketHTTPStatusError) Error() string {
	if e == nil {
		return "responses websocket http bridge error"
	}
	if e.err != nil {
		return e.err.Error()
	}
	return fmt.Sprintf("upstream returned %d", e.status)
}

func (e *responsesWebsocketHTTPStatusError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func statusFromResponsesHTTPError(err error) int {
	var statusErr *responsesWebsocketHTTPStatusError
	if errors.As(err, &statusErr) && statusErr != nil && statusErr.status > 0 {
		return statusErr.status
	}
	return http.StatusBadGateway
}

type responsesSSEPipeWriter struct {
	header http.Header
	status int
	mu     sync.Mutex
	w      *io.PipeWriter
	wrote  bool
}

func newResponsesSSEPipeWriter(w *io.PipeWriter) *responsesSSEPipeWriter {
	return &responsesSSEPipeWriter{
		header: make(http.Header),
		w:      w,
	}
}

func (w *responsesSSEPipeWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *responsesSSEPipeWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.w == nil {
		return 0, io.ErrClosedPipe
	}
	w.wrote = true
	return w.w.Write(p)
}

func (w *responsesSSEPipeWriter) WriteHeader(statusCode int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == 0 {
		w.status = statusCode
	}
}

func (w *responsesSSEPipeWriter) Flush() {}

func cloneHeaderForResponsesHTTPBridge(src http.Header) http.Header {
	if src == nil {
		return make(http.Header)
	}
	dst := src.Clone()
	for _, key := range []string{
		"Connection", "Upgrade", "Sec-Websocket-Key", "Sec-Websocket-Version",
		"Sec-Websocket-Extensions", "Sec-Websocket-Protocol", "Sec-WebSocket-Key",
		"Sec-WebSocket-Version", "Sec-WebSocket-Extensions", "Sec-WebSocket-Protocol",
		"Content-Length", "Transfer-Encoding", "Accept-Encoding",
	} {
		dst.Del(key)
	}
	return dst
}

func normalizeResponsesWebsocketHTTPBody(rawJSON, lastRequest, lastResponseOutput []byte, lastResponseID string, pendingToolCallIDs []string, allowMergeHelpers bool) ([]byte, []byte, error) {
	if !json.Valid(rawJSON) {
		return nil, lastRequest, errors.New("invalid websocket request JSON")
	}
	_ = allowMergeHelpers
	_ = pendingToolCallIDs
	requestType := strings.TrimSpace(gjson.GetBytes(rawJSON, "type").String())
	switch requestType {
	case "response.create":
		if len(lastRequest) == 0 {
			return normalizeResponsesWebsocketHTTPCreate(rawJSON)
		}
		return normalizeResponsesWebsocketHTTPSubsequent(rawJSON, lastRequest, lastResponseOutput, lastResponseID)
	case "response.append":
		return normalizeResponsesWebsocketHTTPSubsequent(rawJSON, lastRequest, lastResponseOutput, lastResponseID)
	default:
		if requestType == "" {
			return nil, lastRequest, errors.New("unsupported websocket request type: missing type")
		}
		return nil, lastRequest, fmt.Errorf("unsupported websocket request type: %s", requestType)
	}
}

func normalizeResponsesWebsocketHTTPCreate(rawJSON []byte) ([]byte, []byte, error) {
	normalized, err := sjson.DeleteBytes(rawJSON, "type")
	if err != nil {
		normalized = bytes.Clone(rawJSON)
	}
	// HTTP 上游通常不能跨连接消费 previous_response_id，首包与后续 merge 均剥离。
	normalized, _ = sjson.DeleteBytes(normalized, "previous_response_id")
	normalized, _ = sjson.SetBytes(normalized, "stream", true)
	if !gjson.GetBytes(normalized, "input").Exists() {
		normalized, _ = sjson.SetRawBytes(normalized, "input", []byte("[]"))
	}
	if strings.TrimSpace(gjson.GetBytes(normalized, "model").String()) == "" {
		return nil, nil, errors.New("missing model in response.create request")
	}
	return normalized, bytes.Clone(normalized), nil
}

func normalizeResponsesWebsocketHTTPSubsequent(rawJSON, lastRequest, lastResponseOutput []byte, lastResponseID string) ([]byte, []byte, error) {
	if len(lastRequest) == 0 {
		return nil, lastRequest, errors.New("websocket request received before response.create")
	}
	nextInput := gjson.GetBytes(rawJSON, "input")
	if !nextInput.Exists() || !nextInput.IsArray() {
		return nil, lastRequest, errors.New("websocket request requires array field: input")
	}

	// compaction 摘要 / full transcript：替换而非 merge。
	if shouldReplaceResponsesHTTPTranscript(rawJSON, nextInput) {
		normalized, _ := sjson.DeleteBytes(rawJSON, "type")
		normalized, _ = sjson.DeleteBytes(normalized, "previous_response_id")
		normalized = ensureResponsesHTTPInheritedFields(normalized, lastRequest)
		normalized, _ = sjson.SetBytes(normalized, "stream", true)
		return normalized, bytes.Clone(normalized), nil
	}

	mergedInput, err := mergeJSONArrayRaw(
		normalizeResponsesHTTPInputArrayRaw(gjson.GetBytes(lastRequest, "input")),
		normalizeJSONArrayRawHTTP(lastResponseOutput),
	)
	if err != nil {
		return nil, lastRequest, fmt.Errorf("invalid previous response output: %w", err)
	}
	mergedInput, err = mergeJSONArrayRaw(mergedInput, nextInput.Raw)
	if err != nil {
		return nil, lastRequest, fmt.Errorf("invalid request input: %w", err)
	}

	normalized, _ := sjson.DeleteBytes(rawJSON, "type")
	normalized, _ = sjson.DeleteBytes(normalized, "previous_response_id")
	normalized, err = sjson.SetRawBytes(normalized, "input", []byte(mergedInput))
	if err != nil {
		return nil, lastRequest, err
	}
	normalized = ensureResponsesHTTPInheritedFields(normalized, lastRequest)
	normalized, _ = sjson.SetBytes(normalized, "stream", true)
	_ = lastResponseID // 保留参数便于后续增量扩展
	return normalized, bytes.Clone(normalized), nil
}

func ensureResponsesHTTPInheritedFields(normalized, lastRequest []byte) []byte {
	if !gjson.GetBytes(normalized, "model").Exists() {
		if model := strings.TrimSpace(gjson.GetBytes(lastRequest, "model").String()); model != "" {
			normalized, _ = sjson.SetBytes(normalized, "model", model)
		}
	}
	if !gjson.GetBytes(normalized, "instructions").Exists() {
		if instructions := gjson.GetBytes(lastRequest, "instructions"); instructions.Exists() {
			normalized, _ = sjson.SetRawBytes(normalized, "instructions", []byte(instructions.Raw))
		}
	}
	if !gjson.GetBytes(normalized, "prompt_cache_key").Exists() {
		if key := strings.TrimSpace(gjson.GetBytes(lastRequest, "prompt_cache_key").String()); key != "" {
			normalized, _ = sjson.SetBytes(normalized, "prompt_cache_key", key)
		}
	}
	return normalized
}

func shouldReplaceResponsesHTTPTranscript(rawJSON []byte, nextInput gjson.Result) bool {
	requestType := strings.TrimSpace(gjson.GetBytes(rawJSON, "type").String())
	if requestType != "response.create" && requestType != "response.append" {
		return false
	}
	previousResponseID := gjson.GetBytes(rawJSON, "previous_response_id")
	if strings.TrimSpace(previousResponseID.String()) != "" {
		return false
	}
	if !nextInput.Exists() || !nextInput.IsArray() {
		return false
	}
	if requestType == "response.create" && !previousResponseID.Exists() && inputHasCodexLocalCompactionSummaryHTTP(nextInput) {
		return true
	}
	for _, item := range nextInput.Array() {
		switch strings.TrimSpace(item.Get("type").String()) {
		case "function_call", "custom_tool_call", "compaction", "compaction_summary":
			return true
		case "message":
			if strings.TrimSpace(item.Get("role").String()) == "assistant" {
				return true
			}
		}
	}
	return false
}

func inputHasCodexLocalCompactionSummaryHTTP(input gjson.Result) bool {
	if !input.IsArray() {
		return false
	}
	hasSummary := false
	for index, item := range input.Array() {
		itemType := strings.TrimSpace(item.Get("type").String())
		if itemType == "additional_tools" {
			tools := item.Get("tools")
			if index != 0 || strings.TrimSpace(item.Get("role").String()) != "developer" || !tools.IsArray() {
				return false
			}
			for _, tool := range tools.Array() {
				if !tool.IsObject() || strings.TrimSpace(tool.Get("type").String()) == "" {
					return false
				}
			}
			continue
		}
		if itemType != "" && itemType != "message" {
			return false
		}
		role := strings.TrimSpace(item.Get("role").String())
		if role != "user" && role != "developer" {
			return false
		}
		if role == "user" && strings.HasPrefix(codexLocalCompactionMessageTextHTTP(item), codexLocalCompactionSummaryPrefixHTTP+"\n") {
			hasSummary = true
		}
	}
	return hasSummary
}

func codexLocalCompactionMessageTextHTTP(message gjson.Result) string {
	content := message.Get("content")
	if content.Type == gjson.String {
		return content.String()
	}
	if !content.IsArray() {
		return ""
	}
	var text strings.Builder
	for _, part := range content.Array() {
		if strings.TrimSpace(part.Get("type").String()) == "input_text" {
			text.WriteString(part.Get("text").String())
		}
	}
	return text.String()
}

func sanitizeResponsesHTTPUpstreamBody(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	// HTTP 桥仅剥离 WS 会话字段与本地 prewarm 标记；其余字段透传给上游。
	for _, field := range []string{
		"previous_response_id",
		"generate",
	} {
		body, _ = sjson.DeleteBytes(body, field)
	}
	body, _ = sjson.SetBytes(body, "stream", true)
	return body
}

func shouldHandleResponsesHTTPPrewarmLocally(rawJSON, lastRequest []byte) bool {
	if len(lastRequest) != 0 {
		return false
	}
	if strings.TrimSpace(gjson.GetBytes(rawJSON, "type").String()) != "response.create" {
		return false
	}
	generateResult := gjson.GetBytes(rawJSON, "generate")
	return generateResult.Exists() && !generateResult.Bool()
}

func writeResponsesHTTPSyntheticPrewarm(conn *websocket.Conn, requestJSON []byte) error {
	payloads, err := syntheticResponsesHTTPPrewarmPayloads(requestJSON)
	if err != nil {
		return err
	}
	for i := range payloads {
		if writeErr := conn.WriteMessage(websocket.TextMessage, payloads[i]); writeErr != nil {
			return writeErr
		}
	}
	return nil
}

func syntheticResponsesHTTPPrewarmPayloads(requestJSON []byte) ([][]byte, error) {
	responseID := "resp_prewarm_" + uuid.NewString()
	createdAt := time.Now().Unix()
	modelName := strings.TrimSpace(gjson.GetBytes(requestJSON, "model").String())

	createdPayload := []byte(`{"type":"response.created","sequence_number":0,"response":{"id":"","object":"response","created_at":0,"status":"in_progress","background":false,"error":null,"output":[]}}`)
	var errSet error
	createdPayload, errSet = sjson.SetBytes(createdPayload, "response.id", responseID)
	if errSet != nil {
		return nil, errSet
	}
	createdPayload, errSet = sjson.SetBytes(createdPayload, "response.created_at", createdAt)
	if errSet != nil {
		return nil, errSet
	}
	if modelName != "" {
		createdPayload, errSet = sjson.SetBytes(createdPayload, "response.model", modelName)
		if errSet != nil {
			return nil, errSet
		}
	}

	completedPayload := []byte(`{"type":"response.completed","sequence_number":1,"response":{"id":"","object":"response","created_at":0,"status":"completed","background":false,"error":null,"output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}}`)
	completedPayload, errSet = sjson.SetBytes(completedPayload, "response.id", responseID)
	if errSet != nil {
		return nil, errSet
	}
	completedPayload, errSet = sjson.SetBytes(completedPayload, "response.created_at", createdAt)
	if errSet != nil {
		return nil, errSet
	}
	if modelName != "" {
		completedPayload, errSet = sjson.SetBytes(completedPayload, "response.model", modelName)
		if errSet != nil {
			return nil, errSet
		}
	}
	return [][]byte{createdPayload, completedPayload}, nil
}

func shouldReleaseResponsesHTTPSession(err error) bool {
	if err == nil {
		return false
	}
	var statusErr *responsesWebsocketHTTPStatusError
	if errors.As(err, &statusErr) && statusErr != nil && statusErr.status > 0 {
		switch statusErr.status {
		case http.StatusUnauthorized,
			http.StatusPaymentRequired,
			http.StatusForbidden,
			http.StatusTooManyRequests,
			http.StatusRequestTimeout,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout,
			http.StatusRequestEntityTooLarge:
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "stream closed before response.completed"),
		strings.Contains(msg, "previous_response_not_found"),
		strings.Contains(msg, "ws_failed"),
		strings.Contains(msg, "upstream stream closed before first payload"),
		strings.Contains(msg, "empty_stream"),
		strings.Contains(msg, "message_too_big"):
		return true
	default:
		return false
	}
}

func normalizeResponsesHTTPInputArrayRaw(input gjson.Result) string {
	if input.IsArray() {
		return normalizeJSONArrayRawHTTP([]byte(input.Raw))
	}
	if input.Type != gjson.String {
		return "[]"
	}
	message := []byte(`[{"type":"message","role":"user","content":[{"type":"input_text","text":""}]}]`)
	message, err := sjson.SetBytes(message, "0.content.0.text", input.String())
	if err != nil {
		return "[]"
	}
	return string(message)
}

func normalizeJSONArrayRawHTTP(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "[]"
	}
	result := gjson.Parse(trimmed)
	if result.Type == gjson.JSON && result.IsArray() {
		return trimmed
	}
	return "[]"
}

func mergeJSONArrayRaw(leftRaw, rightRaw string) (string, error) {
	left := []json.RawMessage{}
	right := []json.RawMessage{}
	if strings.TrimSpace(leftRaw) != "" && strings.TrimSpace(leftRaw) != "[]" {
		if err := json.Unmarshal([]byte(leftRaw), &left); err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(rightRaw) != "" && strings.TrimSpace(rightRaw) != "[]" {
		if err := json.Unmarshal([]byte(rightRaw), &right); err != nil {
			return "", err
		}
	}
	merged := append(left, right...)
	if len(merged) == 0 {
		return "[]", nil
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func applyEndpointModelToResponsesBody(body []byte, endpoint *executor.EndpointConfig) []byte {
	if endpoint == nil || len(body) == 0 {
		return body
	}
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	resolved := strings.TrimSpace(executor.ResolveUpstreamModel(model, endpoint))
	if resolved == "" || resolved == model {
		return body
	}
	updated, err := sjson.SetBytes(body, "model", resolved)
	if err != nil {
		return body
	}
	return updated
}

func normalizeResponsesHTTPDoneToCompleted(payload []byte) []byte {
	if strings.TrimSpace(gjson.GetBytes(payload, "type").String()) != "response.done" {
		return payload
	}
	if updated, err := sjson.SetBytes(payload, "type", "response.completed"); err == nil {
		return updated
	}
	return payload
}

type responsesWebsocketHTTPCollector struct {
	outputItemsByIndex  map[int64][]byte
	outputItemsFallback [][]byte
	completedOutput     []byte
	completedResponseID string
	pendingToolCallIDs  map[string]struct{}
	completed           bool
}

func newResponsesWebsocketHTTPCollector() *responsesWebsocketHTTPCollector {
	return &responsesWebsocketHTTPCollector{
		outputItemsByIndex: make(map[int64][]byte),
		completedOutput:    []byte("[]"),
		pendingToolCallIDs: make(map[string]struct{}),
	}
}

func (c *responsesWebsocketHTTPCollector) Collect(payload []byte) []byte {
	if c == nil || len(payload) == 0 {
		return payload
	}
	if gjson.GetBytes(payload, "type").String() == "response.output_item.done" {
		item := gjson.GetBytes(payload, "item")
		if item.Exists() && item.IsObject() {
			if index := gjson.GetBytes(payload, "output_index"); index.Exists() {
				c.outputItemsByIndex[index.Int()] = bytes.Clone([]byte(item.Raw))
			} else {
				c.outputItemsFallback = append(c.outputItemsFallback, bytes.Clone([]byte(item.Raw)))
			}
			c.updatePendingToolCallIDs(item)
		}
	}
	eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
	if eventType != "response.completed" && eventType != "response.done" {
		return payload
	}
	c.completed = true
	output := gjson.GetBytes(payload, "response.output")
	if !output.Exists() || !output.IsArray() || len(output.Array()) == 0 {
		if restored := c.collectedOutput(); len(restored) > 2 {
			payload, _ = sjson.SetRawBytes(payload, "response.output", restored)
			output = gjson.GetBytes(payload, "response.output")
		}
	}
	if output.Exists() && output.IsArray() {
		c.completedOutput = bytes.Clone([]byte(output.Raw))
		for _, item := range output.Array() {
			c.updatePendingToolCallIDs(item)
		}
	}
	c.completedResponseID = strings.TrimSpace(gjson.GetBytes(payload, "response.id").String())
	return payload
}

func (c *responsesWebsocketHTTPCollector) updatePendingToolCallIDs(item gjson.Result) {
	if c == nil || !item.Exists() {
		return
	}
	callID := strings.TrimSpace(item.Get("call_id").String())
	if callID == "" {
		return
	}
	switch strings.TrimSpace(item.Get("type").String()) {
	case "function_call", "custom_tool_call":
		c.pendingToolCallIDs[callID] = struct{}{}
	case "function_call_output", "custom_tool_call_output":
		delete(c.pendingToolCallIDs, callID)
	}
}

func (c *responsesWebsocketHTTPCollector) PendingToolCallIDs() []string {
	if c == nil || len(c.pendingToolCallIDs) == 0 {
		return nil
	}
	out := make([]string, 0, len(c.pendingToolCallIDs))
	for id := range c.pendingToolCallIDs {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (c *responsesWebsocketHTTPCollector) collectedOutput() []byte {
	if c == nil || len(c.outputItemsByIndex)+len(c.outputItemsFallback) == 0 {
		return []byte("[]")
	}
	indexes := make([]int64, 0, len(c.outputItemsByIndex))
	for index := range c.outputItemsByIndex {
		indexes = append(indexes, index)
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
	items := make([]json.RawMessage, 0, len(indexes)+len(c.outputItemsFallback))
	for _, index := range indexes {
		items = append(items, json.RawMessage(c.outputItemsByIndex[index]))
	}
	for _, item := range c.outputItemsFallback {
		items = append(items, json.RawMessage(item))
	}
	out, err := json.Marshal(items)
	if err != nil {
		return []byte("[]")
	}
	return out
}

func decodeResponsesSSEStream(reader io.Reader, emit func([]byte) error) error {
	if reader == nil {
		return nil
	}
	buffered := bufio.NewReader(reader)
	dataLines := make([]string, 0, 1)
	flush := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		if data == "" || data == "[DONE]" {
			return nil
		}
		if !json.Valid([]byte(data)) {
			return fmt.Errorf("invalid SSE JSON: %s", data)
		}
		return emit([]byte(data))
	}

	for {
		line, err := buffered.ReadString('\n')
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			if flushErr := flush(); flushErr != nil {
				return flushErr
			}
		case strings.HasPrefix(trimmed, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(trimmed[len("data:"):]))
		case strings.HasPrefix(trimmed, "event:"), strings.HasPrefix(trimmed, "id:"), strings.HasPrefix(trimmed, "retry:"), strings.HasPrefix(trimmed, ":"):
		case json.Valid([]byte(trimmed)):
			if flushErr := flush(); flushErr != nil {
				return flushErr
			}
			if emitErr := emit([]byte(trimmed)); emitErr != nil {
				return emitErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return flush()
			}
			return err
		}
	}
}

func writeResponsesWebsocketHTTPError(conn *websocket.Conn, status int, err error) error {
	if conn == nil {
		return errors.New("websocket connection is nil")
	}
	if status <= 0 {
		status = http.StatusBadGateway
	}
	message := http.StatusText(status)
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = err.Error()
	}
	errorBody := any(map[string]any{
		"type":    "server_error",
		"message": message,
	})
	if json.Valid([]byte(message)) {
		var structured map[string]any
		if json.Unmarshal([]byte(message), &structured) == nil {
			if node, ok := structured["error"]; ok {
				errorBody = node
			}
		}
	}
	payload, marshalErr := json.Marshal(map[string]any{
		"type":   "error",
		"status": status,
		"error":  errorBody,
	})
	if marshalErr != nil {
		return marshalErr
	}
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.WriteMessage(websocket.TextMessage, payload)
}
