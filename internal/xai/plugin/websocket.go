package xaiplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"clisimplehub/internal/executor"
	"clisimplehub/internal/logger"
	xai "clisimplehub/internal/xai"
	xaiBackend "clisimplehub/internal/xai/backend"
	xaiShared "clisimplehub/internal/xai/shared"

	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"golang.org/x/net/proxy"
)

const (
	xaiWSHandshakeTimeout = 30 * time.Second
	xaiWSIdleTimeout      = 5 * time.Minute
)

var xaiWebsocketUpgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

// activeWSByAccount 记录账号关联的下游连接，删账号时关闭。
var (
	activeWSMu        sync.Mutex
	activeWSByAccount = map[string]map[*websocket.Conn]struct{}{}
)

func trackWSConn(accountID string, conn *websocket.Conn) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || conn == nil {
		return
	}
	activeWSMu.Lock()
	defer activeWSMu.Unlock()
	if activeWSByAccount[accountID] == nil {
		activeWSByAccount[accountID] = make(map[*websocket.Conn]struct{})
	}
	activeWSByAccount[accountID][conn] = struct{}{}
}

func untrackWSConn(accountID string, conn *websocket.Conn) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || conn == nil {
		return
	}
	activeWSMu.Lock()
	defer activeWSMu.Unlock()
	if set := activeWSByAccount[accountID]; set != nil {
		delete(set, conn)
		if len(set) == 0 {
			delete(activeWSByAccount, accountID)
		}
	}
}

// CloseWebsocketSessionsForAccount 关闭指定账号的下游 WS，并清理关联上游 session。
func CloseWebsocketSessionsForAccount(accountID string) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return
	}
	activeWSMu.Lock()
	set := activeWSByAccount[accountID]
	delete(activeWSByAccount, accountID)
	activeWSMu.Unlock()
	for conn := range set {
		_ = conn.Close()
	}
	// 关闭该账号全部上游 conn + ID/transcript state
	_ = xaiBackend.GlobalUpstreamConnStore().CloseSessionsForAuthID(accountID, "auth_removed")
}

// wsUpstreamState 单条下游 WS 对应的上游连接与会话（懒拨号后填充）。
type wsUpstreamState struct {
	// clientSession：客户端可见会话（未混淆）；用于多轮强制对齐 body.prompt_cache_key
	clientSession string
	// upstreamSession：握手 x-grok-conv-id / ID store key。
	upstreamSession string
	headers         http.Header
	idState         *xaiBackend.WebsocketIDState
	connSess        *xaiBackend.UpstreamConnSession
	upstream        *websocket.Conn
	dialFn          func(context.Context) (*websocket.Conn, *http.Response, error)
	startReader     func(*xaiBackend.UpstreamConnSession, *websocket.Conn)
	releaseOnce     sync.Once
}

func (st *wsUpstreamState) release() {
	if st == nil || st.connSess == nil {
		return
	}
	st.releaseOnce.Do(func() {
		st.connSess.UnlockRequest()
		if n := st.connSess.Release(); n <= 0 {
			st.connSess.Close("client_detached")
		}
	})
}

