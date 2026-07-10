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
	"strings"
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
	"golang.org/x/net/proxy"
)

const (
	codexResponsesWebsocketIdleTimeout = 5 * time.Minute
	codexResponsesWebsocketHandshakeTO = 30 * time.Second
)

var codexWebsocketUpgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

func isCodexWebsocketRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket")
}

func (s *CodexService) HandleResponsesWebsocket(w http.ResponseWriter, r *http.Request) {
	downstreamConn, err := codexWebsocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer downstreamConn.Close()

	sessionKey := websocketDownstreamSessionKey(r)
	retainResponsesWebsocketToolCaches(sessionKey)
	defer releaseResponsesWebsocketToolCaches(sessionKey)

	s.proxyResponsesWebsocketTurns(r.Context(), downstreamConn, r.Header.Clone(), r.URL.Path, sessionKey)
}

func (s *CodexService) proxyResponsesWebsocketTurns(ctx context.Context, downstream *websocket.Conn, clientHeaders http.Header, requestPath string, sessionKey string) {
	var lastRequest []byte
	lastResponseOutput := []byte("[]")

	for {
		msgType, payload, err := downstream.ReadMessage()
		if err != nil {
			return
		}
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}

		requestJSON, httpRequestJSON, nextLastRequest, err := normalizeResponsesWebsocketRequest(payload, lastRequest, lastResponseOutput)
		if err != nil {
			_ = writeCodexWebsocketError(downstream, http.StatusBadRequest, err)
			continue
		}
		requestJSON = repairResponsesWebsocketToolCalls(sessionKey, requestJSON)
		httpRequestJSON = repairResponsesWebsocketToolCalls(sessionKey, httpRequestJSON)
		nextLastRequest = repairResponsesWebsocketToolCalls(sessionKey, nextLastRequest)
		lastRequest = nextLastRequest

		completedOutput, err := s.forwardResponsesWebsocketTurn(ctx, downstream, requestJSON, httpRequestJSON, clientHeaders, requestPath, sessionKey)
		if err != nil {
			if wsErr, ok := err.(*codexWebsocketUpstreamError); ok && len(wsErr.payload) > 0 {
				_ = downstream.WriteMessage(websocket.TextMessage, wsErr.payload)
			} else {
				_ = writeCodexWebsocketError(downstream, http.StatusBadGateway, err)
			}
			return
		}
		if len(completedOutput) > 0 {
			lastResponseOutput = completedOutput
		}
	}
}

func (s *CodexService) forwardResponsesWebsocketTurn(ctx context.Context, downstream *websocket.Conn, requestJSON []byte, httpRequestJSON []byte, clientHeaders http.Header, requestPath string, sessionKey string) ([]byte, error) {
	pool := codex.GetPool()
	if pool == nil {
		return nil, errors.New("codex pool not initialized")
	}

	configPath := pool.ConfigPath()
	config, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("config load failed: %v", err)
	}
	if config == nil {
		config = &codexShared.CodexMultiConfig{}
	}

	firstAccount := pool.SelectWebsocket()
	if firstAccount == nil {
		firstAccount = pool.Select()
	}
	if firstAccount == nil {
		return nil, fmt.Errorf("no available codex accounts in %s mode", pool.Mode())
	}

	tried := make(map[string]bool, maxRetryAccounts)
	tryWebsocketAccounts := firstAccount.Websockets
	var lastErr error
	for attempt := 0; attempt < maxRetryAccounts; attempt++ {
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}

		account := firstAccount
		if attempt > 0 {
			if tryWebsocketAccounts {
				account = pool.SelectWebsocketExcluding(tried)
				if account == nil {
					tryWebsocketAccounts = false
				}
			}
			if !tryWebsocketAccounts {
				account = pool.SelectExcluding(tried)
			}
		}
		if account == nil {
			break
		}
		tried[strings.TrimSpace(account.ID)] = true

		var completedOutput []byte
		var retryable bool
		if account.Websockets && tryWebsocketAccounts {
			completedOutput, retryable, err = s.forwardResponsesWebsocketTurnViaUpstream(ctx, downstream, requestJSON, clientHeaders, requestPath, sessionKey, pool, config, account)
		} else {
			completedOutput, retryable, err = s.forwardResponsesWebsocketTurnViaHTTP(ctx, downstream, httpRequestJSON, clientHeaders, requestPath, sessionKey)
		}
		if err == nil {
			return completedOutput, nil
		}
		lastErr = err
		if !retryable {
			return nil, err
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no available codex accounts in %s mode", pool.Mode())
}

