package codexplugin

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

	codex "clisimplehub/internal/codex"
	codexBackend "clisimplehub/internal/codex/backend"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/executor"
	"clisimplehub/internal/logger"
	"clisimplehub/internal/plugin"

	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type codexWebsocketTurnRequest struct {
	Path            string
	Model           string
	Body            []byte
	OriginalBody    []byte
	ClientHeaders   http.Header
	EndpointHeaders map[string]string
	Account         *codexShared.CodexAccount
	Config          *codexShared.CodexMultiConfig
}

type codexWebsocketStream struct {
	Headers http.Header
	Chunks  <-chan codexWebsocketChunk
}

type codexWebsocketChunk struct {
	Payload []byte
	Err     error
}

var (
	errCodexHTTPStreamCompleted = errors.New("codex HTTP stream completed")
	errCodexHTTPUpstreamEvent   = errors.New("codex HTTP upstream error event")
)

type codexWebsocketRead struct {
	conn    *websocket.Conn
	msgType int
	payload []byte
	err     error
}

type codexWebsocketSessionStore struct {
	mu       sync.Mutex
	sessions map[string]*codexWebsocketSession
}

type codexWebsocketSession struct {
	sessionID string

	reqMu sync.Mutex

	connMu    sync.Mutex
	conn      *websocket.Conn
	accountID string

	writeMu sync.Mutex

	activeMu     sync.Mutex
	activeCh     chan codexWebsocketRead
	activeDone   <-chan struct{}
	activeCancel context.CancelFunc

	upstreamDisconnectOnce sync.Once
	upstreamDisconnectCh   chan error
}

type CodexWebsocketsExecutor struct {
	service *CodexService
	store   *codexWebsocketSessionStore
}

func NewCodexWebsocketsExecutor(service *CodexService) *CodexWebsocketsExecutor {
	return &CodexWebsocketsExecutor{
		service: service,
		store: &codexWebsocketSessionStore{
			sessions: make(map[string]*codexWebsocketSession),
		},
	}
}