func (s *XaiService) HandleResponsesWebsocket(w http.ResponseWriter, r *http.Request) {
	downstream, err := xaiWebsocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer downstream.Close()

	ctx := r.Context()
	pool := xai.GetPool()
	if pool == nil {
		_ = writeWSError(downstream, "xai pool not initialized")
		return
	}
	config := pool.Snapshot()
	if config == nil {
		config = &xaiShared.XaiMultiConfig{Config: xaiShared.DefaultXaiConfig()}
	}

	account := pool.SelectWebsocketStrict()
	if account == nil {
		_ = writeWSError(downstream, "no available xai accounts with websockets enabled")
		return
	}
	accountID := strings.TrimSpace(account.ID)
	trackWSConn(accountID, downstream)
	defer func() { untrackWSConn(accountID, downstream) }()

	proxyURL := resolveAccountProxy(pool, account)
	token, err := ensureAccessToken(ctx, pool, account, proxyURL)
	if err != nil {
		_ = writeWSError(downstream, "auth failed: "+err.Error())
		return
	}

	httpURL := xaiBackend.UpstreamWebsocketURL(config, account, r.URL.Path)
	wsURL, err := xaiBackend.BuildWebsocketURL(httpURL)
	if err != nil {
		_ = writeWSError(downstream, err.Error())
		return
	}
	upgradeHeaders := r.Header.Clone()
	// 跨请求绑 WS：仅信 X-Execution-Session-Id。
	// 不信任客户端 x-grok-conv-id（该头仅由代理写给上游）。
	// 无 execution 时：body.prompt_cache_key / Claude·composer 由 ResolveUpstreamSessionID 推导。
	headerSession := xaiBackend.ExecutionSessionIDFromHeaders(upgradeHeaders)

	var st *wsUpstreamState
	defer func() {
		if st != nil {
			st.release()
		}
	}()

	excludedAccounts := map[string]bool{}
	refreshed401 := map[string]bool{}
	var ensureDialed func(string) error
	ensureDialed = func(clientSession string) error {
		if st != nil && st.upstream != nil {
			return nil
		}
		clientSession = strings.TrimSpace(clientSession)
		if clientSession == "" {
			clientSession = fmt.Sprintf("ephemeral-%s-%d", accountID, time.Now().UnixNano())
		}

		upstreamSession := clientSession

		headers := xaiBackend.ApplyWebsocketHeadersWithAccount(token, upstreamSession, config, account)

		idState := xaiBackend.GlobalWebsocketIDStore().Get(upstreamSession)
		if idState == nil {
			idState = xaiBackend.NewWebsocketIDState()
		}
		sessKey := xaiBackend.SessionKey(upstreamSession, accountID)
		connSess := xaiBackend.GlobalUpstreamConnStore().GetOrCreate(sessKey)
		if connSess != nil {
			connSess.SetAuthMeta(accountID, upstreamSession)
			connSess.Acquire()
			connSess.LockRequest()
		}

		dialFn := func(c context.Context) (*websocket.Conn, *http.Response, error) {
			return dialXaiWebsocket(c, wsURL, headers, proxyURL)
		}
		startReader := func(sess *xaiBackend.UpstreamConnSession, conn *websocket.Conn) {
			sess.ReadLoop(conn)
		}

		upstream, resp, dialErr := ensureOrDial(ctx, connSess, accountID, wsURL, dialFn, startReader)
		if dialErr != nil {
			if connSess != nil {
				connSess.UnlockRequest()
				if n := connSess.Release(); n <= 0 {
					connSess.Close("dial_failed")
				}
			}
			msg := dialErr.Error()
			status := 0
			var responseHeaders http.Header
			var responseBody []byte
			if resp != nil {
				status = resp.StatusCode
				responseHeaders = resp.Header.Clone()
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				responseBody = body
				_ = resp.Body.Close()
				if len(body) > 0 {
					msg = fmt.Sprintf("websocket dial failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
				}
			}
			// 401 先强制刷新同一账号并重试一次；第二次失败才进入状态分类和换号。
			if shouldRefreshWS401(status, account, refreshed401[accountID]) {
				refreshed401[accountID] = true
				if refreshed, refreshErr := refreshAccessToken(ctx, pool, account, proxyURL); refreshErr == nil && refreshed != "" {
					token = refreshed
					return ensureDialed(clientSession)
				}
			}
			applyWSHandshakeFailure(pool, accountID, status, responseHeaders, responseBody)
			excludedAccounts[accountID] = true
			if next := pool.SelectWebsocketExcluding(excludedAccounts); next != nil {
				nextProxy := resolveAccountProxy(pool, next)
				nextToken, tokenErr := ensureAccessToken(ctx, pool, next, nextProxy)
				if tokenErr == nil {
					untrackWSConn(accountID, downstream)
					account = next
					accountID = strings.TrimSpace(next.ID)
					trackWSConn(accountID, downstream)
					proxyURL, token = nextProxy, nextToken
					httpURL = xaiBackend.UpstreamWebsocketURL(config, account, r.URL.Path)
					wsURL, tokenErr = xaiBackend.BuildWebsocketURL(httpURL)
					if tokenErr == nil {
						return ensureDialed(clientSession)
					}
				}
			}
			return fmt.Errorf("%s", msg)
		}

		st = &wsUpstreamState{
			clientSession:   clientSession,
			upstreamSession: upstreamSession,
			headers:         headers,
			idState:         idState,
			connSess:        connSess,
			upstream:        upstream,
			dialFn:          dialFn,
			startReader:     startReader,
		}
		pool.ReportSuccess(account.ID)
		return nil
	}

	// 主循环：读下游 → 解析会话（懒拨号）→ 处理一轮 → 写上游 → 读到 completed
	for {
		_ = downstream.SetReadDeadline(time.Now().Add(xaiWSIdleTimeout))
		msgType, payload, readErr := downstream.ReadMessage()
		if readErr != nil {
			return
		}
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}

		eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
		if eventType != "" && eventType != "response.create" {
			// 控制消息：若尚未拨号，用 header/ephemeral 建立上游
			if st == nil {
				if err := ensureDialed(headerSession); err != nil {
					_ = writeWSError(downstream, err.Error())
					return
				}
			}
			if writeErr := writeUpstream(st.connSess, st.upstream, payload); writeErr != nil {
				up, _, reErr := reconnectUpstream(ctx, st.connSess, accountID, wsURL, st.dialFn, st.startReader)
				if reErr != nil || writeUpstream(st.connSess, up, payload) != nil {
					_ = writeWSError(downstream, "upstream write failed: "+writeErr.Error())
					return
				}
				st.upstream = up
			}
			continue
		}

		// 解析本轮客户端会话：连接级已建立则固定；否则 execution > body.prompt_cache_key > Claude/composer
		preferred := ""
		if st != nil {
			preferred = st.clientSession
		}
		if preferred == "" {
			preferred = headerSession // 仅 Execution-Session-Id
		}
		model := gjson.GetBytes(payload, "model").String()
		// 剥离入站 x-grok-conv-id，避免 replay/其它路径误读
		sessionHeaders := upgradeHeaders.Clone()
		sessionHeaders.Del("x-grok-conv-id")
		sessionHeaders.Del("X-Grok-Conv-Id")
		clientSession := xaiBackend.ResolveUpstreamSessionID(payload, sessionHeaders, preferred, model)

		if st == nil {
			if err := ensureDialed(clientSession); err != nil {
				_ = writeWSError(downstream, err.Error())
				return
			}
		}

		// compaction_trigger → HTTP compact + 伪 WS 事件
		if xaiBackend.InputHasCompactionTrigger(payload) {
			if err := s.handleWSCompaction(ctx, pool, config, account, proxyURL, token, st.idState, payload, downstream); err != nil {
				_ = writeWSError(downstream, err.Error())
			}
			continue
		}

		// 准备 + ID 映射；强制 body.prompt_cache_key 与握手 session 对齐
		reqBody, mapper, warmup, prepErr := prepareWSTurn(
			payload, st.idState, st.clientSession, st.upstreamSession,
		)
		if prepErr != nil {
			_ = writeWSError(downstream, prepErr.Error())
			continue
		}

		var readCh chan xaiBackend.UpstreamRead
		if st.connSess != nil {
			readCh = make(chan xaiBackend.UpstreamRead, 4096)
			st.connSess.SetActive(readCh)
		}

		if writeErr := writeUpstream(st.connSess, st.upstream, reqBody); writeErr != nil {
			if st.connSess != nil {
				st.connSess.ClearActive(readCh)
				st.connSess.InvalidateConn(st.upstream, "send_error", writeErr, true)
			}
			up, _, reErr := reconnectUpstream(ctx, st.connSess, accountID, wsURL, st.dialFn, st.startReader)
			if reErr != nil {
				_ = writeWSError(downstream, "upstream reconnect failed: "+reErr.Error())
				return
			}
			st.upstream = up
			if st.connSess != nil {
				readCh = make(chan xaiBackend.UpstreamRead, 4096)
				st.connSess.SetActive(readCh)
			}
			if writeErr = writeUpstream(st.connSess, st.upstream, reqBody); writeErr != nil {
				if st.connSess != nil {
					st.connSess.ClearActive(readCh)
				}
				_ = writeWSError(downstream, "upstream write failed: "+writeErr.Error())
				return
			}
		}

		if err := pumpUpstreamTurn(ctx, st.connSess, st.upstream, readCh, st.idState, mapper, reqBody, warmup, downstream); err != nil {
			applyWSTurnFailure(pool, accountID, err)
			if st.connSess != nil {
				st.connSess.InvalidateConn(st.upstream, "turn_error", err, true)
			}
			if u2, _, e2 := reconnectUpstream(ctx, st.connSess, accountID, wsURL, st.dialFn, st.startReader); e2 == nil {
				st.upstream = u2
			}
			if !strings.Contains(err.Error(), "downstream") {
				_ = writeWSError(downstream, err.Error())
			} else {
				return
			}
		}
	}
}

