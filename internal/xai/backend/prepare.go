package backend

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// PreparedRequest 准备后的 Responses 请求。
type PreparedRequest struct {
	BaseModel   string
	Body        []byte
	SessionID   string
	ReplayScope ReplayScope
}

// PrepareOptions 控制 prepare 行为。
type PrepareOptions struct {
	Stream       bool
	Model        string
	SessionID    string // 显式会话（header / prompt_cache_key）
	IsWebsocket  bool
	IsCompact    bool
	KeepPrevious bool // WS 路径可保留 previous_response_id
	// EnableReplay：Claude 等多轮源启用 reasoning replay 注入
	EnableReplay bool
	// ReplaySessionKey：连续对话 key（空则从 body/session 推导）
	ReplaySessionKey string
	// Headers：用于解析 replay session key
	Headers interface{ Get(string) string }
}

// 仅处理 Responses JSON；媒体路径不应调用。
func PrepareResponsesBody(body []byte, opts PrepareOptions) (*PreparedRequest, error) {
	if len(body) == 0 {
		return &PreparedRequest{Body: body}, nil
	}
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("invalid responses request json")
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
	out = ApplyThinking(out, model)
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

	// Replay 注入须在 sanitize 之前（否则 encrypted_content 会被剥掉）
	replaySession := strings.TrimSpace(opts.ReplaySessionKey)
	if replaySession == "" {
		replaySession = ResolveReplaySessionKey(out, nil, opts.SessionID)
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

	sessionID := strings.TrimSpace(opts.SessionID)
	if sessionID == "" {
		if v := gjson.GetBytes(out, "prompt_cache_key"); v.Exists() {
			sessionID = strings.TrimSpace(v.String())
		}
	}
	if sessionID == "" && RequiresIsolatedConversation(baseModel) {
		sessionID = uuid.NewString()
	}
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
		Body:        out,
		SessionID:   sessionID,
		ReplayScope: replayScope,
	}, nil
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
	body = removeEncryptedReasoningInclude(body)
	if SupportsReasoningEffort(model) {
		return body
	}
	return stripReasoningEffort(body)
}

func removeEncryptedReasoningInclude(body []byte) []byte {
	include := gjson.GetBytes(body, "include")
	if !include.Exists() || !include.IsArray() {
		return body
	}
	kept := make([]string, 0, len(include.Array()))
	for _, item := range include.Array() {
		value := strings.TrimSpace(item.String())
		if value == "" || value == "reasoning.encrypted_content" {
			continue
		}
		kept = append(kept, value)
	}
	if len(kept) == 0 && len(include.Array()) > 0 {
		// 全被过滤：写空数组
		body, _ = sjson.SetBytes(body, "include", []string{})
		return body
	}
	if len(kept) != len(include.Array()) {
		body, _ = sjson.SetBytes(body, "include", kept)
	}
	return body
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
			if strings.TrimSpace(enc.String()) == "" {
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
