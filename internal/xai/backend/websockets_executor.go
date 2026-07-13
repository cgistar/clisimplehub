package backend

// WebSocket transport。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	xaiShared "clisimplehub/internal/xai/shared"
)

// 按 session 复用上游连接。
type UpstreamConnSession struct {
	SessionID string // store key（session\x00account）
	AuthID    string
	ConvID    string // 下游会话 id（用于清理 ID state）
	WSURL     string

	reqMu   sync.Mutex
	connMu  sync.Mutex
	writeMu sync.Mutex
	conn    *websocket.Conn

	readerConn *websocket.Conn

	activeMu     sync.Mutex
	activeCh     chan UpstreamRead
	activeDone   <-chan struct{}
	activeCancel context.CancelFunc

	refs int // 下游附着计数

	disconnectOnce sync.Once
	disconnectCh   chan error
}

// UpstreamRead 上游读事件。
type UpstreamRead struct {
	Conn    *websocket.Conn
	MsgType int
	Payload []byte
	Err     error
}

// UpstreamConnStore 全局上游 session 表。
type UpstreamConnStore struct {
	mu       sync.Mutex
	sessions map[string]*UpstreamConnSession
}

func NewUpstreamConnStore() *UpstreamConnStore {
	return &UpstreamConnStore{sessions: make(map[string]*UpstreamConnSession)}
}

var globalUpstreamConnStore = NewUpstreamConnStore()

func GlobalUpstreamConnStore() *UpstreamConnStore { return globalUpstreamConnStore }

// SessionKey 用 conv + account 绑定上游连接。
func SessionKey(sessionID, accountID string) string {
	sessionID = strings.TrimSpace(sessionID)
	accountID = strings.TrimSpace(accountID)
	if sessionID == "" {
		return ""
	}
	if accountID == "" {
		return sessionID
	}
	return sessionID + "\x00" + accountID
}

func (s *UpstreamConnStore) GetOrCreate(key string) *UpstreamConnSession {
	key = strings.TrimSpace(key)
	if s == nil || key == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string]*UpstreamConnSession)
	}
	if sess := s.sessions[key]; sess != nil {
		return sess
	}
	// 解析 session\x00account
	convID, authID := splitSessionKey(key)
	sess := &UpstreamConnSession{
		SessionID:    key,
		AuthID:       authID,
		ConvID:       convID,
		disconnectCh: make(chan error, 1),
	}
	s.sessions[key] = sess
	return sess
}

func splitSessionKey(key string) (convID, authID string) {
	if i := strings.IndexByte(key, 0); i >= 0 {
		return key[:i], key[i+1:]
	}
	return key, ""
}

func (s *UpstreamConnStore) Delete(key string) *UpstreamConnSession {
	key = strings.TrimSpace(key)
	if s == nil || key == "" {
		return nil
	}
	s.mu.Lock()
	sess := s.sessions[key]
	delete(s.sessions, key)
	s.mu.Unlock()
	return sess
}

// CloseSessionsForAuthID 关闭指定账号的全部上游 WS session（删账号时调用）。
func (s *UpstreamConnStore) CloseSessionsForAuthID(authID, reason string) int {
	authID = strings.TrimSpace(authID)
	if s == nil || authID == "" {
		return 0
	}
	s.mu.Lock()
	toClose := make([]*UpstreamConnSession, 0)
	for key, sess := range s.sessions {
		if sess == nil {
			continue
		}
		id := strings.TrimSpace(sess.AuthID)
		if id == "" {
			_, id = splitSessionKey(key)
		}
		if id == authID {
			toClose = append(toClose, sess)
			delete(s.sessions, key)
		}
	}
	s.mu.Unlock()
	for _, sess := range toClose {
		if sess.ConvID != "" {
			GlobalWebsocketIDStore().Delete(sess.ConvID)
		}
		sess.Close(reason)
	}
	return len(toClose)
}

// SetAuthMeta 更新 session 的账号/会话元数据。
func (s *UpstreamConnSession) SetAuthMeta(authID, convID string) {
	if s == nil {
		return
	}
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if authID != "" {
		s.AuthID = authID
	}
	if convID != "" {
		s.ConvID = convID
	}
}