func (s *CodexService) forwardResponsesWebsocketTurnViaUpstream(ctx context.Context, downstream *websocket.Conn, requestJSON []byte, clientHeaders http.Header, requestPath string, sessionKey string, pool *codex.CodexAccountPool, config *codexShared.CodexMultiConfig, account *codexShared.CodexAccount) ([]byte, bool, error) {
	proxyURL := resolveCodexAccountProxyURL(pool, account)
	authMgr := s.GetOrCreateAuthManager(account.ID, pool.ConfigPath(), proxyURL)
	accessToken, accountID, err := authMgr.GetAccessToken()
	if err != nil {
		markCodexWebsocketAuthError(pool, account, err)
		return nil, true, fmt.Errorf("auth failed: %v", err)
	}

	upstreamURL, err := buildCodexResponsesWebsocketURL(codexBackend.UpstreamURL(config, codexBackend.TargetPath(requestPath)))
	if err != nil {
		return nil, false, err
	}
	preparedBody, upstreamHeaders, identityState, err := codexBackend.PrepareWebsocket(ctx, codexBackend.Request{
		Path:                   requestPath,
		Source:                 codexBackend.SourceCodex,
		Model:                  extractModelFromBody(requestJSON),
		Body:                   requestJSON,
		OriginalBody:           requestJSON,
		Headers:                clientHeaders,
		Config:                 config,
		AccessToken:            accessToken,
		AccountID:              accountID,
		LocalAccountID:         account.ID,
		PlanType:               account.PlanType,
		DisableImageGeneration: plugin.GetAppDisableImageGeneration(),
	})
	if err != nil {
		return nil, false, err
	}
	upstreamConn, handshakeResp, err := dialCodexWebsocket(ctx, upstreamURL, upstreamHeaders, proxyURL)
	if err != nil {
		handshakeBody := readAndCloseHTTPResponseBody(handshakeResp)
		if handshakeResp != nil && handshakeResp.StatusCode > 0 {
			markCodexWebsocketHandshakeStatus(pool, account, handshakeResp, handshakeBody)
			if len(handshakeBody) > 0 {
				return nil, isCodexWebsocketRetryableStatus(handshakeResp.StatusCode), fmt.Errorf("codex websocket upstream returned %d: %s", handshakeResp.StatusCode, strings.TrimSpace(string(handshakeBody)))
			}
			return nil, isCodexWebsocketRetryableStatus(handshakeResp.StatusCode), fmt.Errorf("codex websocket upstream returned %d", handshakeResp.StatusCode)
		}
		return nil, true, fmt.Errorf("codex websocket dial failed: %v", err)
	}
	defer upstreamConn.Close()
	upstreamConn.EnableWriteCompression(false)

	if err := upstreamConn.WriteMessage(websocket.TextMessage, preparedBody); err != nil {
		return nil, true, err
	}

	completedOutput, err := forwardUpstreamWebsocketToDownstream(ctx, upstreamConn, downstream, sessionKey, identityState)
	if err != nil {
		if wsErr, ok := err.(*codexWebsocketUpstreamError); ok {
			markCodexWebsocketUpstreamError(pool, account, wsErr)
			return completedOutput, wsErr.retryable, err
		}
		return completedOutput, false, err
	}
	pool.ReportSuccess(account.ID)
	return completedOutput, false, nil
}