func shouldRefreshWS401(status int, account *xaiShared.XaiAccount, alreadyRefreshed bool) bool {
	return status == http.StatusUnauthorized && !alreadyRefreshed && account != nil && strings.TrimSpace(account.RefreshToken) != ""
}

func applyWSHandshakeFailure(pool *xai.XaiAccountPool, accountID string, status int, headers http.Header, body []byte) {
	if pool == nil || accountID == "" {
		return
	}
	switch status {
	case http.StatusTooManyRequests:
		cooldown := parseRetryAfter(headers)
		if cooldown <= 0 {
			cooldown = time.Minute
		}
		if isFreeUsageExhaustedBody(body) {
			cooldown = 24 * time.Hour
		}
		pool.CooldownWebsocketAccount(accountID, cooldown, "websocket_rate_limit")
	case http.StatusPaymentRequired, http.StatusForbidden:
		if isQuotaLikeBody(body) || isFreeUsageExhaustedBody(body) {
			pool.MarkFailed(accountID, xaiShared.XaiStatusExhausted, 0, "websocket_quota")
		}
	case http.StatusUnauthorized:
		pool.MarkFailed(accountID, xaiShared.XaiStatusBanned, 24*time.Hour, "websocket_unauthorized")
	}
}

type wsTurnError struct {
	Status     int
	RetryAfter time.Duration
	Payload    []byte
	Message    string
}

