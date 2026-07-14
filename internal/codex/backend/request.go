package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	codexShared "clisimplehub/internal/codex/shared"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const defaultImageToolModel = "gpt-image-2"

// Responses Lite 检测
// HTTP 走 header；WebSocket 常把仅握手可见的 header 镜像进 client_metadata。
const (
	responsesLiteHeader   = "X-OpenAI-Internal-Codex-Responses-Lite"
	responsesLiteMetadata = "client_metadata.ws_request_header_x_openai_internal_codex_responses_lite"
)

var imageGenToolJSON = []byte(`{"type":"image_generation","output_format":"png"}`)
var imageGenToolArrayJSON = []byte(`[{"type":"image_generation","output_format":"png"}]`)

// disable-image-generation 四态配置。
const (
	ImageGenOff         = "off"         // 注入 image_generation 工具（启用）
	ImageGenAll         = "all"         // 全部剥离（含 /v1/images/* 端点）
	ImageGenChat        = "chat"        // 非 images 端点剥离
	ImageGenPassthrough = "passthrough" // 透传，不注入不剥离（默认）
)

// resolveImageGenMode 归一化配置值，空值/未知值兜底为 passthrough。
func resolveImageGenMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ImageGenOff:
		return ImageGenOff
	case ImageGenAll:
		return ImageGenAll
	case ImageGenChat:
		return ImageGenChat
	case ImageGenPassthrough:
		return ImageGenPassthrough
	default:
		return ImageGenPassthrough
	}
}

var promptCacheStore = struct {
	sync.Mutex
	items map[string]promptCacheEntry
}{items: make(map[string]promptCacheEntry)}

type promptCacheEntry struct {
	ID     string
	Expire time.Time
}

func Prepare(ctx context.Context, req Request) (*http.Request, []byte, *imagePreparedRequest, IdentityState, ReplayScope, error) {
	body := append([]byte(nil), req.Body...)
	path := TargetPath(req.Path)
	var imageMeta *imagePreparedRequest
	var replayScope ReplayScope
	if IsImagesPath(req.Path) {
		path = "/responses"
	}

	if IsCompactPath(req.Path) {
		req.IsStreaming = false
		body = finalizeCompactBody(body, req.Model, resolveImageGenMode(req.DisableImageGeneration), req.Headers)
	} else if IsImagesPath(req.Path) {
		if resolveImageGenMode(req.DisableImageGeneration) == ImageGenAll {
			return nil, nil, nil, IdentityState{}, ReplayScope{}, StatusError{
				Code: http.StatusNotFound,
				Body: []byte(`{"error":{"message":"image generation is disabled (disable-image-generation=all)","type":"invalid_request_error"}}`),
			}
		}
		var err error
		if gjson.GetBytes(body, "tool_choice.type").String() == "image_generation" {
			body, err = PrepareOpenAIImageBody(body)
			imageMeta = defaultImagePreparedRequest(req)
		} else {
			var responseFormat string
			var streamPrefix string
			body, responseFormat, streamPrefix, err = PrepareOpenAIImageRequest(req.Path, body, req.Model, req.Headers)
			imageMeta = &imagePreparedRequest{
				Body:           append([]byte(nil), body...),
				ResponseFormat: responseFormat,
				StreamPrefix:   streamPrefix,
			}
		}
		if err != nil {
			return nil, nil, nil, IdentityState{}, ReplayScope{}, err
		}
	} else {
		body = finalizeResponsesBody(body, req.Model, req.PlanType, resolveImageGenMode(req.DisableImageGeneration), req.Headers)
	}

	// Claude→Codex：在 finalize 后注入 reasoning replay
	if IsClaudeSource(req.Source) && !IsImagesPath(req.Path) && !IsCompactPath(req.Path) {
		sessionKey := ResolveReplaySessionKeyWithClaude(body, req.OriginalBody, req.Headers, req.ReplaySessionKey)
		body, replayScope = ApplyReasoningReplay(body, req.Model, sessionKey, true)
	}

	targetURL := UpstreamURL(req.Config, path)
	body, headers := applyPromptCache(req.Source, body, req.OriginalBody, req.Headers, req.Model, req.LocalAccountID)
	body, headers, identityState := applyIdentityConfuse(req.LocalAccountID, body, headers)
	httpReq, err := http.NewRequestWithContext(ctx, methodOrPost(req.Method), targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, nil, IdentityState{}, ReplayScope{}, err
	}
	// 图片直连不转发客户端 UA，降低 Cloudflare 1010。
	clientHeaders := headers
	if IsImagesPath(req.Path) && clientHeaders != nil {
		clientHeaders = clientHeaders.Clone()
		clientHeaders.Del("User-Agent")
	}
	ApplyHeaders(httpReq, req.AccessToken, req.AccountID, req.IsStreaming || IsImagesPath(req.Path), req.Config, clientHeaders)
	return httpReq, body, imageMeta, identityState, replayScope, nil
}

