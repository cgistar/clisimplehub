package backend

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	xaiShared "clisimplehub/internal/xai/shared"
)

// MapInboundPath converts /xai/v1/... inbound path to upstream path under base.
// Examples:
//
//	/xai/v1/responses            -> /responses
//	/xai/v1/responses/compact    -> /responses/compact
//	/xai/v1/images/generations   -> /images/generations
//	/xai/v1/videos/generations   -> /videos/generations
//	/xai/v1/videos/{id}          -> /videos/{id}
//	/xai/v1/models               -> /models
func MapInboundPath(path string) string {
	path = normalizePath(path)
	for _, prefix := range []string{"/xai/v1", "/xai"} {
		if path == prefix {
			return "/"
		}
		if strings.HasPrefix(path, prefix+"/") {
			rest := strings.TrimPrefix(path, prefix)
			return normalizePath(rest)
		}
	}
	return path
}

func IsResponsesPath(path string) bool {
	p := MapInboundPath(path)
	return p == "/responses" || strings.HasPrefix(p, "/responses/")
}

func IsCompactPath(path string) bool {
	return MapInboundPath(path) == "/responses/compact"
}

func IsImagesPath(path string) bool {
	p := MapInboundPath(path)
	return strings.HasPrefix(p, "/images/")
}

func IsVideosPath(path string) bool {
	p := MapInboundPath(path)
	return p == "/videos" || strings.HasPrefix(p, "/videos/")
}

func IsModelsPath(path string) bool {
	return MapInboundPath(path) == "/models"
}

// IsMediaPath 图片/视频走官方 API，不改写到 cli-chat-proxy。
func IsMediaPath(path string) bool {
	return IsImagesPath(path) || IsVideosPath(path)
}

func configuredBaseURL(config *xaiShared.XaiMultiConfig) string {
	if config == nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(config.Config.BaseURL), "/")
}

func normalizeBaseURL(base string) string {
	return strings.TrimRight(strings.TrimSpace(base), "/")
}

func IsDefaultAPIBaseURL(base string) bool {
	return normalizeBaseURL(base) == normalizeBaseURL(xaiShared.DefaultAPIBaseURL)
}

func IsCLIChatProxyBaseURL(base string) bool {
	return normalizeBaseURL(base) == normalizeBaseURL(xaiShared.CLIChatProxyBaseURL)
}

// ResolveMediaBaseURL 图片/视频/WebSocket：官方 API 或显式非空配置。
func ResolveMediaBaseURL(config *xaiShared.XaiMultiConfig) string {
	if base := configuredBaseURL(config); base != "" {
		return base
	}
	return xaiShared.DefaultAPIBaseURL
}

// ResolveChatBaseURL 非媒体 HTTP 文本 base。
// usingApi=true（或 api_key 默认）：官方 API / 配置 base。
// usingApi=false（或 oauth 默认）：空或官方默认 base 改写到 cli-chat-proxy；显式自定义 base 保留。
func ResolveChatBaseURL(config *xaiShared.XaiMultiConfig, account *xaiShared.XaiAccount) string {
	base := configuredBaseURL(config)
	if account.UsingAPIEnabled() {
		if base == "" {
			return xaiShared.DefaultAPIBaseURL
		}
		return base
	}
	if base == "" || IsDefaultAPIBaseURL(base) {
		return xaiShared.CLIChatProxyBaseURL
	}
	return base
}

// ResolveUpstreamBaseURL 按路径选择 chat / media base。
// compact：cli-chat-proxy 无 /responses/compact（404 空 body），走官方 API（对齐可用端点）。
// 文本 chat：由账号 usingApi / authKind 决定官方 API 或 cli-chat-proxy。
func ResolveUpstreamBaseURL(config *xaiShared.XaiMultiConfig, account *xaiShared.XaiAccount, inboundPath string) string {
	if IsMediaPath(inboundPath) || IsCompactPath(inboundPath) {
		return ResolveMediaBaseURL(config)
	}
	return ResolveChatBaseURL(config, account)
}

func UpstreamURL(config *xaiShared.XaiMultiConfig, account *xaiShared.XaiAccount, inboundPath string) string {
	return joinBasePath(ResolveUpstreamBaseURL(config, account, inboundPath), MapInboundPath(inboundPath))
}

// UpstreamWebsocketURL WebSocket 固定官方 API（cli-chat-proxy 不支持 upgrade）。
func UpstreamWebsocketURL(config *xaiShared.XaiMultiConfig, account *xaiShared.XaiAccount, inboundPath string) string {
	_ = account
	return joinBasePath(ResolveMediaBaseURL(config), MapInboundPath(inboundPath))
}

func joinBasePath(base, upstreamPath string) string {
	base = normalizeBaseURL(base)
	if base == "" {
		base = xaiShared.DefaultAPIBaseURL
	}
	upstreamPath = normalizePath(upstreamPath)
	if upstreamPath == "/" {
		return base
	}
	return base + upstreamPath
}

func BuildWebsocketURL(httpURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(httpURL))
	if err != nil {
		return "", err
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported websocket URL scheme %q", parsed.Scheme)
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("websocket URL host is empty")
	}
	return parsed.String(), nil
}

