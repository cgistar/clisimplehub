package backend

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
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
