package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	grokPool "clisimplehub/internal/transformer/grok"
	"clisimplehub/internal/transformer/grok/shared"
)

const (
	GrokChatAPI   = "https://grok.com"
	ChatPath      = "/rest/app-chat/conversations/new"
	RateLimitPath = "/rest/rate-limits"
)

type ModelInfo struct {
	GrokModel string
	ModelMode string
	IsImage   bool
	IsVideo   bool
}

var modelRegistry = map[string]ModelInfo{
	"grok-3":                 {GrokModel: "grok-3", ModelMode: "MODEL_MODE_GROK_3"},
	"grok-3-mini":            {GrokModel: "grok-3", ModelMode: "MODEL_MODE_GROK_3_MINI_THINKING"},
	"grok-3-thinking":        {GrokModel: "grok-3", ModelMode: "MODEL_MODE_GROK_3_THINKING"},
	"grok-4":                 {GrokModel: "grok-4", ModelMode: "MODEL_MODE_GROK_4"},
	"grok-4-mini":            {GrokModel: "grok-4-mini", ModelMode: "MODEL_MODE_GROK_4_MINI_THINKING"},
	"grok-4-thinking":        {GrokModel: "grok-4", ModelMode: "MODEL_MODE_GROK_4_THINKING"},
	"grok-4-heavy":           {GrokModel: "grok-4", ModelMode: "MODEL_MODE_HEAVY"},
	"grok-4.1-mini":          {GrokModel: "grok-4-1-thinking-1129", ModelMode: "MODEL_MODE_GROK_4_1_MINI_THINKING"},
	"grok-4.1-fast":          {GrokModel: "grok-4-1-thinking-1129", ModelMode: "MODEL_MODE_FAST"},
	"grok-4.1-expert":        {GrokModel: "grok-4-1-thinking-1129", ModelMode: "MODEL_MODE_EXPERT"},
	"grok-4.1-thinking":      {GrokModel: "grok-4-1-thinking-1129", ModelMode: "MODEL_MODE_GROK_4_1_THINKING"},
	"grok-imagine-1.0":       {GrokModel: "grok-3", ModelMode: "MODEL_MODE_FAST", IsImage: true},
	"grok-imagine-1.0-edit":  {GrokModel: "imagine-image-edit", ModelMode: "MODEL_MODE_FAST", IsImage: true},
	"grok-imagine-1.0-video": {GrokModel: "grok-3", ModelMode: "MODEL_MODE_FAST", IsVideo: true},
}

type Transformer struct {
	mu             sync.RWMutex
	currentAccount *shared.GrokAccount
	configPath     string
	proxyURL       string
	initOnce       sync.Once
}

func NewTransformer() *Transformer {
	return &Transformer{}
}

func (t *Transformer) ensureInitialized() {
	t.mu.RLock()
	hasAccount := t.currentAccount != nil
	t.mu.RUnlock()

	if hasAccount {
		return
	}

	t.initOnce.Do(func() {
		t.tryInitializeAccount()
	})

	t.mu.RLock()
	hasAccount = t.currentAccount != nil
	t.mu.RUnlock()
	if !hasAccount {
		t.tryInitializeAccount()
	}
}

func (t *Transformer) tryInitializeAccount() {
	pool := grokPool.GetPool()
	if pool == nil {
		fmt.Fprintf(os.Stderr, "[Grok] Warning: account pool not initialized\n")
		return
	}
	account := pool.Select()
	if account == nil {
		stats := pool.GetAccountStats()
		totalCount := stats["total"]
		if totalCount == 0 {
			fmt.Fprintf(os.Stderr, "[Grok] Error: no accounts configured in grok.json\n")
		} else {
			fmt.Fprintf(os.Stderr, "[Grok] Error: no available accounts (total: %d, valid: %d, invalid: %d, cooling: %d, failed: %d)\n",
				stats["total"], stats["valid"], stats["invalid"], stats["cooling"], stats["failed"])
			fmt.Fprintf(os.Stderr, "[Grok] Hint: check account status in grok.json or run 'go run cmd/grok-reset/main.go' to reset invalid accounts\n")
		}
		return
	}
	t.mu.Lock()
	t.currentAccount = account
	t.mu.Unlock()
}

func (t *Transformer) TargetInterfaceType() string {
	return "grok"
}

func (t *Transformer) TargetPath(isStreaming bool, upstreamModel string) string {
	return ChatPath
}