func (e *CodexWebsocketsExecutor) ExecuteStream(ctx context.Context, sessionID string, req codexWebsocketTurnRequest) (*codexWebsocketStream, error) {
	if e == nil || e.service == nil {
		return nil, errors.New("codex websockets executor is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if req.Account == nil {
		return nil, errors.New("codex websockets executor requires an account")
	}
	if req.Config == nil {
		req.Config = &codexShared.CodexMultiConfig{}
	}
	reporter := newCodexWebsocketUsageReporter(ctx, e.service, req)
	var stream *codexWebsocketStream
	var err error
	if req.Account.Websockets {
		stream, err = e.executeWebsocketStream(ctx, sessionID, req, reporter)
		if err != nil && isCodexWebsocketUpgradeRequired(err) {
			stream, err = e.executeHTTPStream(ctx, sessionID, req, reporter)
		}
	} else {
		stream, err = e.executeHTTPStream(ctx, sessionID, req, reporter)
	}
	if err != nil {
		reporter.PublishFailure(codexWebsocketErrorStatus(err), err)
	}
	return stream, err
}

func (e *CodexWebsocketsExecutor) executeWebsocketStream(ctx context.Context, sessionID string, req codexWebsocketTurnRequest, reporter *codexUsageReporter) (*codexWebsocketStream, error) {
	pool := codex.GetPool()
	if pool == nil {
		return nil, errors.New("codex pool not initialized")
	}
	proxyURL := resolveCodexAccountProxyURL(pool, req.Account)
	authMgr := e.service.GetOrCreateAuthManager(req.Account.ID, pool.ConfigPath(), proxyURL)
	accessToken, accountID, err := authMgr.GetAccessToken()
	if err != nil {
		markCodexWebsocketAuthError(pool, req.Account, err)
		return nil, newCodexWebsocketExecutionError(http.StatusUnauthorized, true, fmt.Errorf("auth failed: %v", err))
	}

	upstreamURL, err := buildCodexResponsesWebsocketURL(codexBackend.UpstreamURL(req.Config, codexBackend.TargetPath(req.Path)))
	if err != nil {
		return nil, err
	}
	preparedBody, upstreamHeaders, identityState, err := codexBackend.PrepareWebsocket(ctx, codexBackend.Request{
		Path:                   req.Path,
		Source:                 codexBackend.SourceCodex,
		Model:                  req.Model,
		Body:                   req.Body,
		OriginalBody:           req.OriginalBody,
		Headers:                req.ClientHeaders,
		Config:                 req.Config,
		EndpointHeaders:        req.EndpointHeaders,
		AccessToken:            accessToken,
		AccountID:              accountID,
		LocalAccountID:         req.Account.ID,
		PlanType:               req.Account.PlanType,
		DisableImageGeneration: plugin.GetAppDisableImageGeneration(),
	})
	if err != nil {
		return nil, err
	}
	reporter.SetTranslatedRequest(preparedBody)
	reporter.SetUsageRefresh(func() {
		e.service.scheduleUsageSnapshotRefresh(req.Account, accessToken, accountID, proxyURL, req.Config)
	})

	sess := e.getOrCreateSession(sessionID)
	if sess == nil {
		return nil, errors.New("codex websockets executor requires a session id")
	}
	sess.reqMu.Lock()
	unlock := true
	defer func() {
		if unlock {
			sess.reqMu.Unlock()
		}
	}()

	conn, handshakeResp, err := e.ensureUpstreamConn(ctx, sess, req.Account.ID, upstreamURL, upstreamHeaders, proxyURL)
	responseHeaders := cloneHTTPHeaders(handshakeResp)
	if err != nil {
		handshakeBody := readAndCloseHTTPResponseBody(handshakeResp)
		if handshakeResp != nil && handshakeResp.StatusCode > 0 {
			markCodexWebsocketHandshakeStatus(pool, req.Account, handshakeResp, handshakeBody)
			message := fmt.Errorf("codex websocket upstream returned %d", handshakeResp.StatusCode)
			if handshakeResp.StatusCode == http.StatusUpgradeRequired {
				message = fmt.Errorf("codex websocket upgrade required: %s", strings.TrimSpace(string(handshakeBody)))
				if strings.TrimSpace(string(handshakeBody)) == "" {
					message = errors.New("codex websocket upgrade required")
				}
			} else if len(handshakeBody) > 0 {
				message = fmt.Errorf("codex websocket upstream returned %d: %s", handshakeResp.StatusCode, strings.TrimSpace(string(handshakeBody)))
			}
			return nil, newCodexWebsocketExecutionError(handshakeResp.StatusCode, isCodexWebsocketRetryableStatus(handshakeResp.StatusCode), message).withHeaders(handshakeResp.Header)
		}
		return nil, newCodexWebsocketExecutionError(0, true, fmt.Errorf("codex websocket dial failed: %v", err))
	}
	closeHTTPResponseBody(handshakeResp)
	reporter.ObserveHeaders(responseHeaders)
	reporter.StartResponseTTFT()

	readCh := make(chan codexWebsocketRead, 4096)
	sess.setActive(readCh)
	if err = sess.writeMessage(conn, websocket.TextMessage, preparedBody); err != nil {
		sess.clearActive(readCh)
		e.invalidateUpstreamConn(sess, conn, "send_error", err)

		conn, handshakeResp, err = e.ensureUpstreamConn(ctx, sess, req.Account.ID, upstreamURL, upstreamHeaders, proxyURL)
		if err != nil {
			body := readAndCloseHTTPResponseBody(handshakeResp)
			status := 0
			if handshakeResp != nil {
				status = handshakeResp.StatusCode
			}
			executionErr := newCodexWebsocketExecutionError(status, true, fmt.Errorf("codex websocket reconnect failed: %v: %s", err, strings.TrimSpace(string(body))))
			if handshakeResp != nil {
				executionErr.withHeaders(handshakeResp.Header)
			}
			return nil, executionErr
		}
		if len(responseHeaders) == 0 {
			responseHeaders = cloneHTTPHeaders(handshakeResp)
		}
		closeHTTPResponseBody(handshakeResp)
		reporter.ObserveHeaders(cloneHTTPHeaders(handshakeResp))
		readCh = make(chan codexWebsocketRead, 4096)
		sess.setActive(readCh)
		if err = sess.writeMessage(conn, websocket.TextMessage, preparedBody); err != nil {
			sess.clearActive(readCh)
			e.invalidateUpstreamConn(sess, conn, "send_retry_error", err)
			return nil, newCodexWebsocketExecutionError(0, true, err)
		}
	}

	out := make(chan codexWebsocketChunk)
	stream := &codexWebsocketStream{Headers: responseHeaders, Chunks: out}
	unlock = false
	go func() {
		defer close(out)
		defer sess.clearActive(readCh)
		defer sess.reqMu.Unlock()

		for {
			msgType, payload, readErr := readCodexWebsocketMessage(ctx, conn, readCh)
			if readErr != nil {
				mappedErr := mapCodexWebsocketReadError(readErr)
				reporter.PublishFailure(codexWebsocketErrorStatus(mappedErr), mappedErr)
				e.sendChunk(ctx, out, codexWebsocketChunk{Err: mappedErr})
				return
			}
			if msgType == websocket.BinaryMessage {
				protocolErr := errors.New("codex websockets executor: unexpected binary message")
				reporter.PublishFailure(http.StatusBadGateway, protocolErr)
				e.invalidateUpstreamConn(sess, conn, "unexpected_binary", protocolErr)
				e.sendChunk(ctx, out, codexWebsocketChunk{Err: protocolErr})
				return
			}
			if msgType != websocket.TextMessage {
				continue
			}

			payload = bytes.TrimSpace(payload)
			if len(payload) == 0 {
				continue
			}
			reporter.MarkFirstResponseByte()
			payload = codexBackend.ExposeIdentityPayload(payload, identityState)
			reporter.ObservePayload(payload)
			eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
			switch {
			case eventType == "error":
				reporter.PublishFailure(http.StatusBadGateway, newCodexWebsocketUpstreamError(payload))
			case isResponsesWebsocketTerminalEvent(eventType):
				reporter.PublishSuccess(payload)
			}
			if !e.sendChunk(ctx, out, codexWebsocketChunk{Payload: payload}) {
				if ctx.Err() != nil {
					reporter.PublishFailure(codexWebsocketErrorStatus(ctx.Err()), ctx.Err())
				}
				return
			}

			if eventType == "error" {
				upstreamErr := newCodexWebsocketUpstreamError(payload)
				markCodexWebsocketUpstreamError(pool, req.Account, upstreamErr)
				e.invalidateUpstreamConn(sess, conn, "upstream_error", upstreamErr)
				return
			}
			if isResponsesWebsocketTerminalEvent(eventType) {
				pool.ReportSuccess(req.Account.ID)
				return
			}
		}
	}()
	return stream, nil
}

func (e *CodexWebsocketsExecutor) executeHTTPStream(ctx context.Context, sessionID string, req codexWebsocketTurnRequest, reporter *codexUsageReporter) (*codexWebsocketStream, error) {
	pool := codex.GetPool()
	if pool == nil {
		return nil, errors.New("codex pool not initialized")
	}

	sess := e.getOrCreateSession(sessionID)
	if sess == nil {
		return nil, errors.New("codex websockets executor requires a session id")
	}
	sess.reqMu.Lock()
	unlock := true
	defer func() {
		if unlock {
			sess.reqMu.Unlock()
		}
	}()

	body := translateResponsesRequestForCodexHTTP(req.Body, req.ClientHeaders)
	result, retryable := e.service.roundTripWithAccount(ctx, req.Account, &executor.UpstreamRequest{
		Method:              http.MethodPost,
		TargetPath:          req.Path,
		Headers:             req.ClientHeaders,
		Body:                body,
		IsStreaming:         true,
		RequestModel:        req.Model,
		OriginalPath:        req.Path,
		TargetInterfaceType: "codex",
		Endpoint: &executor.EndpointConfig{
			Headers: cloneStringMapLocal(req.EndpointHeaders),
		},
	}, pool, req.Config, reporter)
	if result == nil {
		return nil, newCodexWebsocketExecutionError(0, retryable, errors.New("codex HTTP fallback returned nil result"))
	}
	if result.StatusCode >= http.StatusBadRequest || result.Error != nil {
		message := result.Error
		if message == nil {
			message = fmt.Errorf("codex HTTP fallback returned %d: %s", result.StatusCode, strings.TrimSpace(string(result.Body)))
		}
		if result.Stream != nil {
			_ = result.Stream.Close()
		}
		return nil, newCodexWebsocketExecutionError(result.StatusCode, retryable, message).withHeaders(result.Headers)
	}

	var source io.ReadCloser
	if result.Stream != nil {
		source = result.Stream
	} else {
		source = io.NopCloser(bytes.NewReader(result.Body))
	}
	out := make(chan codexWebsocketChunk)
	unlock = false
	go func() {
		defer close(out)
		defer sess.reqMu.Unlock()
		defer source.Close()
		err := decodeCodexSSEStream(source, func(payload []byte) error {
			reporter.MarkFirstResponseByte()
			reporter.ObservePayload(payload)
			eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
			switch eventType {
			case "error":
				reporter.PublishFailure(http.StatusBadGateway, newCodexWebsocketUpstreamError(payload))
			case "response.completed", "response.done":
				reporter.PublishSuccess(payload)
			}
			payload = normalizeCodexHTTPFallbackCompletion(payload)
			if !e.sendChunk(ctx, out, codexWebsocketChunk{Payload: payload}) {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return io.ErrClosedPipe
			}
			switch eventType {
			case "error":
				return errCodexHTTPUpstreamEvent
			case "response.completed", "response.done":
				return errCodexHTTPStreamCompleted
			}
			return nil
		})
		switch {
		case errors.Is(err, errCodexHTTPStreamCompleted):
			pool.ReportSuccess(req.Account.ID)
			return
		case errors.Is(err, errCodexHTTPUpstreamEvent):
			return
		case err == nil:
			streamErr := errors.New("codex HTTP fallback stream closed before response.completed")
			reporter.PublishFailure(http.StatusBadGateway, streamErr)
			e.sendChunk(ctx, out, codexWebsocketChunk{Err: streamErr})
			return
		case !errors.Is(err, context.Canceled):
			reporter.PublishFailure(codexWebsocketErrorStatus(err), err)
			e.sendChunk(ctx, out, codexWebsocketChunk{Err: err})
			return
		default:
			reporter.PublishFailure(codexWebsocketErrorStatus(err), err)
			return
		}
	}()
	return &codexWebsocketStream{Headers: result.Headers.Clone(), Chunks: out}, nil
}

func (e *CodexWebsocketsExecutor) sendChunk(ctx context.Context, out chan<- codexWebsocketChunk, chunk codexWebsocketChunk) bool {
	if ctx == nil {
		out <- chunk
		return true
	}
	select {
	case out <- chunk:
		return true
	case <-ctx.Done():
		return false
	}
}

func mapCodexWebsocketReadError(err error) error {
	if err == nil {
		return nil
	}
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) && closeErr.Code == websocket.CloseMessageTooBig {
		return newCodexWebsocketExecutionError(
			http.StatusRequestEntityTooLarge,
			false,
			errors.New(`{"error":{"message":"upstream websocket message too big","type":"invalid_request_error","code":"message_too_big"}}`),
		)
	}
	return err
}

