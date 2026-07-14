package codexplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	codex "clisimplehub/internal/codex"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/executor"
	"clisimplehub/internal/logger"
	"clisimplehub/internal/plugin"
	"clisimplehub/internal/storage"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"golang.org/x/net/proxy"
)

const (
	codexResponsesWebsocketIdleTimeout = 5 * time.Minute
	codexResponsesWebsocketHandshakeTO = 30 * time.Second

	codexLocalCompactionSummaryPrefix = "Another language model started to solve this problem and produced a summary of its thinking process. You also have access to the state of the tools that were used by that language model. Use this to build on the work that has already been done and avoid duplicating work. Here is the summary produced by the other language model, use the information in this summary to assist with your own analysis:"
)

var codexWebsocketUpgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

type codexDownstreamWebsocketRead struct {
	msgType int
	payload []byte
}

func isCodexWebsocketRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket")
}

func (s *CodexService) HandleResponsesWebsocket(w http.ResponseWriter, r *http.Request, endpoint *storage.Endpoint) {
	downstreamConn, err := codexWebsocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer downstreamConn.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	executionSessionID := uuid.NewString()
	sessionKey := websocketDownstreamSessionKey(r)
	if sessionKey == "" {
		sessionKey = executionSessionID
	}
	retainResponsesWebsocketToolCaches(sessionKey)
	defer releaseResponsesWebsocketToolCaches(sessionKey)
	if s.websocketExec != nil {
		defer s.websocketExec.CloseExecutionSession(executionSessionID)
	}

	downstreamReads := make(chan codexDownstreamWebsocketRead, 16)
	go readCodexDownstreamWebsocket(ctx, cancel, downstreamConn, downstreamReads)
	s.proxyResponsesWebsocketTurns(ctx, downstreamConn, downstreamReads, r.Header.Clone(), r.URL.Path, sessionKey, executionSessionID, endpoint)
}

func readCodexDownstreamWebsocket(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, reads chan<- codexDownstreamWebsocketRead) {
	defer cancel()
	for {
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		select {
		case reads <- codexDownstreamWebsocketRead{msgType: msgType, payload: payload}:
		case <-ctx.Done():
			return
		}
	}
}

