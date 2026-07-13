package backend

// HTTP executor：header/body/response 按请求种类分流。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/tiktoken-go/tokenizer"

	xaiShared "clisimplehub/internal/xai/shared"
	xaithinking "clisimplehub/internal/xai/thinking"
)

// RequestKind 按请求种类分流 header / body / response。
type RequestKind int

const (
	KindResponses RequestKind = iota
	KindCompact
	KindImages
	KindVideos
	KindModels
	KindWebsocket
	KindUnknown
)

// ClientMode：Compat（Responses wire）/ Transform（chat·claude 已转 Responses）。
// 所有路径统一 prepare；无 Native 旁路。
type ClientMode string

const (
	ClientModeAuto      ClientMode = "" // 自动：默认 Compat
	ClientModeCompat    ClientMode = "compat"
	ClientModeTransform ClientMode = "transform"
)

type Request struct {
	Method      string
	Path        string // inbound path, e.g. /xai/v1/responses
	RawQuery    string
	Body        []byte
	Headers     http.Header
	IsStreaming bool
	Model       string
	// SourceType 标识进入 Responses wire 前的来源协议，用于统一准备阶段的来源相关规则。
	SourceType string

	Config      *xaiShared.XaiMultiConfig
	Account     *xaiShared.XaiAccount
	AccessToken string
	ProxyURL    string
	Client      *http.Client

	// ClientMode：空则 Resolve 为 Compat；Transform 表示上游前发生过协议转换
	ClientMode ClientMode

	// EnableReplay：Claude 等多轮源启用 reasoning replay
	EnableReplay bool

	Attempts   int
	RetryDelay time.Duration
}

type Result struct {
	StatusCode    int
	Headers       http.Header
	Body          []byte
	Stream        io.ReadCloser
	TargetURL     string
	TargetHeaders map[string]string
	RequestBody   []byte
	ReplayScope   ReplayScope
	Error         error
}

type InvalidRequestError struct{ Err error }

func (e *InvalidRequestError) Error() string { return e.Err.Error() }
func (e *InvalidRequestError) Unwrap() error { return e.Err }

func IsInvalidRequestError(err error) bool {
	var target *InvalidRequestError
	return errors.As(err, &target)
}

// ResolveClientMode：发生协议转换 → Transform，否则一律 Compat。
// User-Agent 不得触发未经校验的原样旁路。
func ResolveClientMode(explicit ClientMode, userAgent string, path string, body []byte, needsFormatTransform bool) ClientMode {
	_ = userAgent
	_ = path
	_ = body
	if needsFormatTransform {
		return ClientModeTransform
	}
	switch explicit {
	case ClientModeTransform:
		// 无 format 转换时显式 Transform 仍收敛为 Compat（已是 wire）
		return ClientModeCompat
	case ClientModeCompat:
		return ClientModeCompat
	}
	return ClientModeCompat
}

// isResponsesLikePath 兼容 /xai/v1/responses 与网关 /v1/responses。
func isResponsesLikePath(path string) bool {
	if IsResponsesPath(path) {
		return true
	}
	p := strings.ToLower(strings.TrimRight(strings.TrimSpace(path), "/"))
	return strings.HasSuffix(p, "/responses") || strings.HasSuffix(p, "/responses/compact")
}

// MapInboundPath converts /xai/v1/... inbound path to upstream path under base.
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

// ClassifyPath 按路径判定请求种类。
func ClassifyPath(path string) RequestKind {
	p := MapInboundPath(path)
	switch {
	case p == "/responses/compact":
		return KindCompact
	case strings.HasPrefix(p, "/images/"):
		return KindImages
	case p == "/videos" || strings.HasPrefix(p, "/videos/"):
		return KindVideos
	case p == "/models":
		return KindModels
	case p == "/responses" || strings.HasPrefix(p, "/responses/"):
		return KindResponses
	default:
		return KindUnknown
	}
}

func IsResponsesPath(path string) bool {
	switch ClassifyPath(path) {
	case KindResponses, KindCompact:
		return true
	default:
		return false
	}
}

func IsCompactPath(path string) bool {
	return ClassifyPath(path) == KindCompact
}

func IsImagesPath(path string) bool {
	return ClassifyPath(path) == KindImages
}

func IsVideosPath(path string) bool {
	return ClassifyPath(path) == KindVideos
}

func IsModelsPath(path string) bool {
	return ClassifyPath(path) == KindModels
}

