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
	defer untrackWSConn(accountID, downstream)

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
	sessionID := strings.TrimSpace(r.Header.Get("x-grok-conv-id"))
	if sessionID == "" {
		sessionID = strings.TrimSpace(r.Header.Get("X-Grok-Conv-Id"))
	}
	if sessionID == "" {
		// 无 conv id 时用连接级临时 key（不跨重连复用）
		sessionID = fmt.Sprintf("ephemeral-%s-%d", accountID, time.Now().UnixNano())
	}
	headers := xaiBackend.ApplyWebsocketHeadersWithAccount(token, sessionID, config, account)

	// ID state + 上游 conn session
	idState := xaiBackend.GlobalWebsocketIDStore().Get(sessionID)
	if idState == nil {
		idState = xaiBackend.NewWebsocketIDState()
	}
	sessKey := xaiBackend.SessionKey(sessionID, accountID)
	connSess := xaiBackend.GlobalUpstreamConnStore().GetOrCreate(sessKey)
	if connSess != nil {
		connSess.SetAuthMeta(accountID, sessionID)
		connSess.Acquire()
		defer func() {
			if n := connSess.Release(); n <= 0 {
				// 无下游附着时关闭上游，ID/transcript 仍保留在 idStore
				connSess.Close("client_detached")
			}
		}()
		connSess.LockRequest()
		defer connSess.UnlockRequest()
	}

	dialFn := func(c context.Context) (*websocket.Conn, *http.Response, error) {
		return dialXaiWebsocket(c, wsURL, headers, proxyURL)
	}
	startReader := func(sess *xaiBackend.UpstreamConnSession, conn *websocket.Conn) {
		sess.ReadLoop(conn)
	}

	upstream, resp, err := ensureOrDial(ctx, connSess, accountID, wsURL, dialFn, startReader)
	if err != nil {
		msg := err.Error()
		if resp != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if len(body) > 0 {
				msg = fmt.Sprintf("websocket dial failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
			}
		}
		_ = writeWSError(downstream, msg)
		return
	}
	pool.ReportSuccess(account.ID)

	// 主循环：读下游 → 处理一轮（含 compact/warmup）→ 写上游 → 读到 completed
	for {
		_ = downstream.SetReadDeadline(time.Now().Add(xaiWSIdleTimeout))
		msgType, payload, readErr := downstream.ReadMessage()
		if readErr != nil {
			return
		}
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}

		// 控制类消息原样转发
		eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
		if eventType != "" && eventType != "response.create" {
			if writeErr := writeUpstream(connSess, upstream, payload); writeErr != nil {
				// 尝试重连再写
				upstream, _, err = reconnectUpstream(ctx, connSess, accountID, wsURL, dialFn, startReader)
				if err != nil || writeUpstream(connSess, upstream, payload) != nil {
					_ = writeWSError(downstream, "upstream write failed: "+writeErr.Error())
					return
				}
			}
			continue
		}

		// compaction_trigger → HTTP compact + 伪 WS 事件
		if xaiBackend.InputHasCompactionTrigger(payload) {
			if err := s.handleWSCompaction(ctx, pool, config, account, proxyURL, token, idState, payload, downstream); err != nil {
				_ = writeWSError(downstream, err.Error())
			}
			continue
		}

		// 准备 + ID 映射
		reqBody, mapper, warmup, prepErr := prepareWSTurn(payload, idState)
		if prepErr != nil {
			_ = writeWSError(downstream, prepErr.Error())
			continue
		}

		// 先挂 active reader，再写上游，避免 response 在 SetActive 前到达被丢弃
		var readCh chan xaiBackend.UpstreamRead
		if connSess != nil {
			readCh = make(chan xaiBackend.UpstreamRead, 4096)
			connSess.SetActive(readCh)
		}

		// 写上游（失败则 invalidate + 重连重试一次）
		if writeErr := writeUpstream(connSess, upstream, reqBody); writeErr != nil {
			if connSess != nil {
				connSess.ClearActive(readCh)
				connSess.InvalidateConn(upstream, "send_error", writeErr, true)
			}
			upstream, _, err = reconnectUpstream(ctx, connSess, accountID, wsURL, dialFn, startReader)
			if err != nil {
				_ = writeWSError(downstream, "upstream reconnect failed: "+err.Error())
				return
			}
			if connSess != nil {
				readCh = make(chan xaiBackend.UpstreamRead, 4096)
				connSess.SetActive(readCh)
			}
			if writeErr = writeUpstream(connSess, upstream, reqBody); writeErr != nil {
				if connSess != nil {
					connSess.ClearActive(readCh)
				}
				_ = writeWSError(downstream, "upstream write failed: "+writeErr.Error())
				return
			}
		}

		// 读本轮直到 completed / error（或 warmup 合成 completed）
		if err := pumpUpstreamTurn(ctx, connSess, upstream, readCh, idState, mapper, reqBody, warmup, downstream); err != nil {
			// 上游断线：invalidate，等待下一轮下游消息时重连
			if connSess != nil {
				connSess.InvalidateConn(upstream, "turn_error", err, true)
			}
			// 尝试立即重连供后续轮次
			if u2, _, e2 := reconnectUpstream(ctx, connSess, accountID, wsURL, dialFn, startReader); e2 == nil {
				upstream = u2
			}
			// 非致命：把错误回给客户端，保持下游连接
			if !strings.Contains(err.Error(), "downstream") {
				_ = writeWSError(downstream, err.Error())
			} else {
				return
			}
		}
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

func prepareWSTurn(payload []byte, idState *xaiBackend.WebsocketIDState) (out []byte, mapper *xaiBackend.RequestIDMapper, warmup bool, err error) {
	model := gjson.GetBytes(payload, "model").String()
	prepared, err := xaiBackend.PrepareResponsesBody(payload, xaiBackend.PrepareOptions{
		Stream:       true,
		Model:        model,
		IsWebsocket:  true,
		KeepPrevious: true,
	})
	if err != nil {
		return nil, nil, false, err
	}
	body := prepared.Body
	// 保留客户端 previous_response_id（Prepare 在 KeepPrevious 时不删）
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
		case "response.completed":
			if !warmup && idState != nil && !recordedTranscript {
				idState.RecordTranscriptTurn(reqBody, payload)
				recordedTranscript = true
			}
		case "response.created":
			// warmup：在 created 后合成 completed
			if warmup {
				// 先把 created 发给下游
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
		case "error":
			for _, ev := range events {
				_ = writeDownstream(downstream, ev)
			}
			return fmt.Errorf("%s", extractWSErrorMessage(payload))
		}

		for _, ev := range events {
			if err := writeDownstream(downstream, ev); err != nil {
				return fmt.Errorf("downstream write: %w", err)
			}
		}
		if typ == "response.completed" {
			return nil
		}
	}
}

func writeDownstream(conn *websocket.Conn, payload []byte) error {
	if conn == nil {
		return fmt.Errorf("downstream conn is nil")
	}
	_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	return conn.WriteMessage(websocket.TextMessage, payload)
}

func extractWSErrorMessage(payload []byte) string {
	if m := strings.TrimSpace(gjson.GetBytes(payload, "error.message").String()); m != "" {
		return m
	}
	if m := strings.TrimSpace(gjson.GetBytes(payload, "error.error").String()); m != "" {
		return m
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