func (s *CodexService) proxyResponsesWebsocketTurns(ctx context.Context, downstream *websocket.Conn, downstreamReads <-chan codexDownstreamWebsocketRead, clientHeaders http.Header, requestPath, sessionKey, executionSessionID string, endpoint *storage.Endpoint) {
	if s == nil || s.websocketExec == nil {
		_ = writeCodexWebsocketError(downstream, http.StatusServiceUnavailable, errors.New("codex websocket provider is not initialized"))
		return
	}
	var lastRequest []byte
	lastResponseOutput := []byte("[]")
	lastResponseID := ""
	var lastResponsePendingToolCallIDs []string
	pinnedAccountID := ""
	pinnedWebsocket := false
	forceTranscriptReplayNextRequest := false

	for {
		var downstreamRead codexDownstreamWebsocketRead
		upstreamDisconnectCh := s.websocketExec.UpstreamDisconnectChan(executionSessionID)
		select {
		case <-ctx.Done():
			return
		case <-upstreamDisconnectCh:
			return
		case downstreamRead = <-downstreamReads:
		}
		msgType := downstreamRead.msgType
		payload := downstreamRead.payload
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}

		pool := codex.GetPool()
		if pool == nil || s.websocketExec == nil {
			_ = writeCodexWebsocketError(downstream, http.StatusServiceUnavailable, errors.New("codex websocket provider is not initialized"))
			return
		}
		config, loadErr := loadCodexWebsocketConfig(pool)
		if loadErr != nil {
			_ = writeCodexWebsocketError(downstream, http.StatusInternalServerError, loadErr)
			continue
		}

		activeAccountID := strings.TrimSpace(pool.ActiveAccountID())
		activeAccountChanged := pinnedAccountID != "" &&
			pool.Mode() != codexShared.RotationLoadBalance &&
			activeAccountID != "" &&
			activeAccountID != pinnedAccountID

		pinnedAccount := pool.SelectByID(pinnedAccountID)
		if pinnedAccountID != "" && (activeAccountChanged || pinnedAccount == nil) {
			pinnedAccountID = ""
			pinnedWebsocket = false
			forceTranscriptReplayNextRequest = true
			s.websocketExec.CloseExecutionSession(executionSessionID)
			pinnedAccount = nil
		}
		if pinnedAccount != nil {
			pinnedAccount.Websockets = pinnedWebsocket
		}
		// 纯 WS 池或已 pin 的 WS 账号走上游 passthrough；force transcript replay 时退回 merge。
		usePassthrough := codexWebsocketUsesUpstreamPassthrough(pool, pinnedAccount, pinnedWebsocket, forceTranscriptReplayNextRequest)
		allowIncremental := pinnedAccount != nil && pinnedWebsocket && !forceTranscriptReplayNextRequest

		// 本 turn 开始前的会话快照：pin 释放时回滚，避免失败 turn 污染 merge 历史。
		previousLastRequest := bytes.Clone(lastRequest)
		previousLastResponseOutput := bytes.Clone(lastResponseOutput)
		previousLastResponseID := lastResponseID
		previousLastResponsePendingToolCallIDs := append([]string(nil), lastResponsePendingToolCallIDs...)

		var requestJSON, httpRequestJSON, nextLastRequest []byte
		var err error
		if usePassthrough {
			fallbackModel := strings.TrimSpace(gjson.GetBytes(lastRequest, "model").String())
			requestJSON, err = normalizeResponsesWebsocketPassthroughRequest(payload, fallbackModel)
			if err != nil {
				_ = writeCodexWebsocketError(downstream, http.StatusBadRequest, err)
				continue
			}
			requestJSON = ensureResponsesWebsocketInheritedFields(requestJSON, lastRequest)
			// Codex CLI 的 response.append 常省略 previous_response_id，用会话状态补齐。
			if strings.TrimSpace(gjson.GetBytes(requestJSON, "previous_response_id").String()) == "" {
				prev := strings.TrimSpace(lastResponseID)
				if prev != "" && inputSatisfiesPendingToolCalls(gjson.GetBytes(requestJSON, "input"), lastResponsePendingToolCallIDs) {
					requestJSON, _ = sjson.SetBytes(requestJSON, "previous_response_id", prev)
				}
			}
			// HTTP fallback / 后续 force-replay 仍准备 merge 视图；失败时不阻断 passthrough。
			if mergedWS, mergedHTTP, mergedNext, mergeErr := normalizeResponsesWebsocketRequestWithIncrementalState(
				payload,
				lastRequest,
				lastResponseOutput,
				lastResponseID,
				lastResponsePendingToolCallIDs,
				false,
			); mergeErr == nil {
				httpRequestJSON = mergedHTTP
				nextLastRequest = mergedNext
				if len(httpRequestJSON) == 0 {
					httpRequestJSON = bytes.Clone(mergedWS)
				}
			} else {
				httpRequestJSON = bytes.Clone(requestJSON)
				nextLastRequest = bytes.Clone(requestJSON)
			}
		} else {
			requestJSON, httpRequestJSON, nextLastRequest, err = normalizeResponsesWebsocketRequestWithIncrementalState(
				payload,
				lastRequest,
				lastResponseOutput,
				lastResponseID,
				lastResponsePendingToolCallIDs,
				allowIncremental,
			)
			if err != nil {
				_ = writeCodexWebsocketError(downstream, http.StatusBadRequest, err)
				continue
			}
		}
		requestJSON = applyCodexWebsocketEndpointModel(requestJSON, endpoint)
		httpRequestJSON = applyCodexWebsocketEndpointModel(httpRequestJSON, endpoint)
		nextLastRequest = applyCodexWebsocketEndpointModel(nextLastRequest, endpoint)
		// passthrough 信任客户端增量载荷，不做 tool-call repair；merge 路径保持修复。
		if !usePassthrough {
			if strings.TrimSpace(gjson.GetBytes(requestJSON, "previous_response_id").String()) == "" {
				requestJSON = repairResponsesWebsocketToolCalls(sessionKey, requestJSON)
				requestJSON = dedupeResponsesWebsocketInput(requestJSON)
			}
			nextLastRequest = repairResponsesWebsocketToolCalls(sessionKey, nextLastRequest)
			nextLastRequest = dedupeResponsesWebsocketInput(nextLastRequest)
			// merge 路径：本 turn 先提交 lastRequest（失败时靠快照回滚）。
			lastRequest = bytes.Clone(nextLastRequest)
		}
		httpRequestJSON = repairResponsesWebsocketToolCalls(sessionKey, httpRequestJSON)
		httpRequestJSON = dedupeResponsesWebsocketInput(httpRequestJSON)

		accountSelector := newCodexWebsocketTurnAccountSelector(pool, pinnedAccount)
		// passthrough 增量 body 不适合直接打 HTTP /responses，避免同账号 HTTP 假降级。
		accountSelector.disableHTTPFallback = usePassthrough
		account := accountSelector.Next()
		if account == nil {
			_ = writeCodexWebsocketError(downstream, http.StatusServiceUnavailable, fmt.Errorf("no available codex accounts in %s mode", pool.Mode()))
			continue
		}

		var lastErr error
		turnCompleted := false
		turnEmitted := false
		recoverPinnedSession := false
		for account != nil {
			turnBody := httpRequestJSON
			if account.Websockets {
				turnBody = requestJSON
			}
			stream, execErr := s.websocketExec.ExecuteStream(ctx, executionSessionID, codexWebsocketTurnRequest{
				Path:            requestPath,
				Model:           extractModelFromBody(turnBody),
				Body:            turnBody,
				OriginalBody:    payload,
				ClientHeaders:   clientHeaders,
				EndpointHeaders: codexWebsocketEndpointHeaders(endpoint),
				Account:         account,
				Config:          config,
			})
			if execErr != nil {
				lastErr = execErr
				// 已 pin 会话失败：释放 pin 并强制下一轮 transcript replay，不跨账号硬切。
				if pinnedAccountID != "" && shouldReleaseCodexWebsocketPin(execErr) {
					recoverPinnedSession = true
					break
				}
				if !isRetryableCodexWebsocketExecutionError(execErr) {
					break
				}
				s.websocketExec.CloseExecutionSession(executionSessionID)
				account = accountSelector.Next()
				continue
			}

			// 上游流建立成功后提前 pin WS 账号，保证后续 turn 粘住同一会话。
			if account.Websockets {
				pinnedAccountID = strings.TrimSpace(account.ID)
				pinnedWebsocket = true
			}

			collector := newResponsesWebsocketCollector()
			streamFinished := false
			var upstreamErrorPayload []byte
			for chunk := range stream.Chunks {
				if chunk.Err != nil {
					lastErr = chunk.Err
					break
				}
				if len(chunk.Payload) == 0 {
					continue
				}
				eventType := strings.TrimSpace(gjson.GetBytes(chunk.Payload, "type").String())
				if eventType == "error" {
					upstreamErrorPayload = bytes.Clone(chunk.Payload)
					lastErr = newCodexWebsocketUpstreamError(chunk.Payload)
					break
				}
				chunk.Payload = collector.Collect(chunk.Payload)
				recordResponsesWebsocketToolCallsFromPayload(sessionKey, chunk.Payload)
				if writeErr := downstream.WriteMessage(websocket.TextMessage, chunk.Payload); writeErr != nil {
					return
				}
				turnEmitted = true
				if isResponsesWebsocketTerminalEvent(eventType) {
					streamFinished = true
					break
				}
			}
			if ctx.Err() != nil {
				return
			}

			if streamFinished {
				if usePassthrough {
					lastRequest = bytes.Clone(nextLastRequest)
				}
				lastResponseOutput = collector.CompletedOutput()
				lastResponseID = collector.CompletedResponseID()
				lastResponsePendingToolCallIDs = collector.PendingToolCallIDs()
				pinnedAccountID = strings.TrimSpace(account.ID)
				pinnedWebsocket = account.Websockets
				forceTranscriptReplayNextRequest = false
				turnCompleted = true
				break
			}
			if lastErr == nil {
				lastErr = errors.New("upstream stream closed before response.completed")
			}
			if turnEmitted {
				if len(upstreamErrorPayload) > 0 {
					_ = downstream.WriteMessage(websocket.TextMessage, upstreamErrorPayload)
				} else {
					_ = writeCodexWebsocketError(downstream, http.StatusBadGateway, lastErr)
				}
				return
			}
			if (allowIncremental || pinnedAccountID != "") && shouldReleaseCodexWebsocketPin(lastErr) {
				recoverPinnedSession = true
				break
			}
			// 未完成 turn：解开本 turn 提前 pin，允许同 turn 切号重试。
			if pinnedAccountID != "" && !allowIncremental {
				pinnedAccountID = ""
				pinnedWebsocket = false
			}
			s.websocketExec.CloseExecutionSession(executionSessionID)
			if !isRetryableCodexWebsocketExecutionError(lastErr) {
				break
			}
			account = accountSelector.Next()
		}

		if turnCompleted {
			continue
		}
		// 已 pin 会话失败：清 pin、回滚状态、强制下一轮 transcript replay，并拆上游连接。
		if recoverPinnedSession || (pinnedAccountID != "" && shouldReleaseCodexWebsocketPin(lastErr)) {
			pinnedAccountID = ""
			pinnedWebsocket = false
			forceTranscriptReplayNextRequest = true
			lastRequest = previousLastRequest
			lastResponseOutput = previousLastResponseOutput
			lastResponseID = previousLastResponseID
			lastResponsePendingToolCallIDs = previousLastResponsePendingToolCallIDs
			s.websocketExec.CloseExecutionSession(executionSessionID)
		} else if !usePassthrough {
			// merge 路径本 turn 预提交了 lastRequest，失败且未释放 pin 时仍回滚，避免脏历史。
			lastRequest = previousLastRequest
		}
		if wsErr, ok := lastErr.(*codexWebsocketUpstreamError); ok && len(wsErr.payload) > 0 {
			_ = downstream.WriteMessage(websocket.TextMessage, wsErr.payload)
		} else {
			_ = writeCodexWebsocketError(downstream, codexWebsocketErrorStatus(lastErr), lastErr)
		}
	}
}