func (s *CodexService) forwardResponsesWebsocketTurnViaHTTP(ctx context.Context, downstream *websocket.Conn, requestJSON []byte, clientHeaders http.Header, requestPath string, sessionKey string) ([]byte, bool, error) {
	result := s.RoundTrip(ctx, &executor.UpstreamRequest{
		Method:              http.MethodPost,
		TargetPath:          requestPath,
		Headers:             clientHeaders,
		Body:                requestJSON,
		IsStreaming:         true,
		RequestModel:        extractModelFromBody(requestJSON),
		OriginalPath:        requestPath,
		TargetInterfaceType: "codex",
	})
	if result == nil {
		return nil, false, fmt.Errorf("codex HTTP fallback returned nil result")
	}
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.StatusCode >= 400 {
		if len(result.Body) > 0 {
			return nil, false, fmt.Errorf("codex HTTP fallback returned %d: %s", result.StatusCode, strings.TrimSpace(string(result.Body)))
		}
		return nil, false, fmt.Errorf("codex HTTP fallback returned %d", result.StatusCode)
	}
	var stream []byte
	if result.Stream != nil {
		defer result.Stream.Close()
		data, err := io.ReadAll(result.Stream)
		if err != nil {
			return nil, false, err
		}
		stream = data
	} else {
		stream = result.Body
	}
	completedOutput, err := writeSSEFramesToWebsocket(downstream, stream, sessionKey)
	return completedOutput, false, err
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

func forwardUpstreamWebsocketToDownstream(ctx context.Context, upstream, downstream *websocket.Conn, sessionKey string, identityState codexBackend.IdentityState) ([]byte, error) {
	completedOutput := []byte("[]")
	for {
		if ctx != nil && ctx.Err() != nil {
			return completedOutput, ctx.Err()
		}
		_ = upstream.SetReadDeadline(time.Now().Add(codexResponsesWebsocketIdleTimeout))
		msgType, payload, err := upstream.ReadMessage()
		if err != nil {
			return completedOutput, err
		}
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}
		if msgType == websocket.TextMessage {
			payload = normalizeCodexWebsocketCompletion(bytes.TrimSpace(payload))
			payload = codexBackend.ExposeIdentityPayload(payload, identityState)
			recordResponsesWebsocketToolCallsFromPayload(sessionKey, payload)
			if output := responseCompletedOutputFromWebsocketPayload(payload); output != nil {
				completedOutput = output
			}
			if strings.TrimSpace(gjson.GetBytes(payload, "type").String()) == "error" {
				return completedOutput, newCodexWebsocketUpstreamError(payload)
			}
		}
		if err := downstream.WriteMessage(msgType, payload); err != nil {
			return completedOutput, err
		}
		if strings.TrimSpace(gjson.GetBytes(payload, "type").String()) == "response.completed" {
			return completedOutput, nil
		}
	}
}

func normalizeResponsesWebsocketRequest(rawJSON []byte, lastRequest []byte, lastResponseOutput []byte) ([]byte, []byte, []byte, error) {
	requestType := strings.TrimSpace(gjson.GetBytes(rawJSON, "type").String())
	switch requestType {
	case "response.create":
		if len(lastRequest) == 0 {
			return normalizeResponsesWebsocketCreate(rawJSON)
		}
		return normalizeResponsesWebsocketSubsequent(rawJSON, lastRequest, lastResponseOutput)
	case "response.append":
		return normalizeResponsesWebsocketSubsequent(rawJSON, lastRequest, lastResponseOutput)
	default:
		return nil, nil, lastRequest, fmt.Errorf("unsupported websocket request type: %s", requestType)
	}
}

func normalizeResponsesWebsocketCreate(rawJSON []byte) ([]byte, []byte, []byte, error) {
	normalized, errDelete := sjson.DeleteBytes(rawJSON, "type")
	if errDelete != nil {
		normalized = bytes.Clone(rawJSON)
	}
	normalized, _ = sjson.SetBytes(normalized, "stream", true)
	if !gjson.GetBytes(normalized, "input").Exists() {
		normalized, _ = sjson.SetRawBytes(normalized, "input", []byte("[]"))
	}
	normalized = stripCodexUnsupportedRequestFields(normalized)
	if strings.TrimSpace(gjson.GetBytes(normalized, "model").String()) == "" {
		return nil, nil, nil, errors.New("missing model in response.create request")
	}
	return normalized, bytes.Clone(normalized), bytes.Clone(normalized), nil
}