// LockRequest / UnlockRequest 串行化同 session 上的出站请求
func (s *UpstreamConnSession) LockRequest() {
	if s == nil {
		return
	}
	s.reqMu.Lock()
}

func (s *UpstreamConnSession) UnlockRequest() {
	if s == nil {
		return
	}
	s.reqMu.Unlock()
}

func (s *UpstreamConnSession) Acquire() {
	if s == nil {
		return
	}
	s.connMu.Lock()
	s.refs++
	s.connMu.Unlock()
}

func (s *UpstreamConnSession) Release() int {
	if s == nil {
		return 0
	}
	s.connMu.Lock()
	if s.refs > 0 {
		s.refs--
	}
	n := s.refs
	s.connMu.Unlock()
	return n
}

func (s *UpstreamConnSession) SetActive(ch chan UpstreamRead) {
	if s == nil {
		return
	}
	s.activeMu.Lock()
	if s.activeCancel != nil {
		s.activeCancel()
		s.activeCancel = nil
		s.activeDone = nil
	}
	s.activeCh = ch
	if ch != nil {
		ctx, cancel := context.WithCancel(context.Background())
		s.activeDone = ctx.Done()
		s.activeCancel = cancel
	}
	s.activeMu.Unlock()
}

func (s *UpstreamConnSession) ClearActive(ch chan UpstreamRead) {
	if s == nil {
		return
	}
	s.activeMu.Lock()
	if s.activeCh == ch {
		s.activeCh = nil
		if s.activeCancel != nil {
			s.activeCancel()
		}
		s.activeCancel = nil
		s.activeDone = nil
	}
	s.activeMu.Unlock()
}

func (s *UpstreamConnSession) WriteMessage(conn *websocket.Conn, msgType int, payload []byte) error {
	if s == nil {
		if conn == nil {
			return fmt.Errorf("websocket conn is nil")
		}
		return conn.WriteMessage(msgType, payload)
	}
	if conn == nil {
		return fmt.Errorf("websocket conn is nil")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return conn.WriteMessage(msgType, payload)
}

func (s *UpstreamConnSession) ConfigureConn(conn *websocket.Conn) {
	if s == nil || conn == nil {
		return
	}
	conn.SetPingHandler(func(appData string) error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(10*time.Second))
	})
}

func (s *UpstreamConnSession) NotifyDisconnect(err error) {
	if s == nil {
		return
	}
	s.disconnectOnce.Do(func() {
		if s.disconnectCh == nil {
			return
		}
		select {
		case s.disconnectCh <- err:
		default:
		}
		close(s.disconnectCh)
	})
}

// EnsureUpstreamConn 复用或 dial 上游。dialFn 负责实际握手。
func (s *UpstreamConnSession) EnsureUpstreamConn(
	ctx context.Context,
	authID, wsURL string,
	dialFn func(ctx context.Context) (*websocket.Conn, *http.Response, error),
	startReader func(sess *UpstreamConnSession, conn *websocket.Conn),
) (*websocket.Conn, *http.Response, error) {
	if s == nil {
		return dialFn(ctx)
	}

	s.connMu.Lock()
	conn := s.conn
	readerConn := s.readerConn
	s.connMu.Unlock()
	if conn != nil {
		if readerConn != conn {
			s.connMu.Lock()
			s.readerConn = conn
			s.connMu.Unlock()
			s.ConfigureConn(conn)
			if startReader != nil {
				go startReader(s, conn)
			}
		}
		return conn, nil, nil
	}

	conn, resp, err := dialFn(ctx)
	if err != nil {
		return nil, resp, err
	}

	s.connMu.Lock()
	if s.conn != nil {
		previous := s.conn
		s.connMu.Unlock()
		_ = conn.Close()
		return previous, nil, nil
	}
	s.conn = conn
	s.WSURL = wsURL
	s.AuthID = authID
	s.readerConn = conn
	// 重置 disconnect channel（重连后）
	s.disconnectOnce = sync.Once{}
	s.disconnectCh = make(chan error, 1)
	s.connMu.Unlock()

	s.ConfigureConn(conn)
	if startReader != nil {
		go startReader(s, conn)
	}
	return conn, resp, nil
}