func loadCodexWebsocketConfig(pool *codex.CodexAccountPool) (*codexShared.CodexMultiConfig, error) {
	if pool == nil {
		return nil, errors.New("codex pool not initialized")
	}
	config, err := codexShared.LoadCodexMultiConfig(pool.ConfigPath())
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("config load failed: %v", err)
	}
	if config == nil {
		config = &codexShared.CodexMultiConfig{}
	}
	return config, nil
}

type codexWebsocketTurnAccountSelector struct {
	pool                *codex.CodexAccountPool
	pinned              *codexShared.CodexAccount
	mode                string
	excluded            map[string]bool
	selected            int
	fallbacks           []*codexShared.CodexAccount
	done                bool
	disableHTTPFallback bool
}

func newCodexWebsocketTurnAccountSelector(pool *codex.CodexAccountPool, pinned *codexShared.CodexAccount) *codexWebsocketTurnAccountSelector {
	selector := &codexWebsocketTurnAccountSelector{
		pool:     pool,
		pinned:   pinned,
		excluded: make(map[string]bool, maxRetryAccounts),
	}
	if pool != nil {
		selector.mode = pool.Mode()
	}
	return selector
}

func (s *codexWebsocketTurnAccountSelector) Next() *codexShared.CodexAccount {
	if s == nil || s.pool == nil {
		return nil
	}
	if !s.done {
		if account := s.nextAccount(); account != nil {
			if account.Websockets && s.pinned == nil && !s.disableHTTPFallback {
				fallback := *account
				fallback.Websockets = false
				s.fallbacks = append(s.fallbacks, &fallback)
			}
			return account
		}
		s.done = true
	}
	if len(s.fallbacks) == 0 {
		return nil
	}
	fallback := s.fallbacks[0]
	s.fallbacks = s.fallbacks[1:]
	return fallback
}