func PrepareWebsocket(ctx context.Context, req Request) ([]byte, http.Header, IdentityState, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	body := append([]byte(nil), req.Body...)
	body = finalizeResponsesBody(body, req.Model, req.PlanType, resolveImageGenMode(req.DisableImageGeneration), req.Headers)
	body, headers := applyPromptCache(req.Source, body, req.OriginalBody, req.Headers, req.Model, req.LocalAccountID)
	body, headers, identityState := applyIdentityConfuse(req.LocalAccountID, body, headers)
	return BuildWebsocketRequestBody(body), ApplyWebsocketHeaders(req.AccessToken, req.AccountID, req.Config, headers), identityState, nil
}

func BuildWebsocketRequestBody(body []byte) []byte {
	if len(body) == 0 {
		return nil
	}
	out, err := sjson.SetBytes(append([]byte(nil), body...), "type", "response.create")
	if err == nil && len(out) > 0 {
		return out
	}
	fallback := append([]byte(nil), body...)
	fallback, _ = sjson.SetBytes(fallback, "type", "response.create")
	return fallback
}

func TargetPath(path string) string {
	if IsCompactPath(path) {
		return "/responses/compact"
	}
	return "/responses"
}

func UpstreamURL(config *codexShared.CodexMultiConfig, targetPath string) string {
	baseURL := codexShared.DefaultCodexBaseURL
	if config != nil {
		if cfgBaseURL := strings.TrimSpace(config.GetBaseURL()); cfgBaseURL != "" {
			baseURL = cfgBaseURL
		}
	}
	baseURL = strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	baseURL = strings.TrimSuffix(baseURL, "/responses/compact")
	baseURL = strings.TrimSuffix(baseURL, "/responses")
	if IsCompactPath(targetPath) {
		return baseURL + "/responses/compact"
	}
	return baseURL + "/responses"
}

func IsCompactPath(path string) bool {
	p := strings.ToLower(strings.TrimRight(strings.TrimSpace(path), "/"))
	return strings.HasSuffix(p, "/responses/compact")
}

func IsImagesPath(path string) bool {
	p := strings.ToLower(strings.TrimRight(strings.TrimSpace(path), "/"))
	return p == ImagesGenerationsPath || p == ImagesEditsPath ||
		strings.HasSuffix(p, ImagesGenerationsPath) || strings.HasSuffix(p, ImagesEditsPath)
}

func IsImagesGenerationsPath(path string) bool {
	p := strings.ToLower(strings.TrimRight(strings.TrimSpace(path), "/"))
	return p == ImagesGenerationsPath || strings.HasSuffix(p, ImagesGenerationsPath)
}

func IsImagesEditsPath(path string) bool {
	p := strings.ToLower(strings.TrimRight(strings.TrimSpace(path), "/"))
	return p == ImagesEditsPath || strings.HasSuffix(p, ImagesEditsPath)
}

func methodOrPost(method string) string {
	method = strings.TrimSpace(method)
	if method == "" {
		return http.MethodPost
	}
	return method
}

func finalizeResponsesBody(body []byte, model string, planType string, imageGenMode string, headers http.Header) []byte {
	baseModel := BaseModelName(model)
	if baseModel == "" {
		baseModel = BaseModelName(gjson.GetBytes(body, "model").String())
	}
	if baseModel != "" {
		body, _ = sjson.SetBytes(body, "model", baseModel)
	}
	body, _ = sjson.SetBytes(body, "stream", true)
	body = deleteUnsupportedFields(body)
	body = normalizeInstructions(body)
	body = sanitizeReasoningEncryptedContent(body)
	lite := isResponsesLiteRequest(body, headers)
	body = normalizeParallelToolCallsForTools(body, lite)
	return applyImageGenerationPolicy(body, baseModel, planType, imageGenMode, lite)
}