// InvalidateConn 丢弃当前上游连接。
func (s *UpstreamConnSession) InvalidateConn(conn *websocket.Conn, reason string, err error, notify bool) {
	if s == nil || conn == nil {
		return
	}
	s.connMu.Lock()
	current := s.conn
	if current == nil || current != conn {
		s.connMu.Unlock()
		return
	}
	s.conn = nil
	if s.readerConn == conn {
		s.readerConn = nil
	}
	s.connMu.Unlock()

	if notify {
		s.NotifyDisconnect(err)
	}
	_ = conn.Close()
	_ = reason
}

// Close 关闭上游连接。
func (s *UpstreamConnSession) Close(reason string) {
	if s == nil {
		return
	}
	s.connMu.Lock()
	conn := s.conn
	s.conn = nil
	if s.readerConn == conn {
		s.readerConn = nil
	}
	s.connMu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	_ = reason
}

// ReadLoop 持续读上游并投递到 active channel。
func (s *UpstreamConnSession) ReadLoop(conn *websocket.Conn) {
	if s == nil || conn == nil {
		return
	}
	for {
		msgType, payload, errRead := conn.ReadMessage()
		if errRead != nil {
			s.activeMu.Lock()
			ch := s.activeCh
			done := s.activeDone
			s.activeMu.Unlock()
			if ch != nil {
				select {
				case ch <- UpstreamRead{Conn: conn, Err: errRead}:
				case <-done:
				default:
				}
				s.ClearActive(ch)
				close(ch)
			}
			s.InvalidateConn(conn, "upstream_disconnected", errRead, true)
			return
		}
		if msgType != websocket.TextMessage {
			if msgType == websocket.BinaryMessage {
				errBinary := fmt.Errorf("unexpected binary websocket message")
				s.activeMu.Lock()
				ch := s.activeCh
				done := s.activeDone
				s.activeMu.Unlock()
				if ch != nil {
					select {
					case ch <- UpstreamRead{Conn: conn, Err: errBinary}:
					case <-done:
					default:
					}
					s.ClearActive(ch)
					close(ch)
				}
				s.InvalidateConn(conn, "unexpected_binary", errBinary, true)
				return
			}
			continue
		}
		s.activeMu.Lock()
		ch := s.activeCh
		done := s.activeDone
		s.activeMu.Unlock()
		if ch == nil {
			continue // 无活跃下游请求时丢弃（或可缓冲）
		}
		select {
		case ch <- UpstreamRead{Conn: conn, MsgType: msgType, Payload: payload}:
		case <-done:
		}
	}
}

// ReadMessage 从 session 读（或直读 conn）。
func (s *UpstreamConnSession) ReadMessage(ctx context.Context, conn *websocket.Conn, readCh chan UpstreamRead) (int, []byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		if conn == nil {
			return 0, nil, fmt.Errorf("websocket conn is nil")
		}
		return conn.ReadMessage()
	}
	if conn == nil {
		return 0, nil, fmt.Errorf("websocket conn is nil")
	}
	if readCh == nil {
		return 0, nil, fmt.Errorf("session read channel is nil")
	}
	for {
		select {
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		case ev, ok := <-readCh:
			if !ok {
				return 0, nil, fmt.Errorf("session read channel closed")
			}
			if ev.Conn != nil && ev.Conn != conn {
				continue
			}
			if ev.Err != nil {
				return 0, nil, ev.Err
			}
			return ev.MsgType, ev.Payload, nil
		}
	}
}

// WebsocketIDState 维护会话级 previous_response_id 映射与 transcript。
type WebsocketIDState struct {
	mu                   sync.Mutex
	downstreamToUpstream map[string]string
	sequence             int
	transcriptInput      []json.RawMessage
}

func NewWebsocketIDState() *WebsocketIDState {
	return &WebsocketIDState{downstreamToUpstream: make(map[string]string)}
}

type WebsocketIDStore struct {
	mu       sync.Mutex
	sessions map[string]*WebsocketIDState
}

func NewWebsocketIDStore() *WebsocketIDStore {
	return &WebsocketIDStore{sessions: make(map[string]*WebsocketIDState)}
}

var globalWSIDStore = NewWebsocketIDStore()

func GlobalWebsocketIDStore() *WebsocketIDStore { return globalWSIDStore }