func isCodexWebsocketUpgradeRequired(err error) bool {
	if err == nil {
		return false
	}
	var executionErr *codexWebsocketExecutionError
	if errors.As(err, &executionErr) && executionErr != nil && executionErr.status == http.StatusUpgradeRequired {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "upgrade required") || strings.Contains(msg, "websocket upgrade required")
}

func (s *codexWebsocketSession) setActive(ch chan codexWebsocketRead) {
	if s == nil {
		return
	}
	s.activeMu.Lock()
	if s.activeCancel != nil {
		s.activeCancel()
	}
	s.activeCh = ch
	s.activeDone = nil
	s.activeCancel = nil
	if ch != nil {
		activeCtx, cancel := context.WithCancel(context.Background())
		s.activeDone = activeCtx.Done()
		s.activeCancel = cancel
	}
	s.activeMu.Unlock()
}

func (s *codexWebsocketSession) clearActive(ch chan codexWebsocketRead) {
	if s == nil {
		return
	}
	s.activeMu.Lock()
	if s.activeCh == ch {
		s.activeCh = nil
		if s.activeCancel != nil {
			s.activeCancel()
		}
		s.activeDone = nil
		s.activeCancel = nil
	}
	s.activeMu.Unlock()
}

func (s *codexWebsocketSession) writeMessage(conn *websocket.Conn, msgType int, payload []byte) error {
	if s == nil || conn == nil {
		return errors.New("codex websockets executor: websocket connection is nil")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return conn.WriteMessage(msgType, payload)
}

func (s *codexWebsocketSession) configureConn(conn *websocket.Conn) {
	if s == nil || conn == nil {
		return
	}
	conn.EnableWriteCompression(false)
	conn.SetPingHandler(func(appData string) error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(10*time.Second))
	})
}