func finalizeCompactBody(body []byte, model string, imageGenMode string, headers http.Header) []byte {
	baseModel := BaseModelName(model)
	if baseModel == "" {
		baseModel = BaseModelName(gjson.GetBytes(body, "model").String())
	}
	if baseModel != "" {
		body, _ = sjson.SetBytes(body, "model", baseModel)
	}
	body, _ = sjson.DeleteBytes(body, "stream")
	body = deleteUnsupportedFields(body)
	body = normalizeInstructions(body)
	body = sanitizeReasoningEncryptedContent(body)
	lite := isResponsesLiteRequest(body, headers)
	body = normalizeParallelToolCallsForTools(body, lite)
	return applyImageGenerationPolicy(body, baseModel, "", imageGenMode, lite)
}

// applyImageGenerationPolicy 根据四态配置处理 image_generation 工具：
// off 注入、all/chat 剥离、passthrough 透传。
// Responses Lite 不注入 image_generation（上游仅允许 function/custom/client tool search）。
func applyImageGenerationPolicy(body []byte, baseModel string, planType string, imageGenMode string, lite bool) []byte {
	switch imageGenMode {
	case ImageGenOff:
		return ensureImageGenerationTool(body, baseModel, planType, lite)
	case ImageGenAll, ImageGenChat:
		return stripImageGenerationTools(body)
	default:
		return body
	}
}

// isResponsesLiteRequest 检测 Codex Responses Lite 请求。
// 来源：HTTP header，或 WS body 内 client_metadata 镜像字段。
func isResponsesLiteRequest(body []byte, headers http.Header) bool {
	if headers != nil {
		if strings.EqualFold(strings.TrimSpace(headerValueCaseInsensitive(headers, responsesLiteHeader)), "true") {
			return true
		}
	}
	value := gjson.GetBytes(body, responsesLiteMetadata)
	if !value.Exists() {
		return false
	}
	if value.Type == gjson.True {
		return true
	}
	return value.Type == gjson.String && strings.EqualFold(strings.TrimSpace(value.String()), "true")
}

func deleteUnsupportedFields(body []byte) []byte {
	for _, field := range []string{
		"previous_response_id",
		"prompt_cache_retention",
		"safety_identifier",
		"stream_options",
	} {
		body, _ = sjson.DeleteBytes(body, field)
	}
	return body
}

func normalizeInstructions(body []byte) []byte {
	instructions := gjson.GetBytes(body, "instructions")
	if !instructions.Exists() || instructions.Type == gjson.Null {
		body, _ = sjson.SetBytes(body, "instructions", "")
	}
	return body
}

// sanitizeReasoningEncryptedContent 删除 input 中非法 reasoning.encrypted_content，
// 避免上游因脏签名直接 400
func sanitizeReasoningEncryptedContent(body []byte) []byte {
	input := gjson.GetBytes(body, "input")
	if !input.Exists() || !input.IsArray() {
		return body
	}
	updated := body
	for index, item := range input.Array() {
		if strings.TrimSpace(item.Get("type").String()) != "reasoning" {
			continue
		}
		path := fmt.Sprintf("input.%d.encrypted_content", index)
		encrypted := gjson.GetBytes(updated, path)
		if !encrypted.Exists() {
			continue
		}
		invalid := false
		switch encrypted.Type {
		case gjson.String:
			raw := encrypted.String()
			if raw != strings.TrimSpace(raw) || InspectGPTReasoningSignature(raw) != nil {
				invalid = true
			}
		case gjson.Null:
			invalid = true
		default:
			invalid = true
		}
		if !invalid {
			continue
		}
		if next, err := sjson.DeleteBytes(updated, path); err == nil {
			updated = next
		}
	}
	return updated
}

// normalizeParallelToolCallsForTools：
// - Responses Lite：强制 parallel_tool_calls=false（上游硬性要求；Lite 顶层 tools 常为空）。
// - 普通请求：无 tools 时剥离 parallel_tool_calls，避免上游拒识。
func normalizeParallelToolCallsForTools(body []byte, lite bool) []byte {
	if lite {
		body, _ = sjson.SetBytes(body, "parallel_tool_calls", false)
		return body
	}
	if !gjson.GetBytes(body, "parallel_tool_calls").Exists() {
		return body
	}
	tools := gjson.GetBytes(body, "tools")
	if tools.Exists() && tools.IsArray() && len(tools.Array()) > 0 {
		return body
	}
	body, _ = sjson.DeleteBytes(body, "parallel_tool_calls")
	return body
}