func (s *WebsocketIDStore) Get(sessionID string) *WebsocketIDState {
	sessionID = strings.TrimSpace(sessionID)
	if s == nil || sessionID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string]*WebsocketIDState)
	}
	st := s.sessions[sessionID]
	if st == nil {
		st = NewWebsocketIDState()
		s.sessions[sessionID] = st
	}
	return st
}

func (s *WebsocketIDStore) Delete(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if s == nil || sessionID == "" {
		return
	}
	s.mu.Lock()
	delete(s.sessions, sessionID)
	s.mu.Unlock()
}

// RequestIDMapper 单次 response.create 的上下游 ID 映射器。
type RequestIDMapper struct {
	state                *WebsocketIDState
	downstreamPreviousID string
	upstreamPreviousID   string
	upstreamResponseID   string
	downstreamResponseID string
}

func NewRequestIDMapper(state *WebsocketIDState, downstreamRequest []byte) *RequestIDMapper {
	if state == nil {
		return nil
	}
	downstreamPreviousID := strings.TrimSpace(gjson.GetBytes(downstreamRequest, "previous_response_id").String())
	upstreamPreviousID := downstreamPreviousID
	if downstreamPreviousID != "" {
		upstreamPreviousID = state.UpstreamIDForDownstream(downstreamPreviousID)
	}
	return &RequestIDMapper{
		state:                state,
		downstreamPreviousID: downstreamPreviousID,
		upstreamPreviousID:   upstreamPreviousID,
	}
}

func (s *WebsocketIDState) UpstreamIDForDownstream(downstreamID string) string {
	downstreamID = strings.TrimSpace(downstreamID)
	if s == nil || downstreamID == "" {
		return downstreamID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if upstreamID, ok := s.downstreamToUpstream[downstreamID]; ok {
		return strings.TrimSpace(upstreamID)
	}
	return downstreamID
}

func (s *WebsocketIDState) MapDownstreamToUpstream(downstreamID, upstreamID string) {
	downstreamID = strings.TrimSpace(downstreamID)
	if s == nil || downstreamID == "" {
		return
	}
	s.mu.Lock()
	if s.downstreamToUpstream == nil {
		s.downstreamToUpstream = make(map[string]string)
	}
	s.downstreamToUpstream[downstreamID] = strings.TrimSpace(upstreamID)
	s.mu.Unlock()
}

func (s *WebsocketIDState) SnapshotTranscriptInput() []byte {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.transcriptInput) == 0 {
		return nil
	}
	return marshalRawMessages(s.transcriptInput)
}

func (s *WebsocketIDState) PrependTranscriptInput(payload []byte) []byte {
	if s == nil || len(payload) == 0 {
		return payload
	}
	s.mu.Lock()
	prefix := make([]json.RawMessage, 0, len(s.transcriptInput))
	for _, item := range s.transcriptInput {
		prefix = append(prefix, bytes.Clone(item))
	}
	s.mu.Unlock()
	if len(prefix) == 0 {
		return payload
	}
	current := jsonRawMessages(gjson.GetBytes(payload, "input"))
	merged := append(prefix, current...)
	out, err := sjson.SetRawBytes(payload, "input", marshalRawMessages(merged))
	if err != nil {
		return payload
	}
	return out
}

func (s *WebsocketIDState) RecordTranscriptTurn(requestPayload, completedPayload []byte) {
	if s == nil || len(requestPayload) == 0 || len(completedPayload) == 0 {
		return
	}
	inputItems := jsonRawMessages(gjson.GetBytes(requestPayload, "input"))
	outputItems := jsonRawMessages(gjson.GetBytes(completedPayload, "response.output"))
	if len(outputItems) == 0 {
		// completed 可能直接是 response 对象
		outputItems = jsonRawMessages(gjson.GetBytes(completedPayload, "output"))
	}
	if len(inputItems) == 0 && len(outputItems) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(gjson.GetBytes(requestPayload, "previous_response_id").String()) == "" {
		s.transcriptInput = nil
	}
	s.transcriptInput = append(s.transcriptInput, inputItems...)
	s.transcriptInput = append(s.transcriptInput, outputItems...)
}

