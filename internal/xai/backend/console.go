package backend

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// ConsoleResponsesURL console.x.ai Responses 端点（SSO 免费 Chat）。
	ConsoleResponsesURL = "https://console.x.ai/v1/responses"
	// ConsoleCluster 与抓包一致的 x-cluster。
	ConsoleCluster = "https://us-east-1.api.x.ai"
)

// 对外模型名 → console.x.ai model 字段
var consoleModelMap = map[string]string{
	"grok-4.3-console":                     "grok-4.3",
	"grok-4.3-low":                         "grok-4.3",
	"grok-4.3-medium":                      "grok-4.3",
	"grok-4.3-high":                        "grok-4.3",
	"grok-4.20-0309-reasoning-console":     "grok-4.20-0309-reasoning",
	"grok-4.20-0309-console":               "grok-4.20-0309",
	"grok-4.20-0309-non-reasoning-console": "grok-4.20-0309-non-reasoning",
	"grok-4.20-multi-agent-console":        "grok-4.20-multi-agent-0309",
	"grok-4.20-multi-agent-low":            "grok-4.20-multi-agent-0309",
	"grok-4.20-multi-agent-medium":         "grok-4.20-multi-agent-0309",
	"grok-4.20-multi-agent-high":           "grok-4.20-multi-agent-0309",
	"grok-4.20-multi-agent-xhigh":          "grok-4.20-multi-agent-0309",
	"grok-build-console":                   "grok-build-0.1",
	// 允许直接传上游模型名
	"grok-4.3":                     "grok-4.3",
	"grok-4.20-0309-reasoning":     "grok-4.20-0309-reasoning",
	"grok-4.20-0309":               "grok-4.20-0309",
	"grok-4.20-0309-non-reasoning": "grok-4.20-0309-non-reasoning",
	"grok-4.20-multi-agent-0309":   "grok-4.20-multi-agent-0309",
	"grok-build-0.1":               "grok-build-0.1",
}

var consoleModelsWithReasoning = map[string]struct{}{
	"grok-4.3":                   {},
	"grok-4.20-multi-agent-0309": {},
}

var consoleModelFixedEffort = map[string]string{
	"grok-4.3-low":                 "low",
	"grok-4.3-medium":              "medium",
	"grok-4.3-high":                "high",
	"grok-4.20-multi-agent-low":    "low",
	"grok-4.20-multi-agent-medium": "medium",
	"grok-4.20-multi-agent-high":   "high",
	"grok-4.20-multi-agent-xhigh":  "xhigh",
}

var consoleModelMaxOutput = map[string]int{
	"grok-4.20-multi-agent-0309": 2_000_000,
	"grok-build-0.1":             256_000,
}

var consoleModelsWithSearch = map[string]struct{}{
	"grok-4.20-multi-agent-0309":   {},
	"grok-4.20-0309":               {},
	"grok-4.20-0309-reasoning":     {},
	"grok-4.20-0309-non-reasoning": {},
	"grok-4.3":                     {},
	"grok-build-0.1":               {},
}

var effortMap = map[string]string{
	"none":    "none",
	"minimal": "low",
	"low":     "low",
	"medium":  "medium",
	"high":    "high",
	"xhigh":   "xhigh",
}

// ResolveConsoleModel 返回 console 上游 model；未知则原样返回 trimmed。
func ResolveConsoleModel(requestModel string) string {
	m := strings.TrimSpace(requestModel)
	if m == "" {
		return "grok-4.3"
	}
	if v, ok := consoleModelMap[m]; ok {
		return v
	}
	return m
}

// IsConsoleModel 是否识别为 console 体系模型。
func IsConsoleModel(requestModel string) bool {
	m := strings.TrimSpace(requestModel)
	_, ok := consoleModelMap[m]
	return ok
}

