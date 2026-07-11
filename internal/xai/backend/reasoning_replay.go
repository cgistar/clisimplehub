package backend

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ApplyReasoningReplay 在 sanitize 之前注入缓存的 reasoning / tool_call。
// enable 通常仅 Claude 源打开。
func ApplyReasoningReplay(body []byte, modelName, sessionKey string, enable bool) ([]byte, ReplayScope) {
	scope := ReplayScope{
		ModelName:  BaseModelName(modelName),
		SessionKey: strings.TrimSpace(sessionKey),
	}
	if !enable || !scope.Valid() {
		return body, scope
	}
	items, ok := GetReasoningReplayItems(scope.ModelName, scope.SessionKey)
	if !ok || len(items) == 0 {
		return body, scope
	}
	items = filterReasoningReplayItemsForInput(body, items)
	if len(items) == 0 {
		return body, scope
	}
	updated, ok := insertReasoningReplayItems(body, items)
	if !ok {
		return body, scope
	}
	return updated, scope
}

// CacheReasoningReplayFromCompleted 从 response.completed 事件写缓存。
func CacheReasoningReplayFromCompleted(scope ReplayScope, completedData []byte) {
	if !scope.Valid() || len(completedData) == 0 {
		return
	}
	output := gjson.GetBytes(completedData, "response.output")
	if !output.IsArray() {
		// 也可能是已聚合的 response 对象
		output = gjson.GetBytes(completedData, "output")
	}
	if !output.IsArray() {
		return
	}
	items := make([][]byte, 0, len(output.Array()))
	for _, item := range output.Array() {
		switch strings.TrimSpace(item.Get("type").String()) {
		case "reasoning", "function_call", "custom_tool_call":
			items = append(items, []byte(item.Raw))
		}
	}
	if !CacheReasoningReplayItems(scope.ModelName, scope.SessionKey, items) {
		DeleteReasoningReplayItems(scope.ModelName, scope.SessionKey)
	}
}

// ResolveReplaySessionKey 解析连续对话 key
// originalClaudeBody：Claude 原始 body（转换前），用于提取 session；可与 body 相同。
func ResolveReplaySessionKey(body []byte, headers http.Header, explicit string) string {
	return ResolveReplaySessionKeyWithClaude(body, nil, headers, explicit)
}

// ResolveReplaySessionKeyWithClaude 优先用 Claude 原始 payload 解析 session。
func ResolveReplaySessionKeyWithClaude(body, originalClaudeBody []byte, headers http.Header, explicit string) string {
	if v := strings.TrimSpace(explicit); v != "" {
		// 若调用方已给出完整 key（claude:/prompt-cache:）则直接用
		if strings.Contains(v, ":") {
			return v
		}
		return "session:" + v
	}
	// Claude Code session（必须在 Messages→Responses 转换前解析）
	for _, payload := range [][]byte{originalClaudeBody, body} {
		if k := ClaudeReplaySessionKey(payload, headers); k != "" {
			return k
		}
	}
	if len(body) > 0 {
		if k := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String()); k != "" {
			return "prompt-cache:" + k
		}
		if k := strings.TrimSpace(gjson.GetBytes(body, "client_metadata.x-codex-window-id").String()); k != "" {
			return "window:" + k
		}
		if turn := strings.TrimSpace(gjson.GetBytes(body, "client_metadata.x-codex-turn-metadata").String()); turn != "" {
			if k := strings.TrimSpace(gjson.Get(turn, "prompt_cache_key").String()); k != "" {
				return "prompt-cache:" + k
			}
			if k := strings.TrimSpace(gjson.Get(turn, "window_id").String()); k != "" {
				return "window:" + k
			}
		}
	}
	if headers != nil {
		if turn := headerGet(headers, "X-Codex-Turn-Metadata"); turn != "" {
			if k := strings.TrimSpace(gjson.Get(turn, "prompt_cache_key").String()); k != "" {
				return "prompt-cache:" + k
			}
			if k := strings.TrimSpace(gjson.Get(turn, "window_id").String()); k != "" {
				return "window:" + k
			}
		}
		if k := headerGet(headers, "X-Codex-Window-Id"); k != "" {
			return "window:" + k
		}
		for _, name := range []string{"Session_id", "session_id", "Session-Id", "x-session-id"} {
			if k := headerGet(headers, name); k != "" {
				return "session-id:" + k
			}
		}
		if k := headerGet(headers, "Conversation_id"); k != "" {
			return "conversation_id:" + k
		}
		if k := headerGet(headers, HeaderGrokConvID); k != "" {
			return "grok-conv:" + k
		}
	}
	return ""
}