func (s *codexWebsocketSession) notifyUpstreamDisconnect(err error) {
	if s == nil {
		return
	}
	s.upstreamDisconnectOnce.Do(func() {
		select {
		case s.upstreamDisconnectCh <- err:
		default:
		}
		close(s.upstreamDisconnectCh)
	})
}

func (e *CodexWebsocketsExecutor) getOrCreateSession(sessionID string) *codexWebsocketSession {
	if e == nil || e.store == nil {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	e.store.mu.Lock()
	defer e.store.mu.Unlock()
	if sess := e.store.sessions[sessionID]; sess != nil {
		return sess
	}
	sess := &codexWebsocketSession{
		sessionID:            sessionID,
		upstreamDisconnectCh: make(chan error, 1),
	}
	e.store.sessions[sessionID] = sess
	return sess
}

func (e *CodexWebsocketsExecutor) UpstreamDisconnectChan(sessionID string) <-chan error {
	sess := e.getOrCreateSession(sessionID)
	if sess == nil {
		return nil
	}
	return sess.upstreamDisconnectCh
}

func (e *CodexWebsocketsExecutor) ensureUpstreamConn(ctx context.Context, sess *codexWebsocketSession, accountID, wsURL string, headers http.Header, proxyURL string) (*websocket.Conn, *http.Response, error) {
	if sess == nil {
		return nil, nil, errors.New("codex websockets executor: session is nil")
	}
	sess.connMu.Lock()
	conn := sess.conn
	existingAccountID := sess.accountID
	sess.connMu.Unlock()
	if conn != nil {
		if existingAccountID != strings.TrimSpace(accountID) {
			return nil, nil, fmt.Errorf("codex websockets executor: session is pinned to account %s", existingAccountID)
		}
		return conn, nil, nil
	}

	conn, resp, err := dialCodexWebsocket(ctx, wsURL, headers, proxyURL)
	if err != nil {
		return nil, resp, err
	}
	sess.connMu.Lock()
	if sess.conn != nil {
		previous := sess.conn
		sess.connMu.Unlock()
		_ = conn.Close()
		return previous, resp, nil
	}
	sess.conn = conn
	sess.accountID = strings.TrimSpace(accountID)
	sess.connMu.Unlock()

	sess.configureConn(conn)
	go e.readUpstreamLoop(sess, conn)
	return conn, resp, nil
}

func (e *CodexWebsocketsExecutor) readUpstreamLoop(sess *codexWebsocketSession, conn *websocket.Conn) {
	for {
		_ = conn.SetReadDeadline(time.Now().Add(codexResponsesWebsocketIdleTimeout))
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			sess.activeMu.Lock()
			ch := sess.activeCh
			done := sess.activeDone
			sess.activeMu.Unlock()
			if ch != nil {
				select {
				case ch <- codexWebsocketRead{conn: conn, err: err}:
				case <-done:
				default:
				}
			}
			e.invalidateUpstreamConn(sess, conn, "upstream_disconnected", err)
			return
		}
		sess.activeMu.Lock()
		ch := sess.activeCh
		done := sess.activeDone
		sess.activeMu.Unlock()
		if ch == nil {
			continue
		}
		select {
		case ch <- codexWebsocketRead{conn: conn, msgType: msgType, payload: payload}:
		case <-done:
		}
	}
}