// ConsolePublicModelIDs 对外 /xai/console/v1/models 列表。
func ConsolePublicModelIDs() []string {
	// 稳定顺序：优先别名
	ids := []string{
		"grok-4.3-console",
		"grok-4.3-low",
		"grok-4.3-medium",
		"grok-4.3-high",
		"grok-4.20-0309-reasoning-console",
		"grok-4.20-0309-console",
		"grok-4.20-0309-non-reasoning-console",
		"grok-4.20-multi-agent-console",
		"grok-4.20-multi-agent-low",
		"grok-4.20-multi-agent-medium",
		"grok-4.20-multi-agent-high",
		"grok-4.20-multi-agent-xhigh",
		"grok-build-console",
		// 媒体（grok.com reverse + SSO）
		"grok-imagine-image",
		"grok-imagine-image-lite",
		"grok-imagine-image-pro",
		"grok-imagine-image-quality",
		"grok-imagine-video",
	}
	return ids
}

// IsConsoleImageModel chat/images 图片生成模型。
func IsConsoleImageModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(m, "grok-imagine-image")
}

// IsConsoleImageLiteModel lite 模型（Imagine WS enable_pro=false）。
func IsConsoleImageLiteModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return m == "grok-imagine-image-lite" || strings.HasSuffix(m, "-lite")
}

// IsConsoleImageProModel quality/pro 走 Imagine WS enable_pro=true。
func IsConsoleImageProModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	// lite 优先，避免误匹配
	if IsConsoleImageLiteModel(m) {
		return false
	}
	return strings.Contains(m, "pro") || strings.Contains(m, "quality")
}

// IsConsoleVideoModel 视频模型。
func IsConsoleVideoModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(m, "grok-imagine-video") || m == "grok-video"
}

// IsConsoleChatModel console.x.ai 文本对话模型。
func IsConsoleChatModel(model string) bool {
	m := strings.TrimSpace(model)
	if m == "" {
		return true
	}
	if IsConsoleImageModel(m) || IsConsoleVideoModel(m) {
		return false
	}
	// 已知 console 别名或默认文本
	if _, ok := consoleModelMap[m]; ok {
		return true
	}
	// 未知模型默认按文本走 console.x.ai（由上游报错）
	return true
}

// ConsoleModelsResponse OpenAI 兼容 models 列表。
func ConsoleModelsResponse() map[string]any {
	data := make([]map[string]any, 0, len(ConsolePublicModelIDs()))
	for _, id := range ConsolePublicModelIDs() {
		data = append(data, map[string]any{
			"id":       id,
			"object":   "model",
			"created":  int64(1775606400),
			"owned_by": "xai-console",
		})
	}
	return map[string]any{"object": "list", "data": data}
}

// BuildConsolePayload 将 chat.completions body 转为 console.x.ai/v1/responses payload。
func BuildConsolePayload(chatBody []byte) (map[string]any, string, bool, error) {
	var req struct {
		Model           string          `json:"model"`
		Messages        json.RawMessage `json:"messages"`
		Temperature     *float64        `json:"temperature"`
		TopP            *float64        `json:"top_p"`
		Stream          *bool           `json:"stream"`
		ReasoningEffort string          `json:"reasoning_effort"`
		MaxTokens       *int            `json:"max_tokens"`
		MaxOutputTokens *int            `json:"max_output_tokens"`
	}
	if err := json.Unmarshal(chatBody, &req); err != nil {
		return nil, "", false, err
	}
	var messages []map[string]any
	if len(req.Messages) > 0 {
		if err := json.Unmarshal(req.Messages, &messages); err != nil {
			return nil, "", false, err
		}
	}
	return buildConsolePayloadFromMessages(
		req.Model, messages, req.Temperature, req.TopP, req.Stream, req.ReasoningEffort, req.MaxTokens, req.MaxOutputTokens,
	)
}