// IsMediaPath 图片/视频走官方 API，不改写到 cli-chat-proxy。
func IsMediaPath(path string) bool {
	switch ClassifyPath(path) {
	case KindImages, KindVideos:
		return true
	default:
		return false
	}
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
// 媒体路径不走 chat-proxy 改写。
func ResolveMediaBaseURL(config *xaiShared.XaiMultiConfig) string {
	if base := configuredBaseURL(config); base != "" {
		return base
	}
	return xaiShared.DefaultAPIBaseURL
}

// ResolveChatBaseURL 非媒体 HTTP 文本 base：
//
//	usingApi=true → 官方 API / 配置 base
//	usingApi=false → 空或默认官方 base 改写到 cli-chat-proxy；显式自定义 base 保留
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

// ResolveUpstreamBaseURL 按种类选择 base。
// responses/compact 用 chat base；media 用 media base。
func ResolveUpstreamBaseURL(config *xaiShared.XaiMultiConfig, account *xaiShared.XaiAccount, inboundPath string) string {
	if IsMediaPath(inboundPath) {
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

func appendRawQuery(targetURL, rawQuery string) string {
	q := strings.TrimSpace(rawQuery)
	if q == "" {
		return targetURL
	}
	if strings.Contains(targetURL, "?") {
		return targetURL + "&" + q
	}
	return targetURL + "?" + q
}

func resolveAccessToken(req Request) string {
	token := strings.TrimSpace(req.AccessToken)
	if token == "" && req.Account != nil {
		token = req.Account.BearerToken()
	}
	return token
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

const (
	DefaultClientVersion = "0.2.93"
	// CLI chat-proxy 身份 User-Agent（xai-grok-workspace/<ver>）
	DefaultUserAgent     = "xai-grok-workspace/" + DefaultClientVersion
	DefaultTokenAuth     = "xai-grok-cli"
	DefaultClientSurface = "grok-cli"
	HeaderClientVersion  = "x-grok-client-version"
	HeaderClientSurface  = "x-grok-client-surface"
	HeaderTokenAuth      = "X-XAI-Token-Auth"
	HeaderGrokConvID     = "x-grok-conv-id"
	HeaderIdempotencyKey = "x-idempotency-key"
	// HeaderExecutionSessionID 轻量跨请求/WS 执行会话。
	HeaderExecutionSessionID = "X-Execution-Session-Id"
	// ExecutionSessionMetadataKey TransformContext / opts 元数据键。
	ExecutionSessionMetadataKey = "execution_session_id"
)

// applyXAIDefaultHeaders 写入默认上游请求头。
func applyXAIDefaultHeaders(r *http.Request, token string, stream bool, sessionID string) {
	if r == nil {
		return
	}
	r.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(token) != "" {
		r.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	if stream {
		r.Header.Set("Accept", "text/event-stream")
	} else {
		r.Header.Set("Accept", "application/json")
	}
	r.Header.Set("Connection", "Keep-Alive")
	if sid := strings.TrimSpace(sessionID); sid != "" {
		r.Header.Set(HeaderGrokConvID, sid)
	}
}

// applyXAICustomHeaders 全局 + 账号 custom headers（后写覆盖）。
func applyXAICustomHeaders(r *http.Request, config *xaiShared.XaiMultiConfig, account *xaiShared.XaiAccount) {
	if r == nil {
		return
	}
	if config != nil {
		applyCustomHeaderMap(r, config.Config.CustomHeaders)
	}
	if account != nil {
		applyCustomHeaderMap(r, account.CustomHeaders)
	}
}

func applyCustomHeaderMap(r *http.Request, headers map[string]string) {
	if r == nil || len(headers) == 0 {
		return
	}
	for k, v := range headers {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		r.Header.Set(k, v)
	}
}

// applyXAIMediaHeaders 图片/视频：默认头 + custom，无 CLI 身份头。
func applyXAIMediaHeaders(r *http.Request, token string, sessionID string, config *xaiShared.XaiMultiConfig, account *xaiShared.XaiAccount) {
	applyXAIDefaultHeaders(r, token, false, sessionID)
	applyXAICustomHeaders(r, config, account)
}

// applyXAIChatHeaders 非媒体 HTTP 文本（responses / compact）。
// using_api=true：与 media 相同（官方 API）。
// applyXAIChatHeaders using_api=false 且 base 为 cli-chat-proxy 时附加 Grok CLI 身份头。
func applyXAIChatHeaders(r *http.Request, token string, stream bool, sessionID string, config *xaiShared.XaiMultiConfig, account *xaiShared.XaiAccount) {
	if r == nil {
		return
	}
	if account.UsingAPIEnabled() {
		applyXAIDefaultHeaders(r, token, stream, sessionID)
		applyXAICustomHeaders(r, config, account)
		return
	}
	applyXAIDefaultHeaders(r, token, stream, sessionID)
	base := ResolveChatBaseURL(config, account)
	if IsCLIChatProxyBaseURL(base) {
		version := DefaultClientVersion
		tokenAuth := DefaultTokenAuth
		userAgent := DefaultUserAgent
		if config != nil {
			if v := strings.TrimSpace(config.Config.ClientVersion); v != "" {
				version = v
				userAgent = "xai-grok-workspace/" + v
			}
			if v := strings.TrimSpace(config.Config.TokenAuth); v != "" {
				tokenAuth = v
			}
			if v := strings.TrimSpace(config.Config.UserAgent); v != "" {
				userAgent = v
			}
		}
		// CLI chat proxy 始终要求 token-auth，与账号凭据类型无关。
		r.Header.Set(HeaderTokenAuth, tokenAuth)
		r.Header.Set(HeaderClientVersion, version)
		r.Header.Set("User-Agent", userAgent)
	}
	applyXAICustomHeaders(r, config, account)
}

// copyAllowedInboundHeaders 仅透传上游有意义且安全的入站头。
func copyAllowedInboundHeaders(dst http.Header, src http.Header) {
	if dst == nil || src == nil {
		return
	}
	for _, key := range []string{HeaderIdempotencyKey, "x-request-id"} {
		if v := headerGet(src, key); v != "" {
			dst.Set(key, v)
		}
	}
}

// ExecutionSessionIDFromHeaders 读取轻量 execution_session_id（跨 HTTP/WS 绑定）。
// 支持 X-Execution-Session-Id / x-execution-session-id 等大小写变体。
func ExecutionSessionIDFromHeaders(h http.Header) string {
	if h == nil {
		return ""
	}
	for _, name := range []string{
		HeaderExecutionSessionID,
		"x-execution-session-id",
		"Execution-Session-Id",
		"execution_session_id",
	} {
		if s := headerGet(h, name); s != "" {
			return s
		}
	}
	return ""
}

// ExecutionSessionIDFromMeta 从 map 元数据取 execution_session_id。
func ExecutionSessionIDFromMeta(meta map[string]any) string {
	if len(meta) == 0 {
		return ""
	}
	v, ok := meta[ExecutionSessionMetadataKey]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

// ResolveUpstreamSessionID 解析上游会话 ID
// 优先级：
//  1. explicit（WS 连接级 / 调用方已确定）
//  2. header X-Execution-Session-Id（metadata 注入；跨 HTTP/WS 绑定）
//  3. body.prompt_cache_key
//  4. grok-composer-*：Claude Code 会话 → 稳定 UUID
//  5. grok-composer-*：随机 UUID
func ResolveUpstreamSessionID(body []byte, headers http.Header, explicit, model string) string {
	if s := strings.TrimSpace(explicit); s != "" {
		return s
	}
	if s := ExecutionSessionIDFromHeaders(headers); s != "" {
		return s
	}
	if len(body) > 0 && gjson.ValidBytes(body) {
		if v := gjson.GetBytes(body, "prompt_cache_key"); v.Exists() {
			if s := strings.TrimSpace(v.String()); s != "" {
				return s
			}
		}
	}

	base := BaseModelName(model)
	if base == "" && len(body) > 0 {
		base = BaseModelName(gjson.GetBytes(body, "model").String())
	}
	if !RequiresIsolatedConversation(base) {
		return ""
	}
	if s := StableClaudeCodePromptCacheKey(base, body, headers); s != "" {
		return s
	}
	return uuid.NewString()
}

// StableClaudeCodePromptCacheKey 将 Claude Code session 映射为稳定的上游 prompt_cache_key。
// 使用确定性 UUID，进程重启后仍稳定。
func StableClaudeCodePromptCacheKey(model string, body []byte, headers http.Header) string {
	sid := ExtractClaudeCodeSessionID(body, headers)
	if sid == "" {
		return ""
	}
	name := strings.Join([]string{
		"clisimplehub", "xai", "claude-prompt-cache",
		BaseModelName(model), strings.TrimSpace(sid),
	}, ":")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)).String()
}

func headerGet(h http.Header, key string) string {
	if h == nil {
		return ""
	}
	if v := strings.TrimSpace(h.Get(key)); v != "" {
		return v
	}
	for k, vals := range h {
		if strings.EqualFold(k, key) && len(vals) > 0 {
			return strings.TrimSpace(vals[0])
		}
	}
	return ""
}

type xaiModelDefinition struct {
	ID              string
	Created         int64
	ReasoningLevels []string
}

// models 与 thinking 共用此事实源。
var xaiModelCatalog = []xaiModelDefinition{
	{ID: "grok-build-0.1", Created: 1779321600},
	{ID: "grok-4.5", Created: 1783526400, ReasoningLevels: []string{"low", "medium", "high"}},
	{ID: "grok-4.3", Created: 1775606400, ReasoningLevels: []string{"none", "low", "medium", "high"}},
	{ID: "grok-4.20-0309-reasoning", Created: 1773014400}, {ID: "grok-4.20-0309-non-reasoning", Created: 1773014400},
	{ID: "grok-4.20-multi-agent-0309", Created: 1773014400, ReasoningLevels: []string{"low", "medium", "high"}},
	{ID: "grok-3-mini", Created: 1740960000, ReasoningLevels: []string{"low", "medium", "high"}},
	{ID: "grok-3-mini-fast", Created: 1740960000, ReasoningLevels: []string{"low", "medium", "high"}},
	{ID: "grok-composer-2.5-fast", Created: 1740960000},
	{ID: "grok-imagine-image", Created: 1735689600}, {ID: "grok-imagine-image-quality", Created: 1735689600},
	{ID: "grok-imagine-video", Created: 1735689600}, {ID: "grok-imagine-video-1.5-preview", Created: 1735689600},
}

func lookupXAIModel(model string) *xaiModelDefinition {
	name := strings.ToLower(BaseModelName(model))
	for i := range xaiModelCatalog {
		if xaiModelCatalog[i].ID == name {
			return &xaiModelCatalog[i]
		}
	}
	return nil
}

type ModelSuffix struct {
	ModelName string
	HasSuffix bool
	RawSuffix string
}

// ParseModelSuffix 解析 model(value)；委托 thinking.ParseSuffix。
func ParseModelSuffix(model string) ModelSuffix {
	s := xaithinking.ParseSuffix(model)
	return ModelSuffix{ModelName: s.ModelName, HasSuffix: s.HasSuffix, RawSuffix: strings.ToLower(s.RawSuffix)}
}

func BaseModelName(model string) string {
	name := ParseModelSuffix(model).ModelName
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	return strings.TrimSpace(name)
}

func SupportsReasoningEffort(model string) bool {
	info := lookupXAIModel(model)
	return info != nil && len(info.ReasoningLevels) > 0
}

func RequiresIsolatedConversation(model string) bool {
	return strings.HasPrefix(strings.ToLower(BaseModelName(model)), "grok-composer-")
}

// LookupThinkingModel 将 catalog 映射为 thinking.ModelInfo。
func LookupThinkingModel(model string) *xaithinking.ModelInfo {
	info := lookupXAIModel(model)
	if info == nil {
		return nil
	}
	mi := &xaithinking.ModelInfo{ID: info.ID, Type: "xai"}
	if len(info.ReasoningLevels) == 0 {
		return mi
	}
	zeroAllowed := false
	for _, level := range info.ReasoningLevels {
		if strings.EqualFold(level, "none") {
			zeroAllowed = true
			break
		}
	}
	mi.Thinking = &xaithinking.ThinkingSupport{
		Levels:      append([]string(nil), info.ReasoningLevels...),
		ZeroAllowed: zeroAllowed,
	}
	return mi
}

// ApplyThinking 统一入口：完整 ApplyThinking 管线（suffix 优先、clamp、auto→medium）。
func ApplyThinking(body []byte, model, sourceType string) ([]byte, error) {
	from := xaithinking.SourceTypeToFromFormat(sourceType)
	return xaithinking.ApplyThinking(body, model, from, LookupThinkingModel)
}

func stripReasoningEffort(body []byte) []byte {
	return xaithinking.StripEffort(body)
}

// rewriteModelInBody 将 body.model 设为 base name。
func rewriteModelInBody(body []byte, baseModel string) []byte {
	baseModel = strings.TrimSpace(baseModel)
	if baseModel == "" || len(body) == 0 {
		return body
	}
	out, err := sjson.SetBytes(body, "model", baseModel)
	if err != nil {
		return body
	}
	return out
}

// setStreamFlag 强制 stream 字段与调用一致。
func setStreamFlag(body []byte, stream bool) []byte {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body
	}
	out, err := sjson.SetBytes(body, "stream", stream)
	if err != nil {
		return body
	}
	return out
}

func isValidJSONObject(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var v any
	return json.Unmarshal(body, &v) == nil && gjson.ValidBytes(body)
}

const (
	toolCustom          = "custom"
	toolFunction        = "function"
	toolImageGeneration = "image_generation"
	toolNamespace       = "namespace"
	toolSearch          = "tool_search"
	toolWebSearch       = "web_search"

	codexAppNamespace      = "codex_app"
	automationUpdateTool   = "automation_update"
	safeFunctionParameters = `{"type":"object","properties":{},"additionalProperties":true}`
)

// NormalizeTools 展开 namespace、过滤/改写上游不支持或会卡死的 tool schema。
// 无论 tools 是否存在，结尾都会 NormalizeToolChoice（无 tools 时清 tool_choice）。
func NormalizeTools(body []byte) []byte {
	tools := gjson.GetBytes(body, "tools")
	if !tools.Exists() || !tools.IsArray() {
		return NormalizeToolChoice(body)
	}

	changed := false
	filtered := []byte(`[]`)
	for _, tool := range tools.Array() {
		toolType := tool.Get("type").String()
		if toolType == toolNamespace {
			changed = true
			namespaceName := tool.Get("name").String()
			if namespaceTools := tool.Get("tools"); namespaceTools.IsArray() {
				for _, nested := range namespaceTools.Array() {
					raw, nestedChanged, ok := normalizeTool(nested, namespaceName)
					if !ok {
						return body
					}
					changed = changed || nestedChanged
					if len(raw) == 0 {
						continue
					}
					updated, err := sjson.SetRawBytes(filtered, "-1", raw)
					if err != nil {
						return body
					}
					filtered = updated
				}
			}
			continue
		}
		raw, toolChanged, ok := normalizeTool(tool, "")
		if !ok {
			return body
		}
		changed = changed || toolChanged
		if len(raw) == 0 {
			continue
		}
		updated, err := sjson.SetRawBytes(filtered, "-1", raw)
		if err != nil {
			return body
		}
		filtered = updated
	}
	if !changed {
		// 仍可能需要 tool_choice 清理
		return NormalizeToolChoice(body)
	}
	updated, err := sjson.SetRawBytes(body, "tools", filtered)
	if err != nil {
		return body
	}
	return NormalizeToolChoice(updated)
}

func normalizeTool(tool gjson.Result, namespaceName string) ([]byte, bool, bool) {
	toolType := tool.Get("type").String()
	changed := false
	// tool_search / image_generation 由上游侧处理，Responses tools 列表中剥离
	if toolType == toolSearch || toolType == toolImageGeneration {
		return nil, true, true
	}
	raw := []byte(tool.Raw)
	if toolType == toolCustom {
		if tool.Get("name").String() == "apply_patch" {
			return nil, true, true
		}
		updated, err := sjson.SetBytes(raw, "type", toolFunction)
		if err != nil {
			return nil, false, false
		}
		raw = updated
		toolType = toolFunction
		changed = true
	}
	if toolType == toolWebSearch && tool.Get("external_web_access").Exists() {
		updated, err := sjson.DeleteBytes(raw, "external_web_access")
		if err != nil {
			return nil, false, false
		}
		raw = updated
		changed = true
	}
	if toolType == toolFunction && !gjson.GetBytes(raw, "parameters").Exists() {
		updated, err := sjson.SetRawBytes(raw, "parameters", []byte(`{"type":"object","properties":{}}`))
		if err != nil {
			return nil, false, false
		}
		raw = updated
		changed = true
	}
	// Codex Desktop codex_app.automation_update 大 schema 会导致上游卡 SSE
	if toolType == toolFunction && needsSimplifiedParameters(tool, namespaceName) {
		updated, err := sjson.SetRawBytes(raw, "parameters", []byte(safeFunctionParameters))
		if err != nil {
			return nil, false, false
		}
		raw = updated
		if strict := tool.Get("strict"); strict.Exists() && strict.Bool() {
			updated, err = sjson.SetBytes(raw, "strict", false)
			if err != nil {
				return nil, false, false
			}
			raw = updated
		}
		changed = true
	}
	return raw, changed, true
}

func needsSimplifiedParameters(tool gjson.Result, namespaceName string) bool {
	return strings.EqualFold(strings.TrimSpace(tool.Get("type").String()), toolFunction) &&
		strings.EqualFold(strings.TrimSpace(namespaceName), codexAppNamespace) &&
		strings.EqualFold(strings.TrimSpace(tool.Get("name").String()), automationUpdateTool)
}

// NormalizeToolChoice 无 tools 时删除 tool_choice / parallel_tool_calls。
func NormalizeToolChoice(body []byte) []byte {
	tools := gjson.GetBytes(body, "tools")
	hasTools := tools.Exists() && tools.IsArray() && len(tools.Array()) > 0
	if hasTools {
		return body
	}
	if tools.Exists() {
		body, _ = sjson.DeleteBytes(body, "tools")
	}
	if gjson.GetBytes(body, "tool_choice").Exists() {
		body, _ = sjson.DeleteBytes(body, "tool_choice")
	}
	if gjson.GetBytes(body, "parallel_tool_calls").Exists() {
		body, _ = sjson.DeleteBytes(body, "parallel_tool_calls")
	}
	return body
}

// StaticModelIDs 返回静态模型 id 列表（端点 Models/Routes 默认种子）。
func StaticModelIDs() []string {
	out := make([]string, 0, len(xaiModelCatalog))
	for _, m := range xaiModelCatalog {
		out = append(out, m.ID)
	}
	return out
}

// LocalModelsResponse 返回 OpenAI 兼容的 models 列表响应体。
func LocalModelsResponse() map[string]any {
	data := make([]map[string]any, 0, len(xaiModelCatalog))
	for _, m := range xaiModelCatalog {
		item := map[string]any{
			"id": m.ID, "object": "model", "created": m.Created, "owned_by": "xai",
		}
		data = append(data, item)
	}
	return map[string]any{
		"object": "list",
		"data":   data,
	}
}

// EstimatePreparedTokens 对 prepare 后的 Responses body 做 token 估算
func EstimatePreparedTokens(preparedBody []byte) int {
	if len(preparedBody) == 0 {
		return 0
	}
	enc, err := tokenizer.Get(tokenizer.Cl100kBase)
	if err != nil {
		// 编码器不可用时退回粗估，避免 count_tokens 硬失败
		n := (len(preparedBody) + 3) / 4
		if n < 1 {
			return 1
		}
		return n
	}
	ids, _, err := enc.Encode(string(preparedBody))
	if err != nil {
		n := (len(preparedBody) + 3) / 4
		if n < 1 {
			return 1
		}
		return n
	}
	if len(ids) < 1 {
		return 1
	}
	return len(ids)
}

// CountTokensForRequest 对请求 body 先 prepare 再估算。
func CountTokensForRequest(body []byte, model string, enableReplay bool, sessionKey string) (int, error) {
	prepared, err := PrepareResponsesBody(body, PrepareOptions{
		Stream:           false,
		Model:            model,
		IsCompact:        false,
		EnableReplay:     enableReplay,
		ReplaySessionKey: sessionKey,
	})
	if err != nil {
		return EstimatePreparedTokens(body), nil
	}
	if prepared == nil {
		return EstimatePreparedTokens(body), nil
	}
	return EstimatePreparedTokens(prepared.Body), nil
}

// FormatClaudeCountTokensResponse Claude count_tokens 响应体。
func FormatClaudeCountTokensResponse(tokens int) []byte {
	if tokens < 1 {
		tokens = 1
	}
	return []byte(`{"input_tokens":` + itoa(tokens) + `}`)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// ExtractModelForCount 从 body 取 model。
func ExtractModelForCount(body []byte) string {
	return BaseModelName(gjson.GetBytes(body, "model").String())
}

// PreparedRequest 准备后的 Responses 请求。
type PreparedRequest struct {
	BaseModel   string
	SourceType  string
	Body        []byte
	SessionID   string
	ReplayScope ReplayScope
}

// PrepareOptions 控制 prepare 行为。
type PrepareOptions struct {
	Stream       bool
	Model        string
	SourceType   string // openai-response、chat 或 claude
	SessionID    string // 显式会话（已确定时传入；空则按 body/header/Claude 解析）
	IsWebsocket  bool
	IsCompact    bool
	KeepPrevious bool // WS 路径可保留 previous_response_id
	// EnableReplay：Claude 等多轮源启用 reasoning replay 注入
	EnableReplay bool
	// ReplaySessionKey：连续对话 key（空则从 body/session 推导）
	ReplaySessionKey string
	// Headers：用于 session / Claude / replay 解析
	Headers http.Header
}

// 仅处理 Responses JSON；媒体路径不应调用。
func PrepareResponsesBody(body []byte, opts PrepareOptions) (*PreparedRequest, error) {
	sourceType := normalizeSourceType(opts.SourceType)
	if len(body) == 0 {
		return &PreparedRequest{Body: body, SourceType: sourceType}, nil
	}
	if !gjson.ValidBytes(body) {
		return nil, &InvalidRequestError{Err: fmt.Errorf("invalid responses request json")}
	}

	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = strings.TrimSpace(gjson.GetBytes(body, "model").String())
	}
	baseModel := BaseModelName(model)
	if baseModel == "" {
		baseModel = BaseModelName(gjson.GetBytes(body, "model").String())
	}

	out := append([]byte(nil), body...)
	out, err := ApplyThinking(out, model, sourceType)
	if err != nil {
		return nil, &InvalidRequestError{Err: err}
	}
	if baseModel != "" {
		out = rewriteModelInBody(out, baseModel)
	}
	if !opts.IsWebsocket {
		// HTTP 路径：强制 stream 与调用一致
		out = setStreamFlag(out, opts.Stream && !opts.IsCompact)
	} else {
		out, _ = sjson.DeleteBytes(out, "stream")
		out, _ = sjson.DeleteBytes(out, "stream_options")
	}

	if !opts.KeepPrevious {
		out, _ = sjson.DeleteBytes(out, "previous_response_id")
	}
	out, _ = sjson.DeleteBytes(out, "prompt_cache_retention")
	out, _ = sjson.DeleteBytes(out, "safety_identifier")
	if !opts.IsWebsocket {
		out, _ = sjson.DeleteBytes(out, "stream_options")
	}

	out = NormalizeTools(out)
	// 无 tools 字段时 NormalizeTools 已清理；此处再跑一次保证幂等
	out = NormalizeToolChoice(out)

	// execution_session / body.prompt_cache_key / composer；
	sessionID := ResolveUpstreamSessionID(out, opts.Headers, opts.SessionID, baseModel)
	if sessionID != "" {
		out, _ = sjson.SetBytes(out, "prompt_cache_key", sessionID)
	}

	// Replay 注入须在 sanitize 之前（否则 encrypted_content 会被剥掉）
	replaySession := strings.TrimSpace(opts.ReplaySessionKey)
	if replaySession == "" {
		replaySession = ResolveReplaySessionKey(out, opts.Headers, sessionID)
	}
	var replayScope ReplayScope
	if opts.EnableReplay {
		out, replayScope = ApplyReasoningReplay(out, baseModel, replaySession, true)
	} else {
		replayScope = ReplayScope{ModelName: baseModel, SessionKey: replaySession}
	}

	out = normalizeInputReasoningItems(out)
	out = sanitizeInputEncryptedContent(out)
	out = normalizeCodexInstructions(out)
	out = sanitizeResponsesBody(out, baseModel)

	if opts.IsCompact {
		out, _ = sjson.DeleteBytes(out, "stream")
		out, _ = sjson.DeleteBytes(out, "tools")
		out = removeInputItemsByType(out, "compaction_trigger")
	}

	// sanitize 后仍保证 prompt_cache_key 与 session 对齐
	if sessionID != "" {
		out, _ = sjson.SetBytes(out, "prompt_cache_key", sessionID)
	}
	// 若未显式指定 replay key，用最终 session 回填
	if !replayScope.Valid() && sessionID != "" {
		replayScope = ReplayScope{ModelName: baseModel, SessionKey: "prompt-cache:" + sessionID}
	}

	if opts.IsWebsocket {
		// WS 上游要求 store=true 以支持 previous_response_id
		out, _ = sjson.SetBytes(out, "store", true)
		if t := strings.TrimSpace(gjson.GetBytes(out, "type").String()); t == "" {
			out, _ = sjson.SetBytes(out, "type", "response.create")
		}
	}

	return &PreparedRequest{
		BaseModel:   baseModel,
		SourceType:  sourceType,
		Body:        out,
		SessionID:   sessionID,
		ReplayScope: replayScope,
	}, nil
}

func normalizeSourceType(sourceType string) string {
	switch strings.ToLower(strings.TrimSpace(sourceType)) {
	case "chat", "claude":
		return strings.ToLower(strings.TrimSpace(sourceType))
	case "codex", "responses", "openai-response", "":
		return "openai-response"
	default:
		return strings.ToLower(strings.TrimSpace(sourceType))
	}
}

// normalizeCodexInstructions：instructions=null → ""
func normalizeCodexInstructions(body []byte) []byte {
	instructions := gjson.GetBytes(body, "instructions")
	if !instructions.Exists() || instructions.Type == gjson.Null {
		body, _ = sjson.SetBytes(body, "instructions", "")
	}
	return body
}

func sanitizeResponsesBody(body []byte, model string) []byte {
	if lookupXAIModel(model) == nil {
		return body
	}
	if SupportsReasoningEffort(model) {
		return body
	}
	return stripReasoningEffort(body)
}

func normalizeInputReasoningItems(body []byte) []byte {
	input := gjson.GetBytes(body, "input")
	if !input.Exists() || !input.IsArray() {
		return body
	}
	updated := body
	for i, item := range input.Array() {
		if item.Get("type").String() != "reasoning" {
			continue
		}
		contentPath := fmt.Sprintf("input.%d.content", i)
		if content := gjson.GetBytes(updated, contentPath); content.Exists() && content.Type == gjson.Null {
			if next, err := sjson.DeleteBytes(updated, contentPath); err == nil {
				updated = next
			}
		}
		encPath := fmt.Sprintf("input.%d.encrypted_content", i)
		if enc := gjson.GetBytes(updated, encPath); enc.Exists() && enc.Type == gjson.Null {
			if next, err := sjson.DeleteBytes(updated, encPath); err == nil {
				updated = next
			}
		}
	}
	return mergeAdjacentReasoningSummaries(updated)
}

func sanitizeInputEncryptedContent(body []byte) []byte {
	input := gjson.GetBytes(body, "input")
	if !input.Exists() || !input.IsArray() {
		return body
	}
	items := make([]json.RawMessage, 0, len(input.Array()))
	changed := false
	for _, item := range input.Array() {
		itemType := strings.TrimSpace(item.Get("type").String())
		if itemType != "reasoning" && itemType != "compaction" {
			items = append(items, json.RawMessage(item.Raw))
			continue
		}
		enc := item.Get("encrypted_content")
		if !enc.Exists() {
			items = append(items, json.RawMessage(item.Raw))
			continue
		}
		invalid := false
		switch enc.Type {
		case gjson.String:
			if InspectGrokEncryptedContent(enc.String()) != nil {
				invalid = true
			}
		case gjson.Null:
			invalid = true
		default:
			invalid = true
		}
		if !invalid {
			items = append(items, json.RawMessage(item.Raw))
			continue
		}
		if itemType == "compaction" {
			changed = true
			continue
		}
		next, err := sjson.DeleteBytes([]byte(item.Raw), "encrypted_content")
		if err != nil {
			items = append(items, json.RawMessage(item.Raw))
			continue
		}
		items = append(items, json.RawMessage(next))
		changed = true
	}
	if !changed {
		return body
	}
	rawInput, err := json.Marshal(items)
	if err != nil {
		return body
	}
	updated, err := sjson.SetRawBytes(body, "input", rawInput)
	if err != nil {
		return body
	}
	return mergeAdjacentReasoningSummaries(updated)
}

func mergeAdjacentReasoningSummaries(body []byte) []byte {
	input := gjson.GetBytes(body, "input")
	if !input.Exists() || !input.IsArray() {
		return body
	}
	changed := false
	items := make([]json.RawMessage, 0, len(input.Array()))
	for _, item := range input.Array() {
		if len(items) > 0 && canMergeReasoningSummary(items[len(items)-1], item) {
			merged, ok := appendReasoningSummary(items[len(items)-1], item.Get("summary").Array())
			if ok {
				items[len(items)-1] = json.RawMessage(merged)
				changed = true
				continue
			}
		}
		items = append(items, json.RawMessage(item.Raw))
	}
	if !changed {
		return body
	}
	rawInput, err := json.Marshal(items)
	if err != nil {
		return body
	}
	updated, err := sjson.SetRawBytes(body, "input", rawInput)
	if err != nil {
		return body
	}
	return updated
}

func canMergeReasoningSummary(previous json.RawMessage, current gjson.Result) bool {
	prev := gjson.ParseBytes(previous)
	if prev.Get("type").String() != "reasoning" || current.Get("type").String() != "reasoning" {
		return false
	}
	if !prev.Get("summary").IsArray() || !current.Get("summary").IsArray() {
		return false
	}
	if len(current.Get("summary").Array()) == 0 {
		return false
	}
	for name := range current.Map() {
		if name != "type" && name != "summary" {
			return false
		}
	}
	return true
}

func appendReasoningSummary(previous json.RawMessage, currentSummary []gjson.Result) ([]byte, bool) {
	updated := []byte(previous)
	summary := gjson.GetBytes(updated, "summary")
	if !summary.IsArray() {
		return previous, false
	}
	nextIndex := len(summary.Array())
	for i, item := range currentSummary {
		next, err := sjson.SetRawBytes(updated, fmt.Sprintf("summary.%d", nextIndex+i), []byte(item.Raw))
		if err != nil {
			return previous, false
		}
		updated = next
	}
	return updated, true
}

func removeInputItemsByType(body []byte, itemType string) []byte {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body
	}
	items := make([]json.RawMessage, 0, len(input.Array()))
	changed := false
	for _, item := range input.Array() {
		if item.Get("type").String() == itemType {
			changed = true
			continue
		}
		items = append(items, json.RawMessage(item.Raw))
	}
	if !changed {
		return body
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return body
	}
	updated, err := sjson.SetRawBytes(body, "input", raw)
	if err != nil {
		return body
	}
	return updated
}

// InputHasItemType reports whether input[] contains an item of the given type.
func InputHasItemType(body []byte, itemType string) bool {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return false
	}
	for _, item := range input.Array() {
		if item.Get("type").String() == itemType {
			return true
		}
	}
	return false
}

// BuildCompactionTriggerStreamChunks 将 compact JSON 响应合成 HTTP SSE 帧序列。
func BuildCompactionTriggerStreamChunks(preparedBody []byte, baseModel string, compactData []byte) [][]byte {
	responseID := CompactionResponseID(compactData)
	now := time.Now().Unix()
	createdAt := gjson.GetBytes(compactData, "created_at").Int()
	if createdAt == 0 {
		createdAt = now
	}
	completedAt := gjson.GetBytes(compactData, "completed_at").Int()
	if completedAt == 0 {
		completedAt = now
	}

	item := CompactionOutputItem(compactData, responseID)
	output := make([]byte, 0, len(item)+2)
	output = append(output, '[')
	output = append(output, item...)
	output = append(output, ']')

	createdResponse := buildCompactionBaseResponse(preparedBody, baseModel, compactData, responseID, createdAt, "in_progress")
	inProgressResponse := buildCompactionBaseResponse(preparedBody, baseModel, compactData, responseID, createdAt, "in_progress")
	completedResponse := buildCompactionBaseResponse(preparedBody, baseModel, compactData, responseID, createdAt, "completed")
	completedResponse, _ = sjson.SetBytes(completedResponse, "completed_at", completedAt)
	completedResponse, _ = sjson.SetRawBytes(completedResponse, "output", output)
	if usage := gjson.GetBytes(compactData, "usage"); usage.Exists() {
		completedResponse, _ = sjson.SetRawBytes(completedResponse, "usage", []byte(usage.Raw))
	}

	createdPayload := []byte(`{"type":"response.created","sequence_number":0}`)
	createdPayload, _ = sjson.SetRawBytes(createdPayload, "response", createdResponse)
	inProgressPayload := []byte(`{"type":"response.in_progress","sequence_number":1}`)
	inProgressPayload, _ = sjson.SetRawBytes(inProgressPayload, "response", inProgressResponse)
	addedPayload := []byte(`{"type":"response.output_item.added","sequence_number":2,"output_index":0}`)
	addedPayload, _ = sjson.SetRawBytes(addedPayload, "item", item)
	keepalivePayload := []byte(`{"type":"keepalive","sequence_number":3}`)
	donePayload := []byte(`{"type":"response.output_item.done","sequence_number":4,"output_index":0}`)
	donePayload, _ = sjson.SetRawBytes(donePayload, "item", item)
	completedPayload := []byte(`{"type":"response.completed","sequence_number":5}`)
	completedPayload, _ = sjson.SetRawBytes(completedPayload, "response", completedResponse)

	return [][]byte{
		BuildSSEFrame("response.created", createdPayload),
		BuildSSEFrame("response.in_progress", inProgressPayload),
		BuildSSEFrame("response.output_item.added", addedPayload),
		BuildSSEFrame("keepalive", keepalivePayload),
		BuildSSEFrame("response.output_item.done", donePayload),
		BuildSSEFrame("response.completed", completedPayload),
	}
}

// BuildCompactionTriggerWSEvents 合成 WS 事件（JSON，非 SSE 帧）。
func BuildCompactionTriggerWSEvents(preparedBody []byte, baseModel string, compactData []byte) [][]byte {
	frames := BuildCompactionTriggerStreamChunks(preparedBody, baseModel, compactData)
	out := make([][]byte, 0, len(frames))
	for _, frame := range frames {
		// 提取 data: 行
		for _, line := range strings.Split(string(frame), "\n") {
			if strings.HasPrefix(line, "data: ") {
				out = append(out, []byte(strings.TrimSpace(line[6:])))
				break
			}
		}
	}
	return out
}

func buildCompactionBaseResponse(preparedBody []byte, baseModel string, compactData []byte, responseID string, createdAt int64, status string) []byte {
	response := []byte(`{"id":"","object":"response","created_at":0,"status":"","background":false,"error":null,"incomplete_details":null,"output":[]}`)
	response, _ = sjson.SetBytes(response, "id", responseID)
	response, _ = sjson.SetBytes(response, "created_at", createdAt)
	response, _ = sjson.SetBytes(response, "status", status)
	if model := gjson.GetBytes(compactData, "model").String(); model != "" {
		response, _ = sjson.SetBytes(response, "model", model)
	} else if baseModel != "" {
		response, _ = sjson.SetBytes(response, "model", baseModel)
	}
	for _, field := range []string{
		"instructions", "max_output_tokens", "max_tool_calls", "parallel_tool_calls",
		"previous_response_id", "prompt_cache_key", "reasoning", "text", "tool_choice",
		"tools", "top_logprobs", "top_p", "truncation", "user", "metadata",
	} {
		if value := gjson.GetBytes(preparedBody, field); value.Exists() {
			response, _ = sjson.SetRawBytes(response, field, []byte(value.Raw))
		}
	}
	return response
}

// BuildSSEFrame 构造 SSE event 帧。
func BuildSSEFrame(eventName string, data []byte) []byte {
	out := make([]byte, 0, len(eventName)+len(data)+16)
	out = append(out, "event: "...)
	out = append(out, eventName...)
	out = append(out, '\n')
	out = append(out, "data: "...)
	out = append(out, data...)
	out = append(out, '\n', '\n')
	return out
}

// PrepareCompactBody 为 /responses/compact 准备 body。
func PrepareCompactBody(body []byte, model string) ([]byte, error) {
	prepared, err := PrepareResponsesBody(body, PrepareOptions{
		Stream:    false,
		Model:     model,
		IsCompact: true,
	})
	if err != nil {
		return nil, err
	}
	if prepared == nil {
		return body, nil
	}
	return prepared.Body, nil
}

// SyntheticCompactionStream 将 compact 结果包装为可被 writeUpstreamResult 透传的伪流。
func SyntheticCompactionStream(preparedBody []byte, baseModel string, compactData []byte) []byte {
	chunks := BuildCompactionTriggerStreamChunks(preparedBody, baseModel, compactData)
	total := 0
	for _, c := range chunks {
		total += len(c)
	}
	out := make([]byte, 0, total)
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out
}

// FormatCompactionError 统一错误包装。
func FormatCompactionError(status int, body []byte) error {
	return fmt.Errorf("compact upstream %d: %s", status, strings.TrimSpace(string(body)))
}

// AggregateResponsesSSE 将上游 Responses SSE 收成非流 JSON。
// 收集 output_item.done 并 patch completed.output 后再取 response 对象。
func AggregateResponsesSSE(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	// 已是 JSON 对象则直接返回
	if gjson.ValidBytes(data) && data[0] == '{' {
		return NormalizeNonStreamReasoning(data)
	}
	if completed, ok := extractCompletedFromSSE(data); ok {
		return CompletedEventToNonStreamBody(completed)
	}
	// 无 completed：退回最后一条 data 事件便于排障
	var fallback []byte
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, dataTag) {
			continue
		}
		payload := bytes.TrimSpace(line[len(dataTag):])
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		fallback = NormalizeReasoningSummaryData(payload)
	}
	if len(fallback) > 0 {
		return fallback
	}
	return data
}