func (s *codexWebsocketTurnAccountSelector) nextAccount() *codexShared.CodexAccount {
	if s.selected >= maxRetryAccounts {
		return nil
	}
	if s.pinned != nil {
		if s.selected > 0 {
			return nil
		}
		s.selected++
		return s.pinned
	}
	if s.selected > 0 && s.mode == codexShared.RotationFixed {
		return nil
	}
	var account *codexShared.CodexAccount
	if s.selected == 0 {
		account = s.pool.Select()
	} else {
		// 只在前一账号已经实际失败后才查询下一候选，避免枚举阶段污染全局激活账号。
		account = s.pool.SelectExcluding(s.excluded)
	}
	if account == nil {
		return nil
	}
	accountID := strings.TrimSpace(account.ID)
	if accountID == "" || s.excluded[accountID] {
		return nil
	}
	s.excluded[accountID] = true
	s.selected++
	return account
}

func applyCodexWebsocketEndpointModel(body []byte, endpoint *storage.Endpoint) []byte {
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

func codexWebsocketEndpointHeaders(endpoint *storage.Endpoint) map[string]string {
	if endpoint == nil || len(endpoint.Headers) == 0 {
		return nil
	}
	headers := make(map[string]string, len(endpoint.Headers))
	for key, value := range endpoint.Headers {
		headers[key] = value
	}
	return headers
}

func isRetryableCodexWebsocketExecutionError(err error) bool {
	if err == nil {
		return false
	}
	var executionErr *codexWebsocketExecutionError
	if errors.As(err, &executionErr) {
		return executionErr.retryable
	}
	var upstreamErr *codexWebsocketUpstreamError
	if errors.As(err, &upstreamErr) {
		return upstreamErr.retryable
	}
	return true
}

func codexWebsocketErrorStatus(err error) int {
	var executionErr *codexWebsocketExecutionError
	if errors.As(err, &executionErr) && executionErr.status > 0 {
		return executionErr.status
	}
	if errors.Is(err, context.Canceled) {
		return 499
	}
	return http.StatusBadGateway
}

func resolveCodexAccountProxyURL(pool *codex.CodexAccountPool, account *codexShared.CodexAccount) string {
	proxyURL := plugin.GetAppProxyURL()
	if proxyURL == "" && account != nil {
		proxyURL = strings.TrimSpace(account.ProxyUrl)
	}
	if proxyURL == "" && pool != nil {
		proxyURL = pool.ProxyURL()
	}
	return proxyURL
}

func handleCodexWebsocketAuthError(w http.ResponseWriter, pool *codex.CodexAccountPool, account *codexShared.CodexAccount, err error) {
	markCodexWebsocketAuthError(pool, account, err)
	writeJSON(w, http.StatusUnauthorized, map[string]any{
		"error": map[string]any{
			"type":    "authentication_error",
			"message": fmt.Sprintf("auth failed: %v", err),
		},
	})
}

func markCodexWebsocketAuthError(pool *codex.CodexAccountPool, account *codexShared.CodexAccount, err error) {
	if err != nil && pool != nil && account != nil {
		errStr := err.Error()
		switch {
		case strings.Contains(errStr, "refresh_token_reused"):
			pool.MarkFailed(account.ID, codexShared.CodexStatusReused, 0, "refresh_token_reused")
		case strings.Contains(errStr, "invalid_grant"):
			pool.MarkFailed(account.ID, codexShared.CodexStatusBanned, 0, "auth_failed")
		}
	}
}

func markCodexWebsocketHandshakeStatus(pool *codex.CodexAccountPool, account *codexShared.CodexAccount, resp *http.Response, body []byte) {
	if pool == nil || account == nil || resp == nil {
		return
	}
	switch resp.StatusCode {
	case http.StatusPaymentRequired:
		pool.MarkFailed(account.ID, codexShared.CodexStatusExhausted, 0, "websocket_quota_exhausted")
	case http.StatusTooManyRequests:
		cooldown := parseCooldownFromBody(body)
		if cooldown <= 0 {
			cooldown = parseCooldownDuration(resp)
		}
		pool.MarkFailed(account.ID, codexShared.CodexStatusValid, cooldown, "websocket_rate_limit")
	}
}

func markCodexWebsocketUpstreamError(pool *codex.CodexAccountPool, account *codexShared.CodexAccount, upstreamErr *codexWebsocketUpstreamError) {
	if pool == nil || account == nil || upstreamErr == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(upstreamErr.errorType)) {
	case "rate_limit_error":
		pool.MarkFailed(account.ID, codexShared.CodexStatusValid, time.Minute, "websocket_upstream_rate_limit")
	}
}

func isCodexWebsocketRetryableStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusPaymentRequired, http.StatusTooManyRequests:
		return true
	default:
		return statusCode == 0 || statusCode >= 500
	}
}