// BuildConsolePayloadFromResponses 将 OpenAI Responses body 转为 console payload。
func BuildConsolePayloadFromResponses(body []byte) (map[string]any, string, bool, error) {
	var req struct {
		Model           string          `json:"model"`
		Input           json.RawMessage `json:"input"`
		Instructions    string          `json:"instructions"`
		Temperature     *float64        `json:"temperature"`
		TopP            *float64        `json:"top_p"`
		Stream          *bool           `json:"stream"`
		ReasoningEffort string          `json:"reasoning_effort"`
		MaxOutputTokens *int            `json:"max_output_tokens"`
		Reasoning       *struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, "", false, err
	}
	effort := req.ReasoningEffort
	if req.Reasoning != nil && strings.TrimSpace(req.Reasoning.Effort) != "" {
		effort = req.Reasoning.Effort
	}
	messages := responsesInputToMessages(req.Input, req.Instructions)
	return buildConsolePayloadFromMessages(
		req.Model, messages, req.Temperature, req.TopP, req.Stream, effort, nil, req.MaxOutputTokens,
	)
}

// BuildConsolePayloadFromAnthropic 将 Anthropic Messages body 转为 console payload。
func BuildConsolePayloadFromAnthropic(body []byte) (map[string]any, string, bool, error) {
	var req struct {
		Model       string          `json:"model"`
		System      json.RawMessage `json:"system"`
		Messages    json.RawMessage `json:"messages"`
		Temperature *float64        `json:"temperature"`
		TopP        *float64        `json:"top_p"`
		Stream      *bool           `json:"stream"`
		MaxTokens   *int            `json:"max_tokens"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, "", false, err
	}
	var messages []map[string]any
	if len(req.Messages) > 0 {
		if err := json.Unmarshal(req.Messages, &messages); err != nil {
			return nil, "", false, err
		}
	}
	if sys := anthropicSystemText(req.System); sys != "" {
		messages = append([]map[string]any{{"role": "system", "content": sys}}, messages...)
	}
	// Anthropic 默认非流
	stream := req.Stream
	if stream == nil {
		f := false
		stream = &f
	}
	return buildConsolePayloadFromMessages(
		req.Model, messages, req.Temperature, req.TopP, stream, "", req.MaxTokens, nil,
	)
}

func buildConsolePayloadFromMessages(
	requestModel string,
	messages []map[string]any,
	temperature, topP *float64,
	stream *bool,
	reasoningEffort string,
	maxTokens, maxOutputTokens *int,
) (map[string]any, string, bool, error) {
	requestModel = strings.TrimSpace(requestModel)
	if requestModel == "" {
		requestModel = "grok-4.3-console"
	}
	consoleModel := ResolveConsoleModel(requestModel)

	inputItems := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		if role == "" {
			role = "user"
		}
		apiRole := role
		switch role {
		case "system", "developer":
			apiRole = "system"
		case "assistant":
			apiRole = "assistant"
		default:
			apiRole = "user"
		}
		blocks := consoleContentBlocks(msg["content"])
		if len(blocks) == 0 {
			continue
		}
		inputItems = append(inputItems, map[string]any{
			"role":    apiRole,
			"content": blocks,
		})
	}
	if len(inputItems) == 0 {
		return nil, "", false, fmt.Errorf("messages/input cannot be empty")
	}

	temp := 0.7
	if temperature != nil {
		temp = *temperature
	}
	tp := 0.95
	if topP != nil {
		tp = *topP
	}
	streamVal := true
	if stream != nil {
		streamVal = *stream
	}

	effort := consoleModelFixedEffort[requestModel]
	if effort == "" {
		effort = effortMap[strings.ToLower(strings.TrimSpace(reasoningEffort))]
		if effort == "" {
			effort = "medium"
		}
	}

	maxOut := consoleModelMaxOutput[consoleModel]
	if maxOut <= 0 {
		maxOut = 1_000_000
	}
	if maxOutputTokens != nil && *maxOutputTokens > 0 {
		maxOut = *maxOutputTokens
	} else if maxTokens != nil && *maxTokens > 0 {
		maxOut = *maxTokens
	}

	payload := map[string]any{
		"model":             consoleModel,
		"input":             inputItems,
		"max_output_tokens": maxOut,
		"temperature":       temp,
		"top_p":             tp,
		"store":             false,
		"include":           []string{"reasoning.encrypted_content"},
		"stream":            streamVal,
	}
	if _, ok := consoleModelsWithReasoning[consoleModel]; ok {
		payload["reasoning"] = map[string]any{"effort": effort}
	}
	if _, ok := consoleModelsWithSearch[consoleModel]; ok {
		payload["tools"] = []map[string]any{
			{"type": "web_search", "enable_image_understanding": true},
			{"type": "x_search", "enable_video_understanding": true},
		}
		payload["tool_choice"] = "auto"
	}
	return payload, requestModel, streamVal, nil
}

func responsesInputToMessages(input json.RawMessage, instructions string) []map[string]any {
	out := make([]map[string]any, 0, 4)
	if s := strings.TrimSpace(instructions); s != "" {
		out = append(out, map[string]any{"role": "system", "content": s})
	}
	if len(input) == 0 {
		return out
	}
	// string
	var asStr string
	if err := json.Unmarshal(input, &asStr); err == nil {
		if strings.TrimSpace(asStr) != "" {
			out = append(out, map[string]any{"role": "user", "content": asStr})
		}
		return out
	}
	// array of messages / items
	var arr []map[string]any
	if err := json.Unmarshal(input, &arr); err == nil {
		for _, item := range arr {
			role, _ := item["role"].(string)
			if role == "" {
				// Responses content item 无 role 时当 user
				if c := item["content"]; c != nil {
					out = append(out, map[string]any{"role": "user", "content": c})
				} else if t, _ := item["type"].(string); t == "message" {
					out = append(out, map[string]any{"role": "user", "content": item["content"]})
				}
				continue
			}
			out = append(out, map[string]any{"role": role, "content": item["content"]})
		}
		return out
	}
	return out
}

func anthropicSystemText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var blocks []map[string]any
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, bl := range blocks {
			if t, _ := bl["type"].(string); t == "text" || t == "" {
				b.WriteString(asString(bl["text"]))
			}
		}
		return strings.TrimSpace(b.String())
	}
	return ""
}

func consoleContentBlocks(content any) []map[string]any {
	switch c := content.(type) {
	case string:
		if c == "" {
			return nil
		}
		return []map[string]any{{"type": "input_text", "text": c}}
	case []any:
		blocks := make([]map[string]any, 0, len(c))
		for _, item := range c {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch strings.TrimSpace(asString(m["type"])) {
			case "text":
				blocks = append(blocks, map[string]any{"type": "input_text", "text": asString(m["text"])})
			case "image_url":
				url := ""
				if iu, ok := m["image_url"].(map[string]any); ok {
					url = asString(iu["url"])
				}
				if url != "" {
					blocks = append(blocks, map[string]any{"type": "input_image", "image_url": url})
				}
			default:
				if t := asString(m["text"]); t != "" {
					blocks = append(blocks, map[string]any{"type": "input_text", "text": t})
				}
			}
		}
		return blocks
	default:
		s := asString(c)
		if s == "" {
			return nil
		}
		return []map[string]any{{"type": "input_text", "text": s}}
	}
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// BuildConsoleHeaders 构造 console.x.ai 请求头（Bearer anonymous + sso Cookie）。
func BuildConsoleHeaders(sso string) map[string]string {
	sso = strings.TrimSpace(sso)
	if strings.HasPrefix(strings.ToLower(sso), "sso=") {
		sso = strings.TrimSpace(sso[4:])
	}
	return map[string]string{
		"Accept":          "*/*",
		"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
		"Authorization":   "Bearer anonymous",
		"Content-Type":    "application/json",
		"Cookie":          "sso=" + sso + "; sso-rw=" + sso,
		"Origin":          "https://console.x.ai",
		"Referer":         "https://console.x.ai/",
		"User-Agent":      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36",
		"x-cluster":       ConsoleCluster,
	}
}