func ensureImageGenerationTool(body []byte, baseModel string, planType string, lite bool) []byte {
	if lite {
		return body
	}
	if strings.HasSuffix(strings.TrimSpace(baseModel), "spark") {
		return body
	}
	if strings.EqualFold(strings.TrimSpace(planType), "free") {
		return body
	}
	tools := gjson.GetBytes(body, "tools")
	if !tools.Exists() || !tools.IsArray() {
		body, _ = sjson.SetRawBytes(body, "tools", imageGenToolArrayJSON)
		return body
	}
	for _, t := range tools.Array() {
		if t.Get("type").String() == "image_generation" {
			return body
		}
	}
	body, _ = sjson.SetRawBytes(body, "tools.-1", imageGenToolJSON)
	return body
}

// stripImageGenerationTools 从 tools 数组移除 image_generation 条目，
// 并清除 tool_choice（当其 type 为 image_generation 时）。
func stripImageGenerationTools(body []byte) []byte {
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return body
	}
	var kept []json.RawMessage
	changed := false
	tools.ForEach(func(_, tool gjson.Result) bool {
		if tool.Get("type").String() == "image_generation" {
			changed = true
			return true
		}
		kept = append(kept, json.RawMessage(tool.Raw))
		return true
	})
	if !changed {
		return body
	}
	if len(kept) == 0 {
		body, _ = sjson.DeleteBytes(body, "tools")
	} else {
		keptBytes, err := json.Marshal(kept)
		if err != nil {
			return body
		}
		body, _ = sjson.SetRawBytes(body, "tools", keptBytes)
	}
	if tc := gjson.GetBytes(body, "tool_choice"); tc.Exists() && tc.Get("type").String() == "image_generation" {
		body, _ = sjson.DeleteBytes(body, "tool_choice")
	}
	return body
}

func ApplyHeaders(req *http.Request, accessToken, accountID string, isStreaming bool, config *codexShared.CodexMultiConfig, clientHeaders http.Header) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	// 主集合 Version / Beta / Turn-Metadata / Request-Id / UA / Session(Mac) / Originator。
	filtered := FilterClientHeaders(clientHeaders)
	if val := filtered.Get("X-Codex-Beta-Features"); val != "" {
		req.Header.Set("X-Codex-Beta-Features", val)
	}
	if val := filtered.Get("Version"); val != "" {
		req.Header.Set("Version", val)
	} else if config != nil && strings.TrimSpace(config.Config.ClientVersion) != "" {
		req.Header.Set("Version", config.GetClientVersion())
	}
	if val := filtered.Get("X-Codex-Turn-Metadata"); val != "" {
		req.Header.Set("X-Codex-Turn-Metadata", val)
	}
	if val := filtered.Get("X-Client-Request-Id"); val != "" {
		req.Header.Set("X-Client-Request-Id", val)
	}

	// UA：配置优先 → 客户端透传 → 默认（不再强制伪装 DefaultCodexUserAgent）。
	userAgent := ""
	if config != nil {
		userAgent = strings.TrimSpace(config.GetUserAgent())
	}
	if userAgent == "" && clientHeaders != nil {
		userAgent = strings.TrimSpace(clientHeaders.Get("User-Agent"))
	}
	if userAgent == "" {
		userAgent = codexShared.DefaultCodexUserAgent
	}
	req.Header.Set("User-Agent", userAgent)

	// Session_id：仅 Mac UA 时 Ensure（透传或生成）
	if strings.Contains(userAgent, "Mac OS") {
		if val := filtered.Get("Session_id"); val != "" {
			req.Header.Set("Session_id", val)
		} else if req.Header.Get("Session_id") == "" {
			req.Header.Set("Session_id", uuid.NewString())
		}
	} else if val := filtered.Get("Session_id"); val != "" {
		req.Header.Set("Session_id", val)
	}

	if isStreaming {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("Connection", "Keep-Alive")

	if originator := strings.TrimSpace(filtered.Get("Originator")); originator != "" {
		req.Header.Set("Originator", originator)
	} else if config != nil {
		req.Header.Set("Originator", config.GetOriginator())
	} else {
		req.Header.Set("Originator", codexShared.DefaultCodexOriginator)
	}
	if accountID != "" {
		req.Header.Set("Chatgpt-Account-Id", accountID)
	}
	// 透传 Responses Lite 标记（HTTP 路径；WS 另有 body client_metadata）。
	if val := strings.TrimSpace(headerValueCaseInsensitive(clientHeaders, responsesLiteHeader)); val != "" {
		req.Header.Set(responsesLiteHeader, val)
	}
	if config != nil {
		for k, v := range config.GetCustomHeaders() {
			if k = strings.TrimSpace(k); k != "" {
				if v = strings.TrimSpace(v); v != "" {
					req.Header.Set(k, v)
				}
			}
		}
	}
}