func (s *WebsocketIDState) ReplaceTranscriptWithItems(items ...[]byte) {
	if s == nil {
		return
	}
	next := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		item = bytes.TrimSpace(item)
		if len(item) == 0 || !json.Valid(item) {
			continue
		}
		next = append(next, bytes.Clone(item))
	}
	s.mu.Lock()
	s.transcriptInput = next
	s.mu.Unlock()
}

// UpstreamRequestPayload 出站：下游 previous_response_id → 上游 id。
func (m *RequestIDMapper) UpstreamRequestPayload(payload []byte) []byte {
	if m == nil || len(payload) == 0 {
		return payload
	}
	if m.downstreamPreviousID == m.upstreamPreviousID {
		// 仍可能需要 strip instructions
		if m.upstreamPreviousID != "" && gjson.GetBytes(payload, "instructions").Exists() {
			payload, _ = sjson.DeleteBytes(payload, "instructions")
		}
		return payload
	}
	if m.upstreamPreviousID == "" {
		out, err := sjson.DeleteBytes(payload, "previous_response_id")
		if err != nil {
			return payload
		}
		if m.downstreamPreviousID != "" && m.state != nil {
			out = m.state.PrependTranscriptInput(out)
		}
		if gjson.GetBytes(out, "instructions").Exists() {
			out, _ = sjson.DeleteBytes(out, "instructions")
		}
		return out
	}
	out, err := sjson.SetBytes(payload, "previous_response_id", m.upstreamPreviousID)
	if err != nil {
		return payload
	}
	if gjson.GetBytes(out, "instructions").Exists() {
		out, _ = sjson.DeleteBytes(out, "instructions")
	}
	return out
}

// DownstreamResponsePayload 入站：上游 id → 下游 id 全树改写。
func (m *RequestIDMapper) DownstreamResponsePayload(payload []byte) []byte {
	if m == nil || len(payload) == 0 {
		return payload
	}
	upstreamResponseID := strings.TrimSpace(gjson.GetBytes(payload, "response.id").String())
	if upstreamResponseID == "" {
		upstreamResponseID = strings.TrimSpace(gjson.GetBytes(payload, "id").String())
	}
	downstreamResponseID := m.DownstreamIDForUpstreamResponse(upstreamResponseID)
	if downstreamResponseID == "" {
		return payload
	}
	return rewriteDownstreamIDs(payload, m.upstreamResponseID, downstreamResponseID, m.upstreamPreviousID, m.downstreamPreviousID)
}

func (m *RequestIDMapper) DownstreamIDForUpstreamResponse(upstreamResponseID string) string {
	upstreamResponseID = strings.TrimSpace(upstreamResponseID)
	if m == nil || m.state == nil {
		return upstreamResponseID
	}
	if m.upstreamResponseID != "" {
		return m.downstreamResponseID
	}
	if upstreamResponseID == "" {
		return ""
	}

	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.upstreamResponseID = upstreamResponseID
	m.downstreamResponseID = upstreamResponseID
	if m.state.downstreamToUpstream == nil {
		m.state.downstreamToUpstream = make(map[string]string)
	}
	_, seen := m.state.downstreamToUpstream[upstreamResponseID]
	if (m.downstreamPreviousID != "" && m.upstreamPreviousID != "" && upstreamResponseID == m.upstreamPreviousID) || seen {
		m.state.sequence++
		m.downstreamResponseID = fmt.Sprintf("%s-xai-%d", upstreamResponseID, m.state.sequence)
	}
	m.state.downstreamToUpstream[upstreamResponseID] = upstreamResponseID
	m.state.downstreamToUpstream[m.downstreamResponseID] = upstreamResponseID
	return m.downstreamResponseID
}

func rewriteDownstreamIDs(payload []byte, upstreamResponseID, downstreamResponseID, upstreamPreviousID, downstreamPreviousID string) []byte {
	upstreamResponseID = strings.TrimSpace(upstreamResponseID)
	downstreamResponseID = strings.TrimSpace(downstreamResponseID)
	upstreamPreviousID = strings.TrimSpace(upstreamPreviousID)
	downstreamPreviousID = strings.TrimSpace(downstreamPreviousID)
	if len(payload) == 0 || (upstreamResponseID == downstreamResponseID && upstreamPreviousID == downstreamPreviousID) {
		return payload
	}
	var value any
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return payload
	}
	if !rewriteIDValue(value, upstreamResponseID, downstreamResponseID, upstreamPreviousID, downstreamPreviousID, "") {
		return payload
	}
	out, err := json.Marshal(value)
	if err != nil {
		return payload
	}
	return out
}