func (e *wsTurnError) Error() string {
	if e == nil {
		return "websocket upstream error"
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return strings.TrimSpace(string(e.Payload))
}

func parseWSTurnError(payload []byte) *wsTurnError {
	err := &wsTurnError{Payload: append([]byte(nil), payload...), Message: extractWSErrorMessage(payload)}
	for _, path := range []string{"status", "status_code", "error.status", "error.status_code", "response.error.status", "response.error.status_code"} {
		if value := gjson.GetBytes(payload, path); value.Exists() {
			err.Status = int(value.Int())
			if err.Status > 0 {
				break
			}
		}
	}
	for _, path := range []string{"retry_after", "error.retry_after", "response.error.retry_after"} {
		if value := gjson.GetBytes(payload, path); value.Exists() && value.Float() > 0 {
			err.RetryAfter = time.Duration(value.Float() * float64(time.Second))
			break
		}
	}
	classification := strings.ToLower(string(payload))
	if err.Status == 0 {
		switch {
		case strings.Contains(classification, "rate_limit"), strings.Contains(classification, "rate limit"), strings.Contains(classification, "too many requests"):
			err.Status = http.StatusTooManyRequests
		case strings.Contains(classification, "unauthorized"), strings.Contains(classification, "authentication"):
			err.Status = http.StatusUnauthorized
		case isQuotaLikeBody(payload), isFreeUsageExhaustedBody(payload):
			err.Status = http.StatusPaymentRequired
		}
	}
	return err
}

func applyWSTurnFailure(pool *xai.XaiAccountPool, accountID string, failure error) {
	if pool == nil || strings.TrimSpace(accountID) == "" || failure == nil {
		return
	}
	wsErr, ok := failure.(*wsTurnError)
	if !ok {
		return
	}
	switch wsErr.Status {
	case http.StatusTooManyRequests:
		cooldown := wsErr.RetryAfter
		if cooldown <= 0 {
			cooldown = time.Minute
		}
		if isFreeUsageExhaustedBody(wsErr.Payload) {
			cooldown = 24 * time.Hour
		}
		pool.CooldownWebsocketAccount(accountID, cooldown, "websocket_rate_limit")
	case http.StatusPaymentRequired, http.StatusForbidden:
		if isQuotaLikeBody(wsErr.Payload) || isFreeUsageExhaustedBody(wsErr.Payload) {
			pool.MarkFailed(accountID, xaiShared.XaiStatusExhausted, 0, "websocket_quota")
		}
	case http.StatusUnauthorized:
		pool.MarkFailed(accountID, xaiShared.XaiStatusBanned, 24*time.Hour, "websocket_unauthorized")
	}
}

func ensureOrDial(
	ctx context.Context,
	sess *xaiBackend.UpstreamConnSession,
	authID, wsURL string,
	dialFn func(context.Context) (*websocket.Conn, *http.Response, error),
	startReader func(*xaiBackend.UpstreamConnSession, *websocket.Conn),
) (*websocket.Conn, *http.Response, error) {
	if sess == nil {
		return dialFn(ctx)
	}
	return sess.EnsureUpstreamConn(ctx, authID, wsURL, dialFn, startReader)
}

func reconnectUpstream(
	ctx context.Context,
	sess *xaiBackend.UpstreamConnSession,
	authID, wsURL string,
	dialFn func(context.Context) (*websocket.Conn, *http.Response, error),
	startReader func(*xaiBackend.UpstreamConnSession, *websocket.Conn),
) (*websocket.Conn, *http.Response, error) {
	if sess != nil {
		sess.Close("reconnect")
	}
	return ensureOrDial(ctx, sess, authID, wsURL, dialFn, startReader)
}

func writeUpstream(sess *xaiBackend.UpstreamConnSession, conn *websocket.Conn, payload []byte) error {
	if conn == nil {
		return fmt.Errorf("upstream conn is nil")
	}
	_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if sess != nil {
		return sess.WriteMessage(conn, websocket.TextMessage, payload)
	}
	return conn.WriteMessage(websocket.TextMessage, payload)
}

// prepareWSTurn 准备 WS 出站 body。
// clientSession：客户端会话；upstreamSession：握手已用会话。
// 保证 body.prompt_cache_key 与 upstreamSession 对齐。
func prepareWSTurn(
	payload []byte,
	idState *xaiBackend.WebsocketIDState,
	clientSession, upstreamSession string,
) (out []byte, mapper *xaiBackend.RequestIDMapper, warmup bool, err error) {
	clientSession = strings.TrimSpace(clientSession)
	upstreamSession = strings.TrimSpace(upstreamSession)
	model := gjson.GetBytes(payload, "model").String()
	var body []byte

	// PrepareResponsesBody 用连接级 clientSession 作 explicit，强制多轮同源。
	prepared, prepErr := xaiBackend.PrepareResponsesBody(payload, xaiBackend.PrepareOptions{
		Stream:       true,
		Model:        model,
		SourceType:   "openai-response",
		SessionID:    clientSession,
		IsWebsocket:  true,
		KeepPrevious: true,
	})
	if prepErr != nil {
		return nil, nil, false, prepErr
	}
	body = prepared.Body
	// 与握手 upstreamSession 对齐（compat 下二者通常相同）
	align := upstreamSession
	if align == "" {
		align = prepared.SessionID
	}
	if align == "" {
		align = clientSession
	}
	if align != "" {
		body, _ = sjson.SetBytes(body, "prompt_cache_key", align)
	}
	if prev := strings.TrimSpace(gjson.GetBytes(payload, "previous_response_id").String()); prev != "" {
		body, _ = sjson.SetBytes(body, "previous_response_id", prev)
	}
	body = xaiBackend.BuildWebsocketRequestBody(body)
	mapper = xaiBackend.NewRequestIDMapper(idState, body)
	if mapper != nil {
		body = mapper.UpstreamRequestPayload(body)
	}
	warmup = xaiBackend.IsWebsocketWarmup(body)
	return body, mapper, warmup, nil
}

func pumpUpstreamTurn(
	ctx context.Context,
	sess *xaiBackend.UpstreamConnSession,
	conn *websocket.Conn,
	readCh chan xaiBackend.UpstreamRead,
	idState *xaiBackend.WebsocketIDState,
	mapper *xaiBackend.RequestIDMapper,
	reqBody []byte,
	warmup bool,
	downstream *websocket.Conn,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if sess != nil && readCh != nil {
		defer sess.ClearActive(readCh)
	}

	recordedTranscript := false
	outputItemsByIndex := make(map[int64][]byte)
	outputItemsFallback := make([][]byte, 0, 4)
	for {
		var (
			msgType int
			payload []byte
			readErr error
		)
		if sess != nil && readCh != nil {
			turnCtx, cancel := context.WithTimeout(ctx, xaiWSIdleTimeout)
			msgType, payload, readErr = sess.ReadMessage(turnCtx, conn, readCh)
			cancel()
		} else {
			_ = conn.SetReadDeadline(time.Now().Add(xaiWSIdleTimeout))
			msgType, payload, readErr = conn.ReadMessage()
		}
		if readErr != nil {
			return readErr
		}
		if msgType != websocket.TextMessage {
			continue
		}

		upstreamType := gjson.GetBytes(payload, "type").String()
		// 错误必须在任何
		// reasoning/ID 归一和下游写出之前转成内部错误。调用方会据此应用
		// cooldown，并只生成一个稳定的下游 error 事件，避免原始错误帧与
		// 本地 error 帧重复下发。
		if isWSTurnErrorPayload(payload) {
			return parseWSTurnError(payload)
		}
		switch upstreamType {
		case "response.output_item.done":
			xaiBackend.CollectOutputItemDone(payload, outputItemsByIndex, &outputItemsFallback)
		case "response.completed", "response.done":
			payload = xaiBackend.PatchCompletedOutput(payload, outputItemsByIndex, outputItemsFallback)
		}
		// 入站 ID 改写 + reasoning 归一
		if mapper != nil {
			payload = mapper.DownstreamResponsePayload(payload)
		}
		events := xaiBackend.NormalizeReasoningSummaryDataEvents(payload)
		for i, ev := range events {
			ev = xaiBackend.NormalizeReasoningSummaryData(ev)
			events[i] = ev
		}

		typ := gjson.GetBytes(payload, "type").String()
		switch typ {
		case "response.completed", "response.done":
			if !warmup && idState != nil && !recordedTranscript {
				idState.RecordTranscriptTurn(reqBody, payload)
				recordedTranscript = true
			}
		case "response.created":
			// warmup：在 created 后合成 completed
			if warmup {
				for _, ev := range events {
					if err := writeDownstream(downstream, ev); err != nil {
						return fmt.Errorf("downstream write: %w", err)
					}
				}
				synthetic := xaiBackend.BuildWarmupCompletedPayload(payload)
				if mapper != nil {
					synthetic = mapper.DownstreamResponsePayload(synthetic)
				}
				if err := writeDownstream(downstream, synthetic); err != nil {
					return fmt.Errorf("downstream write: %w", err)
				}
				return nil
			}
		}

		for _, ev := range events {
			if err := writeDownstream(downstream, ev); err != nil {
				return fmt.Errorf("downstream write: %w", err)
			}
		}
		if typ == "response.completed" || typ == "response.done" {
			return nil
		}
	}
}

func isWSTurnErrorPayload(payload []byte) bool {
	typ := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
	if typ == "error" {
		return true
	}
	return gjson.GetBytes(payload, "error").Exists()
}

func writeDownstream(conn *websocket.Conn, payload []byte) error {
	if conn == nil {
		return fmt.Errorf("downstream conn is nil")
	}
	_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	return conn.WriteMessage(websocket.TextMessage, payload)
}

func extractWSErrorMessage(payload []byte) string {
	for _, path := range []string{"error.message", "error.error", "message"} {
		if m := strings.TrimSpace(gjson.GetBytes(payload, path).String()); m != "" {
			return m
		}
	}
	return strings.TrimSpace(string(payload))
}

func (s *XaiService) handleWSCompaction(
	ctx context.Context,
	pool *xai.XaiAccountPool,
	config *xaiShared.XaiMultiConfig,
	account *xaiShared.XaiAccount,
	proxyURL, token string,
	idState *xaiBackend.WebsocketIDState,
	downstreamReq []byte,
	downstream *websocket.Conn,
) error {
	if idState == nil {
		return fmt.Errorf("compaction context unavailable")
	}
	transcript := idState.SnapshotTranscriptInput()
	if len(transcript) == 0 {
		return fmt.Errorf("compaction context is empty")
	}
	compactBody, err := xaiBackend.BuildCompactionPayload(downstreamReq, transcript)
	if err != nil {
		return err
	}
	// 走 HTTP compact（官方 API）
	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 0)
	result, execErr := xaiBackend.Execute(ctx, xaiBackend.Request{
		Method:      http.MethodPost,
		Path:        "/xai/v1/responses/compact",
		Body:        compactBody,
		IsStreaming: false,
		Model:       gjson.GetBytes(downstreamReq, "model").String(),
		Config:      config,
		Account:     account,
		AccessToken: token,
		Client:      client,
		Attempts:    1,
	})
	if execErr != nil && (result == nil || result.StatusCode == 0) {
		return execErr
	}
	if result == nil {
		return fmt.Errorf("empty compact result")
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return fmt.Errorf("compact upstream %d: %s", result.StatusCode, strings.TrimSpace(string(result.Body)))
	}

	responseID := xaiBackend.CompactionResponseID(result.Body)
	item := xaiBackend.CompactionOutputItem(result.Body, responseID)
	idState.ReplaceTranscriptWithItems(item)
	idState.MapDownstreamToUpstream(responseID, "")

	model := gjson.GetBytes(downstreamReq, "model").String()
	events := xaiBackend.BuildCompactionTriggerWSEvents(compactBody, xaiBackend.BaseModelName(model), result.Body)
	if len(events) == 0 {
		// 兜底 created+completed
		now := time.Now().Unix()
		events = [][]byte{
			[]byte(fmt.Sprintf(`{"type":"response.created","sequence_number":0,"response":{"id":%q,"object":"response","created_at":%d,"status":"in_progress","output":[]}}`, responseID, now)),
			[]byte(fmt.Sprintf(`{"type":"response.completed","sequence_number":1,"response":{"id":%q,"object":"response","created_at":%d,"completed_at":%d,"status":"completed","output":[%s]}}`, responseID, now, now, string(item))),
		}
	}
	for _, ev := range events {
		if err := writeDownstream(downstream, ev); err != nil {
			return err
		}
	}
	return nil
}