func inputHasValidGrokReasoningEncrypted(body []byte) bool {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return false
	}
	for _, item := range input.Array() {
		if strings.TrimSpace(item.Get("type").String()) != "reasoning" {
			continue
		}
		enc := item.Get("encrypted_content")
		if enc.Type == gjson.String && IsValidGrokEncryptedContent(enc.String()) {
			return true
		}
	}
	return false
}

func filterReasoningReplayItemsForInput(body []byte, items [][]byte) [][]byte {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return nil
	}
	hasInputReasoning := inputHasValidGrokReasoningEncrypted(body)
	existingCalls := make(map[string]bool)
	existingOutputs := make(map[string]bool)
	for _, inputItem := range input.Array() {
		itemType := strings.TrimSpace(inputItem.Get("type").String())
		if itemType == "function_call_output" || itemType == "custom_tool_call_output" {
			callID := strings.TrimSpace(inputItem.Get("call_id").String())
			if callID != "" {
				for _, candidate := range replayComparableCallIDs(callID) {
					existingOutputs[candidate] = true
				}
			}
		}
		for _, key := range replayToolCallKeys(inputItem) {
			existingCalls[key] = true
		}
	}

	filtered := make([][]byte, 0, len(items))
	for _, item := range items {
		itemResult := gjson.ParseBytes(item)
		switch strings.TrimSpace(itemResult.Get("type").String()) {
		case "reasoning":
			if hasInputReasoning {
				continue
			}
		case "function_call", "custom_tool_call":
			keys := replayToolCallKeys(itemResult)
			if len(keys) == 0 || replayAnyKeyExists(existingCalls, keys) {
				continue
			}
			hasMatchingOutput := false
			callID := strings.TrimSpace(itemResult.Get("call_id").String())
			if callID != "" {
				for _, candidate := range replayComparableCallIDs(callID) {
					if existingOutputs[candidate] {
						hasMatchingOutput = true
						break
					}
				}
			}
			if !hasMatchingOutput {
				continue
			}
			for _, key := range keys {
				existingCalls[key] = true
			}
		default:
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func insertReasoningReplayItems(body []byte, replayItems [][]byte) ([]byte, bool) {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() || len(replayItems) == 0 {
		return body, false
	}
	inputItems := input.Array()
	insertIndex := reasoningReplayInsertIndex(inputItems, replayItems)
	replayItems = alignReasoningReplayToolCallIDs(inputItems, replayItems)
	items := make([]string, 0, len(inputItems)+len(replayItems))
	for i, inputItem := range inputItems {
		if i == insertIndex {
			for _, replayItem := range replayItems {
				items = append(items, string(replayItem))
			}
		}
		items = append(items, inputItem.Raw)
	}
	if insertIndex == len(inputItems) {
		for _, replayItem := range replayItems {
			items = append(items, string(replayItem))
		}
	}
	updated, err := sjson.SetRawBytes(body, "input", []byte("["+strings.Join(items, ",")+"]"))
	if err != nil {
		return body, false
	}
	return updated, true
}

func reasoningReplayInsertIndex(inputItems []gjson.Result, replayItems [][]byte) int {
	replayCallIDs := make(map[string]bool)
	for _, replayItem := range replayItems {
		itemResult := gjson.ParseBytes(replayItem)
		itemType := strings.TrimSpace(itemResult.Get("type").String())
		if itemType != "function_call" && itemType != "custom_tool_call" {
			continue
		}
		for _, callID := range replayComparableCallIDs(itemResult.Get("call_id").String()) {
			replayCallIDs[callID] = true
		}
	}
	if len(replayCallIDs) > 0 {
		for index, inputItem := range inputItems {
			itemType := strings.TrimSpace(inputItem.Get("type").String())
			if itemType != "function_call_output" && itemType != "custom_tool_call_output" {
				continue
			}
			callID := strings.TrimSpace(inputItem.Get("call_id").String())
			if callID == "" || replayCallIDs[callID] {
				return index
			}
		}
	}
	for index := len(inputItems) - 1; index >= 0; index-- {
		inputItem := inputItems[index]
		if strings.TrimSpace(inputItem.Get("type").String()) == "message" &&
			strings.TrimSpace(inputItem.Get("role").String()) == "assistant" {
			return index
		}
	}
	for index, inputItem := range inputItems {
		if shouldInsertReasoningReplayBefore(inputItem) {
			return index
		}
	}
	return len(inputItems)
}

func alignReasoningReplayToolCallIDs(inputItems []gjson.Result, replayItems [][]byte) [][]byte {
	outputCallIDs := make(map[string]string)
	for _, inputItem := range inputItems {
		itemType := strings.TrimSpace(inputItem.Get("type").String())
		if itemType != "function_call_output" && itemType != "custom_tool_call_output" {
			continue
		}
		callID := strings.TrimSpace(inputItem.Get("call_id").String())
		if callID == "" {
			continue
		}
		for _, candidate := range replayComparableCallIDs(callID) {
			outputCallIDs[candidate] = callID
		}
	}
	if len(outputCallIDs) == 0 {
		return replayItems
	}
	aligned := make([][]byte, 0, len(replayItems))
	for _, replayItem := range replayItems {
		itemResult := gjson.ParseBytes(replayItem)
		itemType := strings.TrimSpace(itemResult.Get("type").String())
		if itemType != "function_call" && itemType != "custom_tool_call" {
			aligned = append(aligned, replayItem)
			continue
		}
		callID := strings.TrimSpace(itemResult.Get("call_id").String())
		outputCallID := ""
		for _, candidate := range replayComparableCallIDs(callID) {
			if value := outputCallIDs[candidate]; value != "" {
				outputCallID = value
				break
			}
		}
		if outputCallID == "" || outputCallID == callID {
			aligned = append(aligned, replayItem)
			continue
		}
		updated, err := sjson.SetBytes(replayItem, "call_id", outputCallID)
		if err != nil {
			aligned = append(aligned, replayItem)
			continue
		}
		aligned = append(aligned, updated)
	}
	return aligned
}

func shouldInsertReasoningReplayBefore(item gjson.Result) bool {
	if strings.TrimSpace(item.Get("type").String()) != "message" {
		return true
	}
	switch strings.TrimSpace(item.Get("role").String()) {
	case "developer", "system":
		return false
	default:
		return true
	}
}

func replayToolCallKeys(item gjson.Result) []string {
	itemType := strings.TrimSpace(item.Get("type").String())
	if itemType != "function_call" && itemType != "custom_tool_call" {
		return nil
	}
	callIDs := replayComparableCallIDs(item.Get("call_id").String())
	if len(callIDs) == 0 {
		return nil
	}
	keys := make([]string, 0, len(callIDs))
	for _, callID := range callIDs {
		keys = append(keys, itemType+":"+callID)
	}
	return keys
}

func replayAnyKeyExists(existing map[string]bool, keys []string) bool {
	for _, key := range keys {
		if existing[key] {
			return true
		}
	}
	return false
}

func replayComparableCallIDs(callID string) []string {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return nil
	}
	// Claude tool id 可能被截断到 64
	if len(callID) > 64 {
		return []string{callID, callID[:64]}
	}
	return []string{callID}
}