func copyHeaderIfPresent(dst http.Header, src http.Header, key string) {
	if dst == nil || src == nil {
		return
	}
	if val := strings.TrimSpace(headerValueCaseInsensitive(src, key)); val != "" {
		setHeaderCasePreserved(dst, key, val)
	}
}

func ApplyWebsocketHeaders(accessToken, accountID string, config *codexShared.CodexMultiConfig, clientHeaders http.Header) http.Header {
	req, _ := http.NewRequest(http.MethodPost, UpstreamURL(config, "/responses"), nil)
	ApplyHeaders(req, accessToken, accountID, true, config, clientHeaders)
	headers := req.Header.Clone()

	headers.Del("Content-Type")
	headers.Del("Accept")
	headers.Del("Connection")

	if val := headerValueCaseInsensitive(clientHeaders, "X-Codex-Turn-State"); val != "" {
		headers.Set("X-Codex-Turn-State", val)
	}
	if val := headerValueCaseInsensitive(clientHeaders, "X-ResponsesAPI-Include-Timing-Metrics"); val != "" {
		headers.Set("X-ResponsesAPI-Include-Timing-Metrics", val)
	}
	// WS 握手侧可能携带 Lite header；与 body client_metadata 双通道对齐上游。
	if val := strings.TrimSpace(headerValueCaseInsensitive(clientHeaders, responsesLiteHeader)); val != "" {
		headers.Set(responsesLiteHeader, val)
	}
	betaHeader := strings.TrimSpace(headerValueCaseInsensitive(clientHeaders, "OpenAI-Beta"))
	if betaHeader == "" || !strings.Contains(betaHeader, "responses_websockets=") {
		betaHeader = "responses_websockets=2026-02-06"
	}
	headers.Set("OpenAI-Beta", betaHeader)

	if sessionID := strings.TrimSpace(headers.Get("Session_id")); sessionID != "" {
		setHeaderCasePreserved(headers, "session_id", sessionID)
		headers.Set("Conversation_id", sessionID)
	}
	if acctID := strings.TrimSpace(headers.Get("Chatgpt-Account-Id")); acctID != "" {
		setHeaderCasePreserved(headers, "ChatGPT-Account-ID", acctID)
	}
	return headers
}

func FilterClientHeaders(clientHeaders http.Header) http.Header {
	filtered := make(http.Header)
	if clientHeaders == nil {
		return filtered
	}
	// 上游透传主集合；Window/Conversation 仍可读作 replay sessionKey，但不强制写上游。
	for _, key := range []string{
		"Version",
		"Session_id",
		"X-Codex-Beta-Features",
		"X-Codex-Turn-Metadata",
		"X-Client-Request-Id",
		"Originator",
	} {
		if val := strings.TrimSpace(headerValueCaseInsensitive(clientHeaders, key)); val != "" {
			filtered.Set(key, val)
		}
	}
	return filtered
}