func readCodexWebsocketMessage(ctx context.Context, conn *websocket.Conn, readCh <-chan codexWebsocketRead) (int, []byte, error) {
	if conn == nil || readCh == nil {
		return 0, nil, errors.New("codex websockets executor: session reader is not initialized")
	}
	for {
		select {
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		case event := <-readCh:
			if event.conn != conn {
				continue
			}
			return event.msgType, event.payload, event.err
		}
	}
}

func (e *CodexWebsocketsExecutor) invalidateUpstreamConn(sess *codexWebsocketSession, conn *websocket.Conn, reason string, err error) {
	if sess == nil || conn == nil {
		return
	}
	sess.connMu.Lock()
	if sess.conn != conn {
		sess.connMu.Unlock()
		return
	}
	sess.conn = nil
	sess.connMu.Unlock()
	sess.notifyUpstreamDisconnect(err)
	if closeErr := conn.Close(); closeErr != nil {
		logger.Warn("[Codex] close websocket session (%s) failed: %v", reason, closeErr)
	}
}

func (e *CodexWebsocketsExecutor) CloseExecutionSession(sessionID string) {
	if e == nil || e.store == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	e.store.mu.Lock()
	sess := e.store.sessions[sessionID]
	delete(e.store.sessions, sessionID)
	e.store.mu.Unlock()
	closeCodexWebsocketSession(sess)
}

func (e *CodexWebsocketsExecutor) Close() {
	if e == nil || e.store == nil {
		return
	}
	e.store.mu.Lock()
	sessions := make([]*codexWebsocketSession, 0, len(e.store.sessions))
	for id, sess := range e.store.sessions {
		delete(e.store.sessions, id)
		sessions = append(sessions, sess)
	}
	e.store.mu.Unlock()
	for _, sess := range sessions {
		closeCodexWebsocketSession(sess)
	}
}