func writeWSError(conn *websocket.Conn, message string) error {
	if conn == nil {
		return nil
	}
	payload, _ := json.Marshal(map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    "server_error",
			"message": message,
		},
	})
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.WriteMessage(websocket.TextMessage, payload)
}

func dialXaiWebsocket(ctx context.Context, wsURL string, headers http.Header, proxyURL string) (*websocket.Conn, *http.Response, error) {
	dialer := newXaiWebsocketDialer(proxyURL)
	if ctx == nil {
		ctx = context.Background()
	}
	return dialer.DialContext(ctx, wsURL, headers)
}

func newXaiWebsocketDialer(proxyURL string) *websocket.Dialer {
	dialer := &websocket.Dialer{
		Proxy:             http.ProxyFromEnvironment,
		HandshakeTimeout:  xaiWSHandshakeTimeout,
		EnableCompression: false,
		NetDialContext: (&net.Dialer{
			Timeout:   executor.DefaultHTTPDialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}

	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return dialer
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		logger.Warn("[xAI] parse websocket proxy URL failed: %v", err)
		return dialer
	}
	switch parsed.Scheme {
	case "socks5":
		var auth *proxy.Auth
		if parsed.User != nil {
			username := parsed.User.Username()
			password, _ := parsed.User.Password()
			auth = &proxy.Auth{User: username, Password: password}
		}
		socksDialer, err := proxy.SOCKS5("tcp", parsed.Host, auth, proxy.Direct)
		if err != nil {
			logger.Warn("[xAI] create websocket SOCKS5 dialer failed: %v", err)
			return dialer
		}
		dialer.Proxy = nil
		dialer.NetDialContext = func(_ context.Context, network, addr string) (net.Conn, error) {
			return socksDialer.Dial(network, addr)
		}
	case "http", "https":
		dialer.Proxy = http.ProxyURL(parsed)
	default:
		logger.Warn("[xAI] unsupported websocket proxy scheme: %s", parsed.Scheme)
	}
	return dialer
}