func normalizeResponsesWebsocketSubsequent(rawJSON []byte, lastRequest []byte, lastResponseOutput []byte) ([]byte, []byte, []byte, error) {
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

	if prev := strings.TrimSpace(gjson.GetBytes(rawJSON, "previous_response_id").String()); prev != "" {
		websocketBody, errDelete := sjson.DeleteBytes(rawJSON, "type")
		if errDelete != nil {
			websocketBody = bytes.Clone(rawJSON)
		}
		websocketBody = ensureResponsesWebsocketInheritedFields(websocketBody, lastRequest)
		websocketBody, _ = sjson.SetBytes(websocketBody, "stream", true)
		websocketBody = stripCodexUnsupportedRequestFields(websocketBody)

		httpBody, err := normalizeResponsesWebsocketMergedSubsequent(rawJSON, lastRequest, lastResponseOutput, nextInput.Raw)
		if err != nil {
			return nil, nil, lastRequest, err
		}
		return websocketBody, httpBody, bytes.Clone(httpBody), nil
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
	mergedInput, err := mergeJSONArrayRaw(gjson.GetBytes(lastRequest, "input").Raw, normalizeJSONArrayRaw(lastResponseOutput))
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
	normalized = stripCodexUnsupportedRequestFields(normalized)
	return normalized, nil
}

func shouldReplaceWebsocketTranscript(rawJSON []byte, nextInput gjson.Result) bool {
	requestType := strings.TrimSpace(gjson.GetBytes(rawJSON, "type").String())
	if requestType != "response.create" && requestType != "response.append" {
		return false
	}
	if strings.TrimSpace(gjson.GetBytes(rawJSON, "previous_response_id").String()) != "" {
		return false
	}
	if !nextInput.Exists() || !nextInput.IsArray() {
		return false
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

func normalizeResponsesWebsocketTranscriptReplacement(rawJSON []byte, lastRequest []byte) []byte {
	normalized, errDelete := sjson.DeleteBytes(rawJSON, "type")
	if errDelete != nil {
		normalized = bytes.Clone(rawJSON)
	}
	normalized, _ = sjson.DeleteBytes(normalized, "previous_response_id")
	normalized = ensureResponsesWebsocketInheritedFields(normalized, lastRequest)
	normalized, _ = sjson.SetBytes(normalized, "stream", true)
	normalized = stripCodexUnsupportedRequestFields(normalized)
	return bytes.Clone(normalized)
}

func ensureResponsesWebsocketInheritedFields(normalized []byte, lastRequest []byte) []byte {
	if !gjson.GetBytes(normalized, "model").Exists() {
		if modelName := strings.TrimSpace(gjson.GetBytes(lastRequest, "model").String()); modelName != "" {
			normalized, _ = sjson.SetBytes(normalized, "model", modelName)
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

func stripCodexUnsupportedRequestFields(rawJSON []byte) []byte {
	rawJSON, _ = sjson.DeleteBytes(rawJSON, "prompt_cache_retention")
	rawJSON, _ = sjson.DeleteBytes(rawJSON, "safety_identifier")
	return rawJSON
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

func responseCompletedOutputFromWebsocketPayload(payload []byte) []byte {
	output := gjson.GetBytes(payload, "response.output")
	if output.Exists() && output.IsArray() {
		return bytes.Clone([]byte(output.Raw))
	}
	return nil
}

func normalizeCodexWebsocketCompletion(payload []byte) []byte {
	return payload
}

func writeSSEFramesToWebsocket(conn *websocket.Conn, stream []byte, sessionKey string) ([]byte, error) {
	completedOutput := []byte("[]")
	payloads := websocketJSONPayloadsFromChunk(stream)
	for _, payload := range payloads {
		payload = normalizeCodexWebsocketCompletion(payload)
		recordResponsesWebsocketToolCallsFromPayload(sessionKey, payload)
		if output := responseCompletedOutputFromWebsocketPayload(payload); output != nil {
			completedOutput = output
		}
		if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			return completedOutput, err
		}
	}
	return completedOutput, nil
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
	payload := map[string]any{
		"type":   "error",
		"status": status,
		"error": map[string]any{
			"type":    "server_error",
			"message": message,
		},
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