func headerValueCaseInsensitive(headers http.Header, key string) string {
	key = strings.TrimSpace(key)
	if headers == nil || key == "" {
		return ""
	}
	if val := strings.TrimSpace(headers.Get(key)); val != "" {
		return val
	}
	for existingKey, values := range headers {
		if !strings.EqualFold(existingKey, key) {
			continue
		}
		for _, value := range values {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func setHeaderCasePreserved(headers http.Header, key, value string) {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if headers == nil || key == "" || value == "" {
		return
	}
	for existingKey := range headers {
		if strings.EqualFold(existingKey, key) {
			delete(headers, existingKey)
		}
	}
	headers[key] = []string{value}
}

func applyPromptCache(source string, body []byte, originalBody []byte, clientHeaders http.Header, model string, accountID string) ([]byte, http.Header) {
	key := ""
	source = strings.ToLower(strings.TrimSpace(source))
	switch source {
	case "openai-response", SourceCodex, "":
		key = promptCacheKeyFromBody(body)
	case "claude":
		cacheBody := originalBody
		if len(cacheBody) == 0 {
			cacheBody = body
		}
		key = claudePromptCacheKey(cacheBody, model)
	case SourceOpenAIImage:
		key = pickPromptCacheKey(body, clientHeaders)
	case SourceOpenAI:
		key = pickPromptCacheKey(body, clientHeaders)
		if key == "" {
			key = deriveStableCacheKey(clientHeaders)
		}
	}
	if key == "" {
		return body, cloneHeader(clientHeaders)
	}
	body, _ = sjson.SetBytes(body, "prompt_cache_key", key)
	headers := cloneHeader(clientHeaders)
	if strings.TrimSpace(headers.Get("Session_id")) == "" {
		headers.Set("Session_id", key)
	}
	return body, headers
}

func pickPromptCacheKey(body []byte, headers http.Header) string {
	if headers != nil {
		if v := strings.TrimSpace(headers.Get("Session_id")); v != "" {
			return v
		}
	}
	return promptCacheKeyFromBody(body)
}

func promptCacheKeyFromBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	return strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String())
}

func deriveStableCacheKey(headers http.Header) string {
	seed := ""
	if headers != nil {
		for _, key := range []string{"Authorization", "X-Api-Key"} {
			if v := strings.TrimSpace(headers.Get(key)); v != "" {
				seed = v
				break
			}
		}
	}
	if seed == "" {
		return ""
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("clisimplehub:codex:prompt-cache:"+seed)).String()
}

func claudePromptCacheKey(body []byte, model string) string {
	userID := strings.TrimSpace(gjson.GetBytes(body, "metadata.user_id").String())
	if userID == "" {
		return ""
	}
	cacheKey := strings.TrimSpace(model) + "-" + userID
	now := time.Now()
	promptCacheStore.Lock()
	defer promptCacheStore.Unlock()
	if entry, ok := promptCacheStore.items[cacheKey]; ok && entry.ID != "" && entry.Expire.After(now) {
		return entry.ID
	}
	entry := promptCacheEntry{
		ID:     uuid.NewString(),
		Expire: now.Add(time.Hour),
	}
	promptCacheStore.items[cacheKey] = entry
	return entry.ID
}

func cloneHeader(headers http.Header) http.Header {
	if headers == nil {
		return http.Header{}
	}
	return headers.Clone()
}

type ModelSuffixResult struct {
	ModelName string
	HasSuffix bool
	RawSuffix string
}

func ParseModelSuffix(model string) ModelSuffixResult {
	model = strings.TrimSpace(model)
	if model == "" {
		return ModelSuffixResult{}
	}
	lastOpen := strings.LastIndex(model, "(")
	if lastOpen == -1 || !strings.HasSuffix(model, ")") {
		return ModelSuffixResult{ModelName: model}
	}
	return ModelSuffixResult{
		ModelName: strings.TrimSpace(model[:lastOpen]),
		HasSuffix: true,
		RawSuffix: strings.ToLower(strings.TrimSpace(model[lastOpen+1 : len(model)-1])),
	}
}

func BaseModelName(model string) string {
	return ParseModelSuffix(model).ModelName
}

func ApplySuffixThinking(body []byte, model string) ([]byte, bool) {
	suffix := ParseModelSuffix(model)
	if !suffix.HasSuffix {
		return body, false
	}
	switch suffix.RawSuffix {
	case "minimal", "low", "medium", "high", "xhigh", "max", "none", "auto":
	default:
		return body, false
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, false
	}
	reasoning, _ := payload["reasoning"].(map[string]any)
	if reasoning == nil {
		reasoning = make(map[string]any)
	}
	reasoning["effort"] = suffix.RawSuffix
	payload["reasoning"] = reasoning
	updated, err := json.Marshal(payload)
	if err != nil {
		return body, false
	}
	return updated, true
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

func invalidJSONError(kind string) error {
	return fmt.Errorf("invalid OpenAI image %s request JSON", kind)
}
