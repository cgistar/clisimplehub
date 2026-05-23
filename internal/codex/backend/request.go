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
	appmiddleware "clisimplehub/internal/middleware"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const defaultImageToolModel = "gpt-image-2"

var imageGenToolJSON = []byte(`{"type":"image_generation","output_format":"png"}`)
var imageGenToolArrayJSON = []byte(`[{"type":"image_generation","output_format":"png"}]`)

var promptCacheStore = struct {
	sync.Mutex
	items map[string]promptCacheEntry
}{items: make(map[string]promptCacheEntry)}

type promptCacheEntry struct {
	ID     string
	Expire time.Time
}

func Prepare(ctx context.Context, req Request) (*http.Request, []byte, *imagePreparedRequest, error) {
	body := append([]byte(nil), req.Body...)
	path := TargetPath(req.Path)
	var imageMeta *imagePreparedRequest
	if IsImagesPath(req.Path) {
		path = "/responses"
	}

	if IsCompactPath(req.Path) {
		req.IsStreaming = false
		body = finalizeCompactBody(body, req.Model)
	} else if IsImagesPath(req.Path) {
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
			return nil, nil, nil, err
		}
	} else {
		body = finalizeResponsesBody(body, req.Model, req.PlanType)
	}

	targetURL := UpstreamURL(req.Config, path)
	body, headers := applyPromptCache(req.Source, body, req.OriginalBody, req.Headers, req.Model, req.LocalAccountID)
	httpReq, err := http.NewRequestWithContext(ctx, methodOrPost(req.Method), targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, nil, err
	}
	ApplyHeaders(httpReq, req.AccessToken, req.AccountID, req.IsStreaming || IsImagesPath(req.Path), req.Config, headers)
	return httpReq, body, imageMeta, nil
}

func PrepareWebsocket(ctx context.Context, req Request) ([]byte, http.Header, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	body := append([]byte(nil), req.Body...)
	body = finalizeResponsesBody(body, req.Model, req.PlanType)
	body, headers := applyPromptCache(req.Source, body, req.OriginalBody, req.Headers, req.Model, req.LocalAccountID)
	return BuildWebsocketRequestBody(body), ApplyWebsocketHeaders(req.AccessToken, req.AccountID, req.Config, headers), nil
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

func finalizeResponsesBody(body []byte, model string, planType string) []byte {
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
	return ensureImageGenerationTool(body, baseModel, planType)
}

func finalizeCompactBody(body []byte, model string) []byte {
	baseModel := BaseModelName(model)
	if baseModel == "" {
		baseModel = BaseModelName(gjson.GetBytes(body, "model").String())
	}
	if baseModel != "" {
		body, _ = sjson.SetBytes(body, "model", baseModel)
	}
	body, _ = sjson.DeleteBytes(body, "stream")
	body = deleteUnsupportedFields(body)
	return normalizeInstructions(body)
}

func deleteUnsupportedFields(body []byte) []byte {
	for _, field := range []string{
		"previous_response_id",
		"prompt_cache_retention",
		"safety_identifier",
		"stream_options",
		"context_management",
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

func ensureImageGenerationTool(body []byte, baseModel string, planType string) []byte {
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

func ApplyHeaders(req *http.Request, accessToken, accountID string, isStreaming bool, config *codexShared.CodexMultiConfig, clientHeaders http.Header) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

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

	userAgent := codexShared.DefaultCodexUserAgent
	if config != nil {
		userAgent = config.GetUserAgent()
	}
	if config == nil || strings.TrimSpace(config.Config.UserAgent) == "" {
		if clientUserAgent := strings.TrimSpace(clientHeaders.Get("User-Agent")); appmiddleware.IsCodexCLI(clientUserAgent) {
			userAgent = clientUserAgent
		}
	}
	req.Header.Set("User-Agent", userAgent)
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

func ApplyWebsocketHeaders(accessToken, accountID string, config *codexShared.CodexMultiConfig, clientHeaders http.Header) http.Header {
	req, _ := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil)
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
	for _, key := range []string{
		"Version",
		"Session_id",
		"X-Codex-Beta-Features",
		"X-Codex-Turn-Metadata",
		"X-Client-Request-Id",
		"Originator",
	} {
		if val := strings.TrimSpace(clientHeaders.Get(key)); val != "" {
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