func PrepareHTTPRequest(ctx context.Context, req Request) (*http.Request, []byte, ReplayScope, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodPost
	}
	targetURL := UpstreamURL(req.Config, req.Account, req.Path)
	if q := strings.TrimSpace(req.RawQuery); q != "" {
		if strings.Contains(targetURL, "?") {
			targetURL += "&" + q
		} else {
			targetURL += "?" + q
		}
	}

	var bodyReader *bytes.Reader
	body := append([]byte(nil), req.Body...)
	if method == http.MethodGet || method == http.MethodHead {
		body = nil
	}

	// Responses 路径：sanitize / thinking / stream 对齐
	sessionID := sessionIDFromHeaders(req.Headers)
	var replayScope ReplayScope
	if body != nil && IsResponsesPath(req.Path) && !IsMediaPath(req.Path) {
		// compact 除外强制 stream=true。
		upstreamStream := req.IsStreaming
		baseForStream := ResolveUpstreamBaseURL(req.Config, req.Account, req.Path)
		if !IsCompactPath(req.Path) && IsCLIChatProxyBaseURL(baseForStream) {
			upstreamStream = true
		}
		// 优先使用 plan 注入的 x-xai-replay-session（Claude 转换前解析的 key）
		replayKey := ""
		if req.Headers != nil {
			replayKey = strings.TrimSpace(req.Headers.Get("x-xai-replay-session"))
		}
		if replayKey == "" {
			replayKey = ResolveReplaySessionKey(body, req.Headers, sessionID)
		}
		prepared, err := PrepareResponsesBody(body, PrepareOptions{
			Stream:           upstreamStream,
			Model:            req.Model,
			SessionID:        sessionID,
			IsCompact:        IsCompactPath(req.Path),
			EnableReplay:     req.EnableReplay && replayKey != "",
			ReplaySessionKey: replayKey,
		})
		if err != nil {
			return nil, nil, ReplayScope{}, err
		}
		if prepared != nil {
			body = prepared.Body
			if prepared.SessionID != "" {
				sessionID = prepared.SessionID
			}
			replayScope = prepared.ReplayScope
		}
	}

	if body != nil {
		bodyReader = bytes.NewReader(body)
	} else {
		bodyReader = bytes.NewReader(nil)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return nil, nil, replayScope, err
	}
	// 注入 prepare 解析出的 session（composer 自动生成时客户端可能未带）
	if sessionID != "" && httpReq.Header.Get(HeaderGrokConvID) == "" {
		if req.Headers == nil {
			req.Headers = http.Header{}
		}
		if strings.TrimSpace(req.Headers.Get(HeaderGrokConvID)) == "" {
			req.Headers.Set(HeaderGrokConvID, sessionID)
		}
	}
	ApplyHeaders(httpReq, req)
	if sessionID != "" && strings.TrimSpace(httpReq.Header.Get(HeaderGrokConvID)) == "" {
		httpReq.Header.Set(HeaderGrokConvID, sessionID)
	}
	return httpReq, body, replayScope, nil
}

func ApplyHeaders(httpReq *http.Request, req Request) {
	if httpReq == nil {
		return
	}
	// 不透传客户端杂项头（尤其禁止 x-api-key / User-Agent 污染上游）
	// 仅白名单：x-grok-conv-id / x-idempotency-key / x-request-id
	copyAllowedInboundHeaders(httpReq.Header, req.Headers)

	token := strings.TrimSpace(req.AccessToken)
	if token == "" && req.Account != nil {
		token = req.Account.BearerToken()
	}
	sessionID := sessionIDFromHeaders(req.Headers)
	if sessionID == "" {
		sessionID = sessionIDFromHeaders(httpReq.Header)
	}

	// chat Execute/Stream 始终 stream=true + Accept: text/event-stream。
	// compact / media 保持调用方 IsStreaming。
	stream := req.IsStreaming
	base := ResolveUpstreamBaseURL(req.Config, req.Account, req.Path)
	if IsResponsesPath(req.Path) && !IsCompactPath(req.Path) && IsCLIChatProxyBaseURL(base) {
		stream = true
	}

	applyXAICoreHeaders(httpReq, token, stream, sessionID)

	// Grok CLI 身份头：仅 cli-chat-proxy（对齐 applyXAIChatHeaders / using_api=false）
	if IsCLIChatProxyBaseURL(base) {
		authKind := ""
		if req.Account != nil {
			authKind = req.Account.AuthKind
		}
		applyGrokCLIIdentityHeaders(httpReq, req.Config, authKind)
	}

	// 全局 custom headers，再叠账号级（后写覆盖）
	if req.Config != nil {
		applyCustomHeaders(httpReq, req.Config.Config.CustomHeaders)
	}
	if req.Account != nil {
		applyCustomHeaders(httpReq, req.Account.CustomHeaders)
	}
}

func ApplyWebsocketHeaders(token string, sessionID string, config *xaiShared.XaiMultiConfig) http.Header {
	headers := http.Header{}
	// WebSocket 握手：不要设 Connection/Upgrade/Content-Type（gorilla 会自己写；
	// 再写 Connection: Keep-Alive 会 duplicate header 导致 dial 失败）。
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

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	return path
}

func SanitizeHeaders(headers http.Header) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		if strings.EqualFold(key, "Authorization") {
			out[key] = "Bearer ***"
			continue
		}
		out[key] = values[0]
	}
	return out
}