// LooksLikeSSE 粗判是否为 SSE 文本。
func LooksLikeSSE(data []byte) bool {
	s := string(data)
	return strings.Contains(s, "data:") && (strings.Contains(s, "response.") || strings.Contains(s, "event:"))
}

// executeResponsesOnce 统一 prepare + chat headers（强制上游 stream）。
func executeResponsesOnce(ctx context.Context, req Request) (*Result, error) {
	return executeResponsesCompat(ctx, req)
}

// executeResponsesCompat 强制 stream + prepare + chat headers。
func executeResponsesCompat(ctx context.Context, req Request) (*Result, error) {
	token := resolveAccessToken(req)
	baseURL := ResolveChatBaseURL(req.Config, req.Account)
	targetURL := appendRawQuery(joinBasePath(baseURL, "/responses"), req.RawQuery)

	upstreamStream := true

	replayKey := resolveRequestReplayKey(req)
	prepared, err := PrepareResponsesBody(req.Body, PrepareOptions{
		Stream:           upstreamStream,
		Model:            req.Model,
		SourceType:       req.SourceType,
		Headers:          req.Headers,
		IsCompact:        false,
		EnableReplay:     req.EnableReplay && replayKey != "",
		ReplaySessionKey: replayKey,
	})
	if err != nil {
		return emptyResultErr(err)
	}
	body := req.Body
	var sessionID string
	var replayScope ReplayScope
	if prepared != nil {
		body = prepared.Body
		sessionID = prepared.SessionID
		replayScope = prepared.ReplayScope
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return emptyResultErr(err)
	}
	copyAllowedInboundHeaders(httpReq.Header, req.Headers)
	// resolved session 同步写入 x-grok-conv-id（避免与 body 分叉）
	if sessionID != "" {
		httpReq.Header.Set(HeaderGrokConvID, sessionID)
	}
	applyXAIChatHeaders(httpReq, token, true, sessionID, req.Config, req.Account)

	targetHeaders := SanitizeHeaders(httpReq.Header)
	resp, err := doHTTP(ctx, req, httpReq)
	if err != nil {
		return transportResult(targetURL, targetHeaders, body, replayScope, err)
	}
	if req.IsStreaming {
		return resultResponsesStream(resp, targetURL, targetHeaders, body, replayScope)
	}
	return resultResponsesNonStream(resp, targetURL, targetHeaders, body, replayScope)
}