func (t *Transformer) OutputContentType(isStreaming bool) string {
	if isStreaming {
		return "text/event-stream"
	}
	return "application/json"
}

func (t *Transformer) GetAPIURL() string {
	return GrokChatAPI
}

func (t *Transformer) GetSsoToken() string {
	t.ensureInitialized()
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.currentAccount != nil {
		return t.currentAccount.SsoToken
	}
	return ""
}

func (t *Transformer) GrokProxyURL() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.currentAccount != nil && t.currentAccount.ProxyUrl != "" {
		return t.currentAccount.ProxyUrl
	}
	return t.proxyURL
}

func (t *Transformer) GetSettings() *shared.GrokSettings {
	return grokPool.GetSettings()
}

func (t *Transformer) RebindAccount() bool {
	pool := grokPool.GetPool()
	if pool == nil {
		return false
	}
	account := pool.Select()
	if account == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.currentAccount != nil && account.SsoToken == t.currentAccount.SsoToken {
		return false
	}
	t.currentAccount = account
	return true
}

func (t *Transformer) CurrentAccountSsoToken() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.currentAccount != nil {
		return t.currentAccount.SsoToken
	}
	return ""
}

func (t *Transformer) TransformRequest(modelName string, rawJSON []byte, stream bool) ([]byte, error) {
	t.ensureInitialized()
	ssoToken := t.GetSsoToken()
	proxyURL := t.GrokProxyURL()
	return buildGrokRequest(modelName, rawJSON, stream, ssoToken, proxyURL)
}

func (t *Transformer) TransformResponseStream(ctx context.Context, modelName string, originalReq, req, rawLine []byte, state *any) ([]string, error) {
	return transformStreamLine(modelName, rawLine, state)
}

func (t *Transformer) TransformResponseNonStream(ctx context.Context, modelName string, originalReq, req, rawJSON []byte, state *any) ([]byte, error) {
	return transformNonStream(modelName, rawJSON)
}

func LookupModel(modelID string) (ModelInfo, bool) {
	m, ok := modelRegistry[strings.ToLower(strings.TrimSpace(modelID))]
	return m, ok
}

func buildGrokRequest(modelName string, rawJSON []byte, stream bool, ssoToken, proxyURL string) ([]byte, error) {
	var openaiReq map[string]any
	if err := json.Unmarshal(rawJSON, &openaiReq); err != nil {
		return nil, fmt.Errorf("invalid request JSON: %w", err)
	}

	info, ok := LookupModel(modelName)
	if !ok {
		info = ModelInfo{GrokModel: modelName, ModelMode: ""}
	}

	settings := grokPool.GetSettings()
	if settings == nil {
		def := shared.DefaultSettings()
		settings = &def
	}

	// Extract messages with multimodal support
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	result, err := ExtractMessagesAndAttachments(ctx, openaiReq, ssoToken, proxyURL)
	if err != nil {
		return nil, fmt.Errorf("failed to extract messages: %w", err)
	}

	fileAttachments := make([]any, 0, len(result.FileAttachments))
	for _, id := range result.FileAttachments {
		fileAttachments = append(fileAttachments, id)
	}

	grokReq := map[string]any{
		"temporary":                 settings.GetTemporary(),
		"modelName":                 info.GrokModel,
		"message":                   result.Message,
		"fileAttachments":           fileAttachments,
		"imageAttachments":          []any{},
		"disableSearch":             false,
		"enableImageGeneration":     true,
		"returnImageBytes":          false,
		"returnRawGrokInXaiRequest": false,
		"enableImageStreaming":      false,
		"imageGenerationCount":      1,
		"forceConcise":              false,
		"toolOverrides": map[string]any{
			"imageGen": info.IsImage, // 根据模型类型启用图片生成
		},
		"enableSideBySide":  true,
		"isPreset":          false,
		"sendFinalMetadata": true,
		"customInstructions": "",
		"disableMemory":     settings.GetDisableMemory(),
		"responseMetadata": map[string]any{
			"modelConfigOverride": map[string]any{"modelMap": map[string]any{}},
			"requestModelDetails": map[string]any{"modelId": info.GrokModel},
		},
		"deviceEnvInfo": map[string]any{
			"darkModeEnabled":  false,
			"devicePixelRatio": 2,
			"screenWidth":      2056,
			"screenHeight":     1329,
			"viewportWidth":    2056,
			"viewportHeight":   1083,
		},
	}

	if info.ModelMode != "" {
		grokReq["modelMode"] = info.ModelMode
	}

	return json.Marshal(grokReq)
}