func rewriteIDValue(value any, upstreamResponseID, downstreamResponseID, upstreamPreviousID, downstreamPreviousID, key string) bool {
	switch typed := value.(type) {
	case map[string]any:
		changed := false
		for childKey, childValue := range typed {
			if childString, ok := childValue.(string); ok {
				replaced := rewriteIDString(childString, childKey, upstreamResponseID, downstreamResponseID, upstreamPreviousID, downstreamPreviousID)
				if replaced != childString {
					typed[childKey] = replaced
					changed = true
				}
				continue
			}
			if rewriteIDValue(childValue, upstreamResponseID, downstreamResponseID, upstreamPreviousID, downstreamPreviousID, childKey) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for i := range typed {
			if rewriteIDValue(typed[i], upstreamResponseID, downstreamResponseID, upstreamPreviousID, downstreamPreviousID, key) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func rewriteIDString(value, key, upstreamResponseID, downstreamResponseID, upstreamPreviousID, downstreamPreviousID string) string {
	switch key {
	case "id", "item_id":
		if upstreamResponseID != "" && downstreamResponseID != "" && downstreamResponseID != upstreamResponseID && strings.Contains(value, upstreamResponseID) {
			return strings.ReplaceAll(value, upstreamResponseID, downstreamResponseID)
		}
	case "previous_response_id":
		if upstreamPreviousID != "" && downstreamPreviousID != "" && value == upstreamPreviousID {
			return downstreamPreviousID
		}
	}
	return value
}

func jsonRawMessages(result gjson.Result) []json.RawMessage {
	if !result.Exists() || !result.IsArray() {
		return nil
	}
	items := result.Array()
	out := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		raw := bytes.TrimSpace([]byte(item.Raw))
		if len(raw) == 0 || !json.Valid(raw) {
			continue
		}
		out = append(out, bytes.Clone(raw))
	}
	return out
}

func marshalRawMessages(items []json.RawMessage) []byte {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(bytes.TrimSpace(item))
	}
	buf.WriteByte(']')
	return buf.Bytes()
}

// BuildWebsocketRequestBody 规范化 WS 出站 body。
func BuildWebsocketRequestBody(body []byte) []byte {
	if len(body) == 0 {
		return nil
	}
	out := bytes.Clone(body)
	out, _ = sjson.SetBytes(out, "type", "response.create")
	out, _ = sjson.DeleteBytes(out, "stream")
	out, _ = sjson.DeleteBytes(out, "stream_options")
	out, _ = sjson.DeleteBytes(out, "background")
	out, _ = sjson.SetBytes(out, "store", true)
	if strings.TrimSpace(gjson.GetBytes(out, "previous_response_id").String()) != "" {
		out, _ = sjson.DeleteBytes(out, "instructions")
	}
	return out
}

// IsWebsocketWarmup 是否 generate:false 预热请求。
func IsWebsocketWarmup(payload []byte) bool {
	generate := gjson.GetBytes(payload, "generate")
	return generate.Exists() && !generate.Bool()
}

// BuildWarmupCompletedPayload 伪造 warmup 的 response.completed。
func BuildWarmupCompletedPayload(createdPayload []byte) []byte {
	completed := []byte(`{"type":"response.completed","response":{"output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}}`)
	if sequence := gjson.GetBytes(createdPayload, "sequence_number"); sequence.Exists() {
		completed, _ = sjson.SetBytes(completed, "sequence_number", sequence.Int()+1)
	}
	if response := gjson.GetBytes(createdPayload, "response"); response.Exists() && response.IsObject() {
		responsePayload := []byte(response.Raw)
		responsePayload, _ = sjson.SetBytes(responsePayload, "status", "completed")
		if !gjson.GetBytes(responsePayload, "output").Exists() {
			responsePayload, _ = sjson.SetRawBytes(responsePayload, "output", []byte("[]"))
		}
		if !gjson.GetBytes(responsePayload, "usage").Exists() {
			responsePayload, _ = sjson.SetRawBytes(responsePayload, "usage", []byte(`{"input_tokens":0,"output_tokens":0,"total_tokens":0}`))
		}
		completed, _ = sjson.SetRawBytes(completed, "response", responsePayload)
	}
	return completed
}

// InputHasCompactionTrigger 检测 compaction_trigger。
func InputHasCompactionTrigger(body []byte) bool {
	return InputHasItemType(body, "compaction_trigger")
}

// BuildCompactionPayload 用 transcript 构造 compact 请求。
func BuildCompactionPayload(payload []byte, transcriptInput []byte) ([]byte, error) {
	out := bytes.Clone(payload)
	if len(transcriptInput) == 0 {
		transcriptInput = []byte("[]")
	}
	var err error
	out, err = sjson.SetRawBytes(out, "input", transcriptInput)
	if err != nil {
		return nil, err
	}
	out, _ = sjson.DeleteBytes(out, "previous_response_id")
	return out, nil
}

// CompactionOutputItem 从 compact 响应提取 compaction item。
func CompactionOutputItem(compactData []byte, responseID string) []byte {
	itemResult := gjson.GetBytes(compactData, "output.0")
	item := []byte(`{"type":"compaction"}`)
	if itemResult.Exists() && itemResult.Type == gjson.JSON {
		item = []byte(itemResult.Raw)
	}
	if !gjson.GetBytes(item, "type").Exists() {
		item, _ = sjson.SetBytes(item, "type", "compaction")
	}
	if !gjson.GetBytes(item, "id").Exists() {
		if strings.HasPrefix(responseID, "resp_") {
			item, _ = sjson.SetBytes(item, "id", "cmp_"+strings.TrimPrefix(responseID, "resp_"))
		} else if responseID != "" {
			item, _ = sjson.SetBytes(item, "id", "cmp_"+responseID)
		}
	}
	return item
}

// CompactionResponseID 从 compact 响应取 id。
func CompactionResponseID(compactData []byte) string {
	if responseID := strings.TrimSpace(gjson.GetBytes(compactData, "id").String()); responseID != "" {
		if strings.HasPrefix(responseID, "resp_") {
			return responseID
		}
		return "resp_" + strings.TrimPrefix(responseID, "cmp_")
	}
	return fmt.Sprintf("resp_xai_compaction_%d", timeNowUnixNano())
}

// timeNowUnixNano 便于测试替换；默认 time.Now().UnixNano。
var timeNowUnixNano = func() int64 {
	return time.Now().UnixNano()
}

// ApplyWebsocketHeaders 握手头；勿设 Connection/Upgrade/Content-Type。
func ApplyWebsocketHeaders(token string, sessionID string, config *xaiShared.XaiMultiConfig) http.Header {
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	if strings.TrimSpace(token) != "" {
		headers.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	if sid := strings.TrimSpace(sessionID); sid != "" {
		headers.Set(HeaderGrokConvID, sid)
	}
	applyWSCustomHeaders(headers, config, nil)
	return headers
}

// ApplyWebsocketHeadersWithAccount 握手头，含账号级 custom headers。
func ApplyWebsocketHeadersWithAccount(token, sessionID string, config *xaiShared.XaiMultiConfig, account *xaiShared.XaiAccount) http.Header {
	headers := ApplyWebsocketHeaders(token, sessionID, config)
	if account != nil {
		applyWSCustomHeaders(headers, nil, account.CustomHeaders)
	}
	return headers
}

func applyWSCustomHeaders(headers http.Header, config *xaiShared.XaiMultiConfig, accountHeaders map[string]string) {
	if headers == nil {
		return
	}
	applyOne := func(src map[string]string) {
		for k, v := range src {
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if k == "" || v == "" {
				continue
			}
			switch strings.ToLower(k) {
			case "connection", "upgrade", "sec-websocket-key", "sec-websocket-version",
				"sec-websocket-extensions", "sec-websocket-protocol", "content-length",
				"content-type", "accept":
				continue
			}
			headers.Set(k, v)
		}
	}
	if config != nil {
		applyOne(config.Config.CustomHeaders)
	}
	applyOne(accountHeaders)
}

// headerGet 兼容 http.Header 字面量未规范化的 key。