func resultResponsesStream(resp *http.Response, targetURL string, targetHeaders map[string]string, requestBody []byte, replayScope ReplayScope) (*Result, error) {
	result := &Result{
		StatusCode:    resp.StatusCode,
		Headers:       resp.Header.Clone(),
		TargetURL:     targetURL,
		TargetHeaders: targetHeaders,
		RequestBody:   requestBody,
		ReplayScope:   replayScope,
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, err := readLimitedBody(resp)
		if err != nil {
			result.Error = err
			return result, err
		}
		return statusErrorResult(resp, targetURL, targetHeaders, requestBody, replayScope, data)
	}
	// reasoning 归一 + completed patch + event: 与 data.type 同步
	result.Stream = WrapCompatResponsesSSEStream(resp.Body, replayScope)
	return result, nil
}

func resultResponsesNonStream(resp *http.Response, targetURL string, targetHeaders map[string]string, requestBody []byte, replayScope ReplayScope) (*Result, error) {
	data, err := readLimitedBody(resp)
	if err != nil {
		return transportResult(targetURL, targetHeaders, requestBody, replayScope, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusErrorResult(resp, targetURL, targetHeaders, requestBody, replayScope, data)
	}

	if LooksLikeSSE(data) {
		completed, ok := extractCompletedFromSSE(data)
		if !ok {
			err = fmt.Errorf("xai stream error: stream disconnected before response.completed")
			return &Result{
				StatusCode:    http.StatusRequestTimeout,
				Headers:       resp.Header.Clone(),
				Body:          data,
				TargetURL:     targetURL,
				TargetHeaders: targetHeaders,
				RequestBody:   requestBody,
				ReplayScope:   replayScope,
				Error:         err,
			}, err
		}
		if replayScope.Valid() {
			CacheReasoningReplayFromCompleted(replayScope, completed)
		}
		out := CompletedEventToNonStreamBody(completed)
		headers := resp.Header.Clone()
		headers.Set("Content-Type", "application/json")
		headers.Del("Content-Length")
		headers.Del("Content-Encoding")
		return &Result{
			StatusCode:    resp.StatusCode,
			Headers:       headers,
			Body:          out,
			TargetURL:     targetURL,
			TargetHeaders: targetHeaders,
			RequestBody:   requestBody,
			ReplayScope:   replayScope,
		}, nil
	}

	data = NormalizeNonStreamReasoning(data)
	if replayScope.Valid() {
		CacheReasoningReplayFromCompleted(replayScope, data)
		if typ := gjson.GetBytes(data, "type").String(); typ == "response.completed" {
			CacheReasoningReplayFromCompleted(replayScope, data)
		}
	}
	headers := resp.Header.Clone()
	headers.Set("Content-Type", "application/json")
	headers.Del("Content-Length")
	headers.Del("Content-Encoding")
	return &Result{
		StatusCode:    resp.StatusCode,
		Headers:       headers,
		Body:          data,
		TargetURL:     targetURL,
		TargetHeaders: targetHeaders,
		RequestBody:   requestBody,
		ReplayScope:   replayScope,
	}, nil
}

// resolveRequestReplayKey 从 header 或 body 解析并做 caller 隔离。
// 有 execution_session_id 时使用 execution: 前缀（跨请求可信、无需 caller key）。
func resolveRequestReplayKey(req Request) string {
	if req.Headers != nil {
		if k := strings.TrimSpace(req.Headers.Get("x-xai-replay-session")); k != "" {
			// 可能已是 caller:/execution: 前缀；Isolate 幂等
			return IsolateReplaySessionKey(k, CallerAPIKeyFromHeaders(req.Headers))
		}
	}
	if execID := ExecutionSessionIDFromHeaders(req.Headers); execID != "" {
		return IsolateReplaySessionKey("execution:"+execID, CallerAPIKeyFromHeaders(req.Headers))
	}
	preSession := ResolveUpstreamSessionID(req.Body, req.Headers, "", req.Model)
	return ResolveReplaySessionKey(req.Body, req.Headers, preSession)
}

// extractCompletedFromSSE 非流路径：收集 output_item.done 并 patch completed。
func extractCompletedFromSSE(data []byte) ([]byte, bool) {
	byIndex := make(map[int64][]byte)
	var fallback [][]byte
	var completed []byte
	for _, line := range bytes.Split(data, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		eventData := NormalizeReasoningSummaryData(bytes.TrimSpace(line[len("data:"):]))
		switch gjson.GetBytes(eventData, "type").String() {
		case "response.output_item.done":
			collectOutputItemDone(eventData, byIndex, &fallback)
		case "response.completed":
			completed = patchCompletedOutput(eventData, byIndex, fallback)
			completed = NormalizeReasoningSummaryData(completed)
		}
	}
	if len(completed) == 0 {
		return nil, false
	}
	return completed, true
}

// executeCompactOnce compact：prepare + chat headers（非流）。
func executeCompactOnce(ctx context.Context, req Request) (*Result, error) {
	token := resolveAccessToken(req)
	baseURL := ResolveChatBaseURL(req.Config, req.Account)
	targetURL := appendRawQuery(joinBasePath(baseURL, "/responses/compact"), req.RawQuery)

	replayKey := resolveRequestReplayKey(req)
	prepared, err := PrepareResponsesBody(req.Body, PrepareOptions{
		Stream:           false,
		Model:            req.Model,
		SourceType:       req.SourceType,
		Headers:          req.Headers,
		IsCompact:        true,
		EnableReplay:     req.EnableReplay && replayKey != "",
		ReplaySessionKey: replayKey,
	})
	if err != nil {
		return emptyResultErr(err)
	}
	body := req.Body
	var sessionID string
	var replayScope ReplayScope
	if prepared != nil {
		body = prepared.Body
		sessionID = prepared.SessionID
		replayScope = prepared.ReplayScope
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return emptyResultErr(err)
	}
	copyAllowedInboundHeaders(httpReq.Header, req.Headers)
	if sessionID != "" {
		httpReq.Header.Set(HeaderGrokConvID, sessionID)
	}
	applyXAIChatHeaders(httpReq, token, false, sessionID, req.Config, req.Account)

	targetHeaders := SanitizeHeaders(httpReq.Header)
	resp, err := doHTTP(ctx, req, httpReq)
	if err != nil {
		return transportResult(targetURL, targetHeaders, body, replayScope, err)
	}

	data, err := readLimitedBody(resp)
	if err != nil {
		return transportResult(targetURL, targetHeaders, body, replayScope, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusErrorResult(resp, targetURL, targetHeaders, body, replayScope, data)
	}

	ClearReasoningReplayAfterCompaction(replayScope)

	return &Result{
		StatusCode:    resp.StatusCode,
		Headers:       resp.Header.Clone(),
		Body:          data,
		TargetURL:     targetURL,
		TargetHeaders: targetHeaders,
		RequestBody:   body,
		ReplayScope:   replayScope,
	}, nil
}

// executeImagesOnce：
// - media base URL
// - body 原样
// - applyXAIMediaHeaders（无 CLI 身份）
// - response 原样
func executeImagesOnce(ctx context.Context, req Request) (*Result, error) {
	token := resolveAccessToken(req)
	baseURL := ResolveMediaBaseURL(req.Config)
	upstreamPath := MapInboundPath(req.Path)
	if upstreamPath == "/" || !strings.HasPrefix(upstreamPath, "/images/") {
		upstreamPath = "/images/generations"
	}
	targetURL := appendRawQuery(joinBasePath(baseURL, upstreamPath), req.RawQuery)

	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodPost
	}
	var bodyReader *bytes.Reader
	body := append([]byte(nil), req.Body...)
	if method == http.MethodGet || method == http.MethodHead {
		body = nil
		bodyReader = bytes.NewReader(nil)
	} else {
		bodyReader = bytes.NewReader(body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return emptyResultErr(err)
	}
	copyAllowedInboundHeaders(httpReq.Header, req.Headers)
	applyXAIMediaHeaders(httpReq, token, "", req.Config, req.Account)

	return executeMediaHTTP(ctx, req, httpReq, targetURL, body)
}

// executeVideosOnce：
// - media base
// - POST generations/edits/extensions；GET /videos/{id}
// - POST 可附 x-idempotency-key
func executeVideosOnce(ctx context.Context, req Request) (*Result, error) {
	token := resolveAccessToken(req)
	baseURL := ResolveMediaBaseURL(req.Config)
	upstreamPath := MapInboundPath(req.Path)
	if upstreamPath == "/" || (upstreamPath == "/videos" && !strings.EqualFold(req.Method, http.MethodGet)) {
		upstreamPath = "/videos/generations"
	}
	targetURL := appendRawQuery(joinBasePath(baseURL, upstreamPath), req.RawQuery)

	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodPost
	}
	var bodyReader *bytes.Reader
	body := append([]byte(nil), req.Body...)
	if method == http.MethodGet || method == http.MethodHead {
		body = nil
		bodyReader = bytes.NewReader(nil)
	} else {
		bodyReader = bytes.NewReader(body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return emptyResultErr(err)
	}
	copyAllowedInboundHeaders(httpReq.Header, req.Headers)
	applyXAIMediaHeaders(httpReq, token, "", req.Config, req.Account)

	// POST：透传/注入 idempotency key
	if method == http.MethodPost {
		// 标准名称优先，同时兼容 x-idempotency-key；上游统一使用 x-*。
		key := headerGet(req.Headers, "Idempotency-Key")
		if key == "" {
			key = headerGet(req.Headers, HeaderIdempotencyKey)
		}
		if key != "" {
			httpReq.Header.Set(HeaderIdempotencyKey, key)
		}
	}

	return executeMediaHTTP(ctx, req, httpReq, targetURL, body)
}

func executeMediaHTTP(ctx context.Context, req Request, httpReq *http.Request, targetURL string, requestBody []byte) (*Result, error) {
	targetHeaders := SanitizeHeaders(httpReq.Header)
	resp, err := doHTTP(ctx, req, httpReq)
	if err != nil {
		return transportResult(targetURL, targetHeaders, requestBody, ReplayScope{}, err)
	}

	// 媒体通常非流；若客户端要流且 2xx，原样透传 body stream
	if req.IsStreaming && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return &Result{
			StatusCode:    resp.StatusCode,
			Headers:       resp.Header.Clone(),
			Stream:        resp.Body,
			TargetURL:     targetURL,
			TargetHeaders: targetHeaders,
			RequestBody:   requestBody,
		}, nil
	}

	data, err := readLimitedBody(resp)
	if err != nil {
		return transportResult(targetURL, targetHeaders, requestBody, ReplayScope{}, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusErrorResult(resp, targetURL, targetHeaders, requestBody, ReplayScope{}, data)
	}
	return &Result{
		StatusCode:    resp.StatusCode,
		Headers:       resp.Header.Clone(),
		Body:          data,
		TargetURL:     targetURL,
		TargetHeaders: targetHeaders,
		RequestBody:   requestBody,
	}, nil
}

// Execute 按请求种类分流执行。
func Execute(ctx context.Context, req Request) (*Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	attempts := req.Attempts
	if attempts <= 0 {
		attempts = 1
	}
	req.Attempts = attempts

	switch ClassifyPath(req.Path) {
	case KindCompact:
		return executeWithRetry(ctx, req, executeCompactOnce)
	case KindImages:
		return executeWithRetry(ctx, req, executeImagesOnce)
	case KindVideos:
		return executeWithRetry(ctx, req, executeVideosOnce)
	default:
		// KindResponses / Unknown：compaction_trigger 流式特殊路径
		if req.IsStreaming && !IsCompactPath(req.Path) && IsResponsesPath(req.Path) &&
			InputHasCompactionTrigger(req.Body) {
			return executeCompactionTriggerStream(ctx, req)
		}
		return executeWithRetry(ctx, req, executeResponsesOnce)
	}
}

type executeOnceFn func(ctx context.Context, req Request) (*Result, error)

func executeWithRetry(ctx context.Context, req Request, once executeOnceFn) (*Result, error) {
	var lastErr error
	var lastResult *Result
	for attempt := 1; attempt <= req.Attempts; attempt++ {
		result, err := once(ctx, req)
		if err == nil {
			return result, nil
		}
		lastErr = err
		lastResult = result
		// 已有 HTTP 状态码：不重试（账号级 failover 由 plugin 处理）
		if result != nil && result.StatusCode > 0 {
			return result, err
		}
		if attempt < req.Attempts {
			if waitErr := waitForRetry(ctx, req.RetryDelay); waitErr != nil {
				if result == nil {
					result = &Result{Error: waitErr}
				} else {
					result.Error = waitErr
				}
				return result, waitErr
			}
			continue
		}
	}
	if lastResult != nil {
		return lastResult, lastErr
	}
	err := fmt.Errorf("request failed: %w", lastErr)
	return &Result{Error: err}, err
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// executeCompactionTriggerStream 触发 compact 后的流式后续。
func executeCompactionTriggerStream(ctx context.Context, req Request) (*Result, error) {
	compactReq := req
	compactReq.IsStreaming = false
	compactReq.Path = "/xai/v1/responses/compact"
	result, execErr := Execute(ctx, compactReq)
	if result == nil {
		result = &Result{Error: execErr}
	}
	if execErr != nil && result.StatusCode == 0 {
		return result, execErr
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return result, result.Error
	}

	sseBody := SyntheticCompactionStream(result.RequestBody, BaseModelName(req.Model), result.Body)
	headers := http.Header{}
	headers.Set("Content-Type", "text/event-stream")
	return &Result{
		StatusCode:    http.StatusOK,
		Headers:       headers,
		Stream:        io.NopCloser(bytes.NewReader(sseBody)),
		TargetURL:     result.TargetURL,
		TargetHeaders: result.TargetHeaders,
		RequestBody:   result.RequestBody,
		ReplayScope:   result.ReplayScope,
	}, nil
}

func emptyResultErr(err error) (*Result, error) {
	return &Result{Error: err}, err
}

func transportResult(targetURL string, headers map[string]string, body []byte, scope ReplayScope, err error) (*Result, error) {
	return &Result{
		TargetURL:     targetURL,
		TargetHeaders: headers,
		RequestBody:   body,
		ReplayScope:   scope,
		Error:         err,
	}, err
}

func doHTTP(ctx context.Context, req Request, httpReq *http.Request) (*http.Response, error) {
	client := req.Client
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(httpReq.WithContext(ctx))
}

func readLimitedBody(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("nil response body")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 52_428_800))
	// Close 错误只记录，不得覆盖已成功读取的 body
	if errClose := resp.Body.Close(); errClose != nil {
		log.Printf("xai executor: close response body error: %v", errClose)
	}
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return data, nil
}

func statusErrorResult(resp *http.Response, targetURL string, targetHeaders map[string]string, requestBody []byte, scope ReplayScope, data []byte) (*Result, error) {
	err := fmt.Errorf("upstream returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	return &Result{
		StatusCode:    resp.StatusCode,
		Headers:       resp.Header.Clone(),
		Body:          data,
		TargetURL:     targetURL,
		TargetHeaders: targetHeaders,
		RequestBody:   requestBody,
		ReplayScope:   scope,
		Error:         err,
	}, err
}