func buildCodexResponsesWebsocketURL(httpURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(httpURL))
	if err != nil {
		return "", err
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	default:
		return "", fmt.Errorf("unsupported responses websocket URL scheme %q", parsed.Scheme)
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("responses websocket URL host is empty")
	}
	return parsed.String(), nil
}

func dialCodexWebsocket(ctx context.Context, wsURL string, headers http.Header, proxyURL string) (*websocket.Conn, *http.Response, error) {
	dialer := newCodexWebsocketDialer(proxyURL)
	dialer.HandshakeTimeout = codexResponsesWebsocketHandshakeTO
	dialer.EnableCompression = true
	if ctx == nil {
		ctx = context.Background()
	}
	return dialer.DialContext(ctx, wsURL, headers)
}

func newCodexWebsocketDialer(proxyURL string) *websocket.Dialer {
	dialer := &websocket.Dialer{
		Proxy:             http.ProxyFromEnvironment,
		HandshakeTimeout:  codexResponsesWebsocketHandshakeTO,
		EnableCompression: true,
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
		logger.Warn("[Codex] parse websocket proxy URL failed: %v", err)
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
			logger.Warn("[Codex] create websocket SOCKS5 dialer failed: %v", err)
			return dialer
		}
		dialer.Proxy = nil
		dialer.NetDialContext = func(_ context.Context, network, addr string) (net.Conn, error) {
			return socksDialer.Dial(network, addr)
		}
	case "http", "https":
		dialer.Proxy = http.ProxyURL(parsed)
	default:
		logger.Warn("[Codex] unsupported websocket proxy scheme: %s", parsed.Scheme)
	}
	return dialer
}

type codexWebsocketUpstreamError struct {
	message   string
	errorType string
	retryable bool
	payload   []byte
}

func (e *codexWebsocketUpstreamError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.message) != "" {
		return e.message
	}
	if strings.TrimSpace(e.errorType) != "" {
		return e.errorType
	}
	return "codex websocket upstream error"
}

func newCodexWebsocketUpstreamError(payload []byte) *codexWebsocketUpstreamError {
	errType := strings.TrimSpace(gjson.GetBytes(payload, "error.type").String())
	if errType == "" {
		errType = strings.TrimSpace(gjson.GetBytes(payload, "error.code").String())
	}
	message := strings.TrimSpace(gjson.GetBytes(payload, "error.message").String())
	if message == "" {
		message = strings.TrimSpace(gjson.GetBytes(payload, "message").String())
	}
	if message == "" {
		message = "codex websocket upstream error"
	}
	return &codexWebsocketUpstreamError{
		message:   message,
		errorType: errType,
		retryable: isCodexWebsocketRetryableErrorType(errType),
		payload:   bytes.Clone(payload),
	}
}

func isCodexWebsocketRetryableErrorType(errorType string) bool {
	switch strings.ToLower(strings.TrimSpace(errorType)) {
	case "authentication_error", "permission_error", "rate_limit_error", "server_error", "api_error", "overloaded_error":
		return true
	default:
		return errorType == ""
	}
}

func codexWebsocketUsesUpstreamPassthrough(pool *codex.CodexAccountPool, pinned *codexShared.CodexAccount, pinnedWebsocket, forceTranscriptReplay bool) bool {
	if forceTranscriptReplay {
		return false
	}
	if pinned != nil && pinnedWebsocket {
		return true
	}
	if pinned != nil {
		return false
	}
	return pool != nil && pool.AllAvailableSupportWebsockets()
}

// normalizeResponsesWebsocketPassthroughRequest 在纯 WS 上游路径上尽量原样转发客户端载荷。
// 仅补齐 model/stream，并校验 type；保留 previous_response_id；type 由 PrepareWebsocket 统一写入。
func normalizeResponsesWebsocketPassthroughRequest(rawJSON []byte, fallbackModel string) ([]byte, error) {
	if !json.Valid(rawJSON) {
		return nil, errors.New("invalid websocket request JSON")
	}
	requestType := strings.TrimSpace(gjson.GetBytes(rawJSON, "type").String())
	switch requestType {
	case "response.create", "response.append":
	default:
		return nil, fmt.Errorf("unsupported websocket request type: %s", requestType)
	}
	normalized := bytes.Clone(rawJSON)
	if strings.TrimSpace(gjson.GetBytes(normalized, "model").String()) == "" {
		fallbackModel = strings.TrimSpace(fallbackModel)
		if fallbackModel == "" {
			return nil, errors.New("missing model in response.create request")
		}
		normalized, _ = sjson.SetBytes(normalized, "model", fallbackModel)
	}
	// 去掉 type：上游由 BuildWebsocketRequestBody 统一写成 response.create。
	if updated, err := sjson.DeleteBytes(normalized, "type"); err == nil {
		normalized = updated
	}
	normalized, _ = sjson.SetBytes(normalized, "stream", true)
	return normalized, nil
}