func closeCodexWebsocketSession(sess *codexWebsocketSession) {
	if sess == nil {
		return
	}
	sess.activeMu.Lock()
	if sess.activeCancel != nil {
		sess.activeCancel()
	}
	sess.activeCh = nil
	sess.activeDone = nil
	sess.activeCancel = nil
	sess.activeMu.Unlock()

	sess.connMu.Lock()
	conn := sess.conn
	sess.conn = nil
	sess.connMu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

func cloneHTTPHeaders(resp *http.Response) http.Header {
	if resp == nil || resp.Header == nil {
		return nil
	}
	return resp.Header.Clone()
}

func cloneStringMapLocal(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func closeHTTPResponseBody(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
}

type codexWebsocketExecutionError struct {
	status    int
	retryable bool
	headers   http.Header
	err       error
}

func (e *codexWebsocketExecutionError) withHeaders(headers http.Header) *codexWebsocketExecutionError {
	if e != nil && headers != nil {
		e.headers = headers.Clone()
	}
	return e
}

func newCodexWebsocketExecutionError(status int, retryable bool, err error) *codexWebsocketExecutionError {
	return &codexWebsocketExecutionError{status: status, retryable: retryable, err: err}
}

func (e *codexWebsocketExecutionError) Error() string {
	if e == nil || e.err == nil {
		return "codex websocket execution failed"
	}
	return e.err.Error()
}

func (e *codexWebsocketExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func translateResponsesRequestForCodexHTTP(rawJSON []byte, headers http.Header) []byte {
	body := bytes.Clone(rawJSON)
	lite := codexBackend.IsResponsesLiteRequest(body, headers)
	input := gjson.GetBytes(body, "input")
	if input.Type == gjson.String {
		message := []byte(`[{"type":"message","role":"user","content":[{"type":"input_text","text":""}]}]`)
		message, _ = sjson.SetBytes(message, "0.content.0.text", input.String())
		body, _ = sjson.SetRawBytes(body, "input", message)
	}
	body, _ = sjson.SetBytes(body, "stream", true)
	body, _ = sjson.SetBytes(body, "store", false)
	body, _ = sjson.SetBytes(body, "parallel_tool_calls", !lite)
	body, _ = sjson.SetBytes(body, "include", []string{"reasoning.encrypted_content"})
	for _, field := range []string{
		"max_output_tokens",
		"max_completion_tokens",
		"temperature",
		"top_p",
		"truncation",
		"user",
		"context_management",
	} {
		body, _ = sjson.DeleteBytes(body, field)
	}
	if tier := gjson.GetBytes(body, "service_tier"); tier.Exists() && tier.String() != "priority" {
		body, _ = sjson.DeleteBytes(body, "service_tier")
	}
	body = convertCodexSystemRoles(body)
	body = normalizeCodexBuiltinTools(body)
	return body
}

func convertCodexSystemRoles(body []byte) []byte {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body
	}
	for index, item := range input.Array() {
		if item.Get("type").String() == "message" && item.Get("role").String() == "system" {
			body, _ = sjson.SetBytes(body, fmt.Sprintf("input.%d.role", index), "developer")
		}
	}
	return body
}

func normalizeCodexBuiltinTools(body []byte) []byte {
	tools := gjson.GetBytes(body, "tools")
	for index, tool := range tools.Array() {
		if isCodexWebSearchPreview(tool.Get("type").String()) {
			body, _ = sjson.SetBytes(body, fmt.Sprintf("tools.%d.type", index), "web_search")
		}
	}
	if isCodexWebSearchPreview(gjson.GetBytes(body, "tool_choice.type").String()) {
		body, _ = sjson.SetBytes(body, "tool_choice.type", "web_search")
	}
	choiceTools := gjson.GetBytes(body, "tool_choice.tools")
	for index, tool := range choiceTools.Array() {
		if isCodexWebSearchPreview(tool.Get("type").String()) {
			body, _ = sjson.SetBytes(body, fmt.Sprintf("tool_choice.tools.%d.type", index), "web_search")
		}
	}
	return body
}

func isCodexWebSearchPreview(toolType string) bool {
	return toolType == "web_search_preview" || toolType == "web_search_preview_2025_03_11"
}

func decodeCodexSSEStream(reader io.Reader, emit func([]byte) error) error {
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
			return fmt.Errorf("codex HTTP fallback returned invalid SSE JSON: %s", data)
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

func sortedPendingToolCallIDs(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	for value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