// transformStreamLine converts a Grok NDJSON line to OpenAI SSE format.
func transformStreamLine(modelName string, rawLine []byte, state *any) ([]string, error) {
	line := bytes.TrimSpace(rawLine)
	if len(line) == 0 {
		return nil, nil
	}

	var grokResp map[string]any
	if err := json.Unmarshal(line, &grokResp); err != nil {
		return nil, nil
	}

	result, _ := grokResp["result"].(map[string]any)
	if result == nil {
		return nil, nil
	}

	if state == nil {
		return nil, fmt.Errorf("nil state pointer")
	}
	if *state == nil {
		settings := grokPool.GetSettings()
		if settings == nil {
			def := shared.DefaultSettings()
			settings = &def
		}
		*state = &streamState{
			prevText:  "",
			isFirst:   true,
			tagFilter: NewTagFilter(settings.GetFilterTags()),
			settings:  settings,
		}
	}
	ss, ok := (*state).(*streamState)
	if !ok {
		settings := grokPool.GetSettings()
		if settings == nil {
			def := shared.DefaultSettings()
			settings = &def
		}
		*state = &streamState{
			prevText:  "",
			isFirst:   true,
			tagFilter: NewTagFilter(settings.GetFilterTags()),
			settings:  settings,
		}
		ss = (*state).(*streamState)
	}

	var results []string

	// Collect provider metadata (conversation/title/final metadata), even if this line isn't a token.
	ss.ensureProviderMeta()
	if conv, ok := result["conversation"].(map[string]any); ok && conv != nil {
		if id, _ := conv["conversationId"].(string); id != "" {
			ss.providerMeta["conversation_id"] = id
		}
	}
	if titleObj, ok := result["title"].(map[string]any); ok && titleObj != nil {
		if title, _ := titleObj["newTitle"].(string); title != "" {
			ss.providerMeta["title"] = title
		}
	}

	response, _ := result["response"].(map[string]any)
	if response == nil {
		return nil, nil
	}

	// Handle cachedImageGenerationResponse (primary source for image URLs)
	// 下载图片并转换为 base64 内联，避免 403 错误
	if cachedResp, ok := response["cachedImageGenerationResponse"].(map[string]any); ok {
		if imageURL, ok := cachedResp["imageUrl"].(string); ok && imageURL != "" {
			// 补全为完整 URL
			fullURL := normalizeImageURL(imageURL)

			// 获取当前账号的 SSO Token 和 Proxy
			ssoToken := ""
			proxyURL := ""
			if pool := grokPool.GetPool(); pool != nil {
				if account := pool.Select(); account != nil {
					ssoToken = account.SsoToken
					proxyURL = account.ProxyUrl
				}
			}

			// 下载图片并转换为 base64
			b64, err := downloadAndConvertToBase64(fullURL, proxyURL, ssoToken)
			var imgMarkdown string
			if err == nil && b64 != "" {
				// 成功下载，返回 base64 内联
				imgMarkdown = fmt.Sprintf("![image](data:image/jpeg;base64,%s)", b64)
			} else {
				// 下载失败，fallback 到原始 URL（可能会 403）
				imgMarkdown = fmt.Sprintf("![image](%s)", fullURL)
			}

			chunk := buildSSEChunk(modelName, imgMarkdown, "", "", ss.isFirst)
			ss.isFirst = false
			results = append(results, chunk)
			// 不要立即返回，继续处理后续响应
		}
	}

	// Pass-through provider-specific metadata if present.
	if pvrm, ok := response["pvrm"]; ok && pvrm != nil {
		ss.providerMeta["upstream_pvrm"] = pvrm
	}
	if fm, ok := response["finalMetadata"].(map[string]any); ok && fm != nil {
		ss.providerMeta["final_metadata"] = fm
	}

	// Handle streaming image generation progress
	if imgResp, ok := response["streamingImageGenerationResponse"].(map[string]any); ok {
		if ss.settings.GetThinking() {
			progress, _ := imgResp["progress"].(float64)
			imageIdx, _ := imgResp["imageIndex"].(float64)
			thinkContent := fmt.Sprintf("[Generating image %d: %.0f%%]", int(imageIdx)+1, progress*100)
			chunk := buildSSEChunk(modelName, thinkContent, "", "", ss.isFirst)
			ss.isFirst = false
			results = append(results, chunk)
		}
		return results, nil
	}

	// Handle modelResponse (final message with possible images)
	if modelResp, ok := response["modelResponse"].(map[string]any); ok {
		// Capture model response metadata (request trace, suggestions, etc.).
		if meta, ok := modelResp["metadata"].(map[string]any); ok && meta != nil {
			ss.providerMeta["model_response_metadata"] = meta
		}
		if rid, _ := modelResp["responseId"].(string); rid != "" {
			ss.providerMeta["response_id"] = rid
		}

		// Collect image URLs
		seen := make(map[string]bool)
		var imgURLs []string
		walkImageURLs(modelResp, seen, &imgURLs)

		if len(imgURLs) > 0 {
			var imgMarkdown strings.Builder
			for i, u := range imgURLs {
				fullURL := normalizeImageURL(u)
				fmt.Fprintf(&imgMarkdown, "![image_%d](%s)", i, fullURL)
				if i < len(imgURLs)-1 {
					imgMarkdown.WriteString("\n")
				}
			}
			chunk := buildSSEChunk(modelName, imgMarkdown.String(), "", "", ss.isFirst)
			ss.isFirst = false
			results = append(results, chunk)
		}

		// Flush tag filter
		if flushed := ss.tagFilter.Flush(); flushed != "" {
			chunk := buildSSEChunk(modelName, flushed, "", "", ss.isFirst)
			ss.isFirst = false
			results = append(results, chunk)
		}

		ext := map[string]any(nil)
		if len(ss.providerMeta) > 0 {
			ext = map[string]any{"pvrm": ss.providerMeta}
		}
		stopChunk := buildSSEChunkWithExt(modelName, "", "", "stop", ss.isFirst, ext)
		ss.isFirst = false
		results = append(results, stopChunk)
		return results, nil
	}

	// Extract fingerprint
	fingerprint := ""
	if llmInfo, ok := response["llmInfo"].(map[string]any); ok {
		fingerprint, _ = llmInfo["modelHash"].(string)
	}

	// Extract token (delta text)
	token, _ := response["token"].(string)
	if token == "" {
		return nil, nil
	}

	// Compute delta from cumulative text
	delta := token
	if len(ss.prevText) > 0 && strings.HasPrefix(token, ss.prevText) {
		delta = token[len(ss.prevText):]
	}
	ss.prevText = token

	if delta == "" {
		return nil, nil
	}

	// Apply tag filter
	filtered := ss.tagFilter.Filter(delta)
	if filtered == "" {
		return nil, nil
	}

	chunk := buildSSEChunk(modelName, filtered, fingerprint, "", ss.isFirst)
	ss.isFirst = false
	return []string{chunk}, nil
}