// shouldReleaseCodexWebsocketPin 仅在显式 status / 已知上游语义错误时释放 pin。
func shouldReleaseCodexWebsocketPin(err error) bool {
	if err == nil {
		return false
	}
	var executionErr *codexWebsocketExecutionError
	if errors.As(err, &executionErr) && executionErr != nil && executionErr.status > 0 {
		switch executionErr.status {
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
	var upstreamErr *codexWebsocketUpstreamError
	if errors.As(err, &upstreamErr) && upstreamErr != nil {
		switch strings.ToLower(strings.TrimSpace(upstreamErr.errorType)) {
		case "authentication_error", "permission_error", "rate_limit_error", "server_error", "api_error", "overloaded_error":
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
		strings.Contains(msg, "message_too_big"),
		strings.Contains(msg, "websocket upgrade required"),
		strings.Contains(msg, "upgrade required"):
		return true
	default:
		return false
	}
}

func normalizeResponsesWebsocketRequest(rawJSON []byte, lastRequest []byte, lastResponseOutput []byte) ([]byte, []byte, []byte, error) {
	return normalizeResponsesWebsocketRequestWithIncrementalState(rawJSON, lastRequest, lastResponseOutput, "", nil, true)
}

func normalizeResponsesWebsocketRequestWithIncrementalState(rawJSON []byte, lastRequest []byte, lastResponseOutput []byte, lastResponseID string, lastResponsePendingToolCallIDs []string, allowIncremental bool) ([]byte, []byte, []byte, error) {
	requestType := strings.TrimSpace(gjson.GetBytes(rawJSON, "type").String())
	switch requestType {
	case "response.create":
		if len(lastRequest) == 0 {
			return normalizeResponsesWebsocketCreate(rawJSON)
		}
		return normalizeResponsesWebsocketSubsequent(rawJSON, lastRequest, lastResponseOutput, lastResponseID, lastResponsePendingToolCallIDs, allowIncremental)
	case "response.append":
		return normalizeResponsesWebsocketSubsequent(rawJSON, lastRequest, lastResponseOutput, lastResponseID, lastResponsePendingToolCallIDs, allowIncremental)
	default:
		return nil, nil, lastRequest, fmt.Errorf("unsupported websocket request type: %s", requestType)
	}
}

func normalizeResponsesWebsocketCreate(rawJSON []byte) ([]byte, []byte, []byte, error) {
	normalized, errDelete := sjson.DeleteBytes(rawJSON, "type")
	if errDelete != nil {
		normalized = bytes.Clone(rawJSON)
	}
	normalized, _ = sjson.DeleteBytes(normalized, "previous_response_id")
	normalized, _ = sjson.SetBytes(normalized, "stream", true)
	if !gjson.GetBytes(normalized, "input").Exists() {
		normalized, _ = sjson.SetRawBytes(normalized, "input", []byte("[]"))
	}
	if strings.TrimSpace(gjson.GetBytes(normalized, "model").String()) == "" {
		return nil, nil, nil, errors.New("missing model in response.create request")
	}
	return normalized, bytes.Clone(normalized), bytes.Clone(normalized), nil
}

func normalizeResponsesWebsocketSubsequent(rawJSON []byte, lastRequest []byte, lastResponseOutput []byte, lastResponseID string, lastResponsePendingToolCallIDs []string, allowIncremental bool) ([]byte, []byte, []byte, error) {
	if len(lastRequest) == 0 {
		return nil, nil, lastRequest, errors.New("websocket request received before response.create")
	}
	nextInput := gjson.GetBytes(rawJSON, "input")
	if !nextInput.Exists() || !nextInput.IsArray() {
		return nil, nil, lastRequest, errors.New("websocket request requires array field: input")
	}

	if shouldReplaceWebsocketTranscript(rawJSON, nextInput) {
		normalized := normalizeResponsesWebsocketTranscriptReplacement(rawJSON, lastRequest)
		return normalized, bytes.Clone(normalized), bytes.Clone(normalized), nil
	}

	if allowIncremental {
		prev := strings.TrimSpace(gjson.GetBytes(rawJSON, "previous_response_id").String())
		if prev == "" && inputSatisfiesPendingToolCalls(nextInput, lastResponsePendingToolCallIDs) {
			prev = strings.TrimSpace(lastResponseID)
		}
		if prev == "" && len(lastResponsePendingToolCallIDs) > 0 {
			normalized := normalizeResponsesWebsocketTranscriptReplacement(rawJSON, lastRequest)
			return normalized, bytes.Clone(normalized), bytes.Clone(normalized), nil
		}
		if prev != "" {
			websocketBody, errDelete := sjson.DeleteBytes(rawJSON, "type")
			if errDelete != nil {
				websocketBody = bytes.Clone(rawJSON)
			}
			websocketBody = ensureResponsesWebsocketInheritedFields(websocketBody, lastRequest)
			websocketBody, _ = sjson.SetBytes(websocketBody, "previous_response_id", prev)
			websocketBody, _ = sjson.SetBytes(websocketBody, "stream", true)

			httpBody, err := normalizeResponsesWebsocketMergedSubsequent(rawJSON, lastRequest, lastResponseOutput, nextInput.Raw)
			if err != nil {
				return nil, nil, lastRequest, err
			}
			return websocketBody, httpBody, bytes.Clone(httpBody), nil
		}
	}

	if inputContainsFullTranscript(nextInput) {
		normalized := normalizeResponsesWebsocketTranscriptReplacement(rawJSON, lastRequest)
		return normalized, bytes.Clone(normalized), bytes.Clone(normalized), nil
	}

	mergedBody, err := normalizeResponsesWebsocketMergedSubsequent(rawJSON, lastRequest, lastResponseOutput, nextInput.Raw)
	if err != nil {
		return nil, nil, lastRequest, err
	}
	return mergedBody, bytes.Clone(mergedBody), bytes.Clone(mergedBody), nil
}

func normalizeResponsesWebsocketMergedSubsequent(rawJSON []byte, lastRequest []byte, lastResponseOutput []byte, nextInputRaw string) ([]byte, error) {
	mergedInput, err := mergeJSONArrayRaw(normalizeResponsesInputArrayRaw(gjson.GetBytes(lastRequest, "input")), normalizeJSONArrayRaw(lastResponseOutput))
	if err != nil {
		return nil, fmt.Errorf("invalid previous response output: %w", err)
	}
	mergedInput, err = mergeJSONArrayRaw(mergedInput, nextInputRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid request input: %w", err)
	}

	normalized, errDelete := sjson.DeleteBytes(rawJSON, "type")
	if errDelete != nil {
		normalized = bytes.Clone(rawJSON)
	}
	normalized, _ = sjson.DeleteBytes(normalized, "previous_response_id")
	normalized, err = sjson.SetRawBytes(normalized, "input", []byte(mergedInput))
	if err != nil {
		return nil, err
	}
	normalized = ensureResponsesWebsocketInheritedFields(normalized, lastRequest)
	normalized, _ = sjson.SetBytes(normalized, "stream", true)
	return normalized, nil
}

func normalizeResponsesInputArrayRaw(input gjson.Result) string {
	if input.IsArray() {
		return normalizeJSONArrayRaw([]byte(input.Raw))
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

func shouldReplaceWebsocketTranscript(rawJSON []byte, nextInput gjson.Result) bool {
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
	// Codex CLI 本地 compaction 后会用带摘要的 create 替换历史，避免与 stale lastRequest merge。
	if requestType == "response.create" && !previousResponseID.Exists() && inputHasCodexLocalCompactionSummary(nextInput) {
		return true
	}

	for _, item := range nextInput.Array() {
		switch strings.TrimSpace(item.Get("type").String()) {
		case "function_call", "custom_tool_call":
			return true
		case "message":
			if strings.TrimSpace(item.Get("role").String()) == "assistant" {
				return true
			}
		}
	}
	return false
}

func inputHasCodexLocalCompactionSummary(input gjson.Result) bool {
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
		if role == "user" && strings.HasPrefix(codexLocalCompactionMessageText(item), codexLocalCompactionSummaryPrefix+"\n") {
			hasSummary = true
		}
	}
	return hasSummary
}

func codexLocalCompactionMessageText(message gjson.Result) string {
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

func normalizeResponsesWebsocketTranscriptReplacement(rawJSON []byte, lastRequest []byte) []byte {
	normalized, errDelete := sjson.DeleteBytes(rawJSON, "type")
	if errDelete != nil {
		normalized = bytes.Clone(rawJSON)
	}
	normalized, _ = sjson.DeleteBytes(normalized, "previous_response_id")
	normalized = ensureResponsesWebsocketInheritedFields(normalized, lastRequest)
	normalized, _ = sjson.SetBytes(normalized, "stream", true)
	return bytes.Clone(normalized)
}

func ensureResponsesWebsocketInheritedFields(normalized []byte, lastRequest []byte) []byte {
	if !gjson.GetBytes(normalized, "model").Exists() {
		if modelName := strings.TrimSpace(gjson.GetBytes(lastRequest, "model").String()); modelName != "" {
			normalized, _ = sjson.SetBytes(normalized, "model", modelName)
		}
	}
	if !gjson.GetBytes(normalized, "prompt_cache_key").Exists() {
		if promptCacheKey := strings.TrimSpace(gjson.GetBytes(lastRequest, "prompt_cache_key").String()); promptCacheKey != "" {
			normalized, _ = sjson.SetBytes(normalized, "prompt_cache_key", promptCacheKey)
		}
	}
	if !gjson.GetBytes(normalized, "instructions").Exists() {
		instructions := gjson.GetBytes(lastRequest, "instructions")
		if instructions.Exists() {
			normalized, _ = sjson.SetRawBytes(normalized, "instructions", []byte(instructions.Raw))
		}
	}
	return normalized
}

func inputContainsFullTranscript(input gjson.Result) bool {
	if !input.IsArray() {
		return false
	}
	for _, item := range input.Array() {
		switch item.Get("type").String() {
		case "compaction", "compaction_summary":
			return true
		}
	}
	return false
}

func inputSatisfiesPendingToolCalls(input gjson.Result, pendingCallIDs []string) bool {
	if len(pendingCallIDs) == 0 {
		return true
	}
	resolved := make(map[string]struct{}, len(pendingCallIDs))
	for _, item := range input.Array() {
		switch strings.TrimSpace(item.Get("type").String()) {
		case "function_call_output", "custom_tool_call_output":
			if callID := strings.TrimSpace(item.Get("call_id").String()); callID != "" {
				resolved[callID] = struct{}{}
			}
		}
	}
	for _, callID := range pendingCallIDs {
		if _, ok := resolved[strings.TrimSpace(callID)]; !ok {
			return false
		}
	}
	return true
}

func dedupeResponsesWebsocketInput(body []byte) []byte {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body
	}
	seen := make(map[string]struct{}, len(input.Array()))
	items := make([]json.RawMessage, 0, len(input.Array()))
	changed := false
	for _, item := range input.Array() {
		key := ""
		if id := strings.TrimSpace(item.Get("id").String()); id != "" {
			key = "id:" + id
		} else if callID := strings.TrimSpace(item.Get("call_id").String()); callID != "" {
			switch strings.TrimSpace(item.Get("type").String()) {
			case "function_call", "custom_tool_call":
				key = "call:" + callID
			case "function_call_output", "custom_tool_call_output":
				key = "output:" + callID
			}
		}
		if key != "" {
			if _, ok := seen[key]; ok {
				changed = true
				continue
			}
			seen[key] = struct{}{}
		}
		items = append(items, json.RawMessage(item.Raw))
	}
	if !changed {
		return body
	}
	updatedInput, err := json.Marshal(items)
	if err != nil {
		return body
	}
	updated, err := sjson.SetRawBytes(body, "input", updatedInput)
	if err != nil {
		return body
	}
	return updated
}

func mergeJSONArrayRaw(existingRaw, appendRaw string) (string, error) {
	existingRaw = strings.TrimSpace(existingRaw)
	appendRaw = strings.TrimSpace(appendRaw)
	if existingRaw == "" {
		existingRaw = "[]"
	}
	if appendRaw == "" {
		appendRaw = "[]"
	}
	var existing []json.RawMessage
	if err := json.Unmarshal([]byte(existingRaw), &existing); err != nil {
		return "", err
	}
	var appendItems []json.RawMessage
	if err := json.Unmarshal([]byte(appendRaw), &appendItems); err != nil {
		return "", err
	}
	merged := append(existing, appendItems...)
	out, err := json.Marshal(merged)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func normalizeJSONArrayRaw(raw []byte) string {
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

func normalizeCodexHTTPFallbackCompletion(payload []byte) []byte {
	if strings.TrimSpace(gjson.GetBytes(payload, "type").String()) == "response.done" {
		if updated, err := sjson.SetBytes(payload, "type", "response.completed"); err == nil {
			return updated
		}
	}
	return payload
}

func isResponsesWebsocketTerminalEvent(eventType string) bool {
	eventType = strings.TrimSpace(eventType)
	return eventType == "response.completed" || eventType == "response.done"
}

type responsesWebsocketCollector struct {
	outputItemsByIndex  map[int64][]byte
	outputItemsFallback [][]byte
	completedOutput     []byte
	completedResponseID string
	pendingToolCallIDs  map[string]struct{}
}

func newResponsesWebsocketCollector() *responsesWebsocketCollector {
	return &responsesWebsocketCollector{
		outputItemsByIndex: make(map[int64][]byte),
		completedOutput:    []byte("[]"),
		pendingToolCallIDs: make(map[string]struct{}),
	}
}

func (c *responsesWebsocketCollector) Collect(payload []byte) []byte {
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
	if !isResponsesWebsocketTerminalEvent(gjson.GetBytes(payload, "type").String()) {
		return payload
	}

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

func (c *responsesWebsocketCollector) collectedOutput() []byte {
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
	output, err := json.Marshal(items)
	if err != nil {
		return []byte("[]")
	}
	return output
}

func (c *responsesWebsocketCollector) updatePendingToolCallIDs(item gjson.Result) {
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

func (c *responsesWebsocketCollector) CompletedOutput() []byte {
	if c == nil || len(c.completedOutput) == 0 {
		return []byte("[]")
	}
	return bytes.Clone(c.completedOutput)
}

func (c *responsesWebsocketCollector) CompletedResponseID() string {
	if c == nil {
		return ""
	}
	return c.completedResponseID
}

func (c *responsesWebsocketCollector) PendingToolCallIDs() []string {
	if c == nil {
		return nil
	}
	return sortedPendingToolCallIDs(c.pendingToolCallIDs)
}

func websocketJSONPayloadsFromChunk(chunk []byte) [][]byte {
	payloads := make([][]byte, 0, 8)
	lines := bytes.Split(chunk, []byte("\n"))
	for i := range lines {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 || bytes.HasPrefix(line, []byte("event:")) {
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			line = bytes.TrimSpace(line[len("data:"):])
		}
		if len(line) == 0 || bytes.Equal(line, []byte("[DONE]")) {
			continue
		}
		if json.Valid(line) {
			payloads = append(payloads, bytes.Clone(line))
		}
	}
	return payloads
}

func writeCodexWebsocketError(conn *websocket.Conn, status int, err error) error {
	if status <= 0 {
		status = http.StatusInternalServerError
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
	payload := map[string]any{
		"type":   "error",
		"status": status,
		"error":  errorBody,
	}
	var executionErr *codexWebsocketExecutionError
	if errors.As(err, &executionErr) && len(executionErr.headers) > 0 {
		headers := make(map[string]string, len(executionErr.headers))
		for key, values := range executionErr.headers {
			if len(values) > 0 {
				headers[key] = values[0]
			}
		}
		if len(headers) > 0 {
			payload["headers"] = headers
		}
	}
	body, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return marshalErr
	}
	return conn.WriteMessage(websocket.TextMessage, body)
}

func readAndCloseHTTPResponseBody(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return data
}