type streamState struct {
	prevText     string
	isFirst      bool
	tagFilter    *TagFilter
	settings     *shared.GrokSettings
	providerMeta map[string]any
}

func (s *streamState) ensureProviderMeta() {
	if s == nil {
		return
	}
	if s.providerMeta == nil {
		s.providerMeta = make(map[string]any)
	}
}

func buildSSEChunk(model, content, fingerprint, finishReason string, isFirst bool) string {
	return buildSSEChunkWithExt(model, content, fingerprint, finishReason, isFirst, nil)
}

func buildSSEChunkWithExt(model, content, fingerprint, finishReason string, isFirst bool, ext map[string]any) string {
	delta := map[string]any{}
	if isFirst {
		delta["role"] = "assistant"
	}
	if content != "" {
		delta["content"] = content
	}

	choice := map[string]any{
		"index":         0,
		"delta":         delta,
		"logprobs":      nil,
		"finish_reason": nil,
	}
	if finishReason != "" {
		choice["finish_reason"] = finishReason
	}

	chunk := map[string]any{
		"id":      "chatcmpl-grok-" + fmt.Sprintf("%d", time.Now().UnixMilli()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{choice},
	}
	if fingerprint != "" {
		chunk["system_fingerprint"] = fingerprint
	}
	for k, v := range ext {
		if k == "" || v == nil {
			continue
		}
		// Avoid overwriting standard OpenAI fields.
		if _, exists := chunk[k]; exists {
			continue
		}
		chunk[k] = v
	}

	data, _ := json.Marshal(chunk)
	return "data: " + string(data) + "\n\n"
}

func transformNonStream(modelName string, rawJSON []byte) ([]byte, error) {
	lines := bytes.Split(rawJSON, []byte("\n"))
	var fullText string
	var fingerprint string

	settings := grokPool.GetSettings()
	if settings == nil {
		def := shared.DefaultSettings()
		settings = &def
	}

	providerMeta := map[string]any{}
	var imgURLs []string
	imgSeen := make(map[string]bool)

	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var grokResp map[string]any
		if err := json.Unmarshal(line, &grokResp); err != nil {
			continue
		}
		result, _ := grokResp["result"].(map[string]any)
		if result == nil {
			continue
		}

		if conv, ok := result["conversation"].(map[string]any); ok && conv != nil {
			if id, _ := conv["conversationId"].(string); id != "" {
				providerMeta["conversation_id"] = id
			}
		}
		if titleObj, ok := result["title"].(map[string]any); ok && titleObj != nil {
			if title, _ := titleObj["newTitle"].(string); title != "" {
				providerMeta["title"] = title
			}
		}

		response, _ := result["response"].(map[string]any)
		if response == nil {
			continue
		}

		// Handle cachedImageGenerationResponse (primary source for image URLs)
		if cachedResp, ok := response["cachedImageGenerationResponse"].(map[string]any); ok {
			if imageURL, ok := cachedResp["imageUrl"].(string); ok && imageURL != "" && !imgSeen[imageURL] {
				imgSeen[imageURL] = true
				fullURL := normalizeImageURL(imageURL)
				imgURLs = append(imgURLs, fullURL)
			}
		}

		if pvrm, ok := response["pvrm"]; ok && pvrm != nil {
			providerMeta["upstream_pvrm"] = pvrm
		}
		if fm, ok := response["finalMetadata"].(map[string]any); ok && fm != nil {
			providerMeta["final_metadata"] = fm
		}

		if modelResp, ok := response["modelResponse"].(map[string]any); ok {
			if msg, ok := modelResp["message"].(string); ok {
				fullText = msg
			}
			if meta, ok := modelResp["metadata"].(map[string]any); ok && meta != nil {
				providerMeta["model_response_metadata"] = meta
			}
			if rid, _ := modelResp["responseId"].(string); rid != "" {
				providerMeta["response_id"] = rid
			}
			walkImageURLs(modelResp, imgSeen, &imgURLs)
		}
		if token, ok := response["token"].(string); ok && token != "" {
			fullText = token
		}
		if llmInfo, ok := response["llmInfo"].(map[string]any); ok {
			if h, ok := llmInfo["modelHash"].(string); ok {
				fingerprint = h
			}
		}
	}

	// Apply tag filter
	fullText = FilterContent(fullText, settings.GetFilterTags())

	// Append image URLs as markdown (下载并转换为 base64)
	if len(imgURLs) > 0 {
		// 获取当前账号的 SSO Token 和 Proxy
		ssoToken := ""
		proxyURL := ""
		if pool := grokPool.GetPool(); pool != nil {
			if account := pool.Select(); account != nil {
				ssoToken = account.SsoToken
				proxyURL = account.ProxyUrl
			}
		}

		var imgMarkdown strings.Builder
		imgMarkdown.WriteString("\n\n")
		for i, u := range imgURLs {
			fullURL := normalizeImageURL(u)
			// 下载图片并转换为 base64
			b64, err := downloadAndConvertToBase64(fullURL, proxyURL, ssoToken)
			if err == nil && b64 != "" {
				// 成功下载，返回 base64 内联
				fmt.Fprintf(&imgMarkdown, "![image_%d](data:image/jpeg;base64,%s)", i, b64)
			} else {
				// 下载失败，fallback 到原始 URL
				fmt.Fprintf(&imgMarkdown, "![image_%d](%s)", i, fullURL)
			}
			if i < len(imgURLs)-1 {
				imgMarkdown.WriteString("\n")
			}
		}
		fullText += imgMarkdown.String()
	}

	resp := map[string]any{
		"id":      "chatcmpl-grok-" + fmt.Sprintf("%d", time.Now().UnixMilli()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   modelName,
		"choices": []any{
			map[string]any{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": fullText,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		},
	}
	if fingerprint != "" {
		resp["system_fingerprint"] = fingerprint
	}
	if len(providerMeta) > 0 {
		resp["pvrm"] = providerMeta
	}

	return json.Marshal(resp)
}
