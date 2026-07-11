package responses

import (
	"encoding/json"
	"fmt"
	"strings"

	"clisimplehub/internal/transformer/shared"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func appendWebSearchServerToolUse(outputs []string, state *responsesToClaudeStreamState, root, item gjson.Result) []string {
	toolUseID := webSearchToolUseID(state, root, item)
	if toolUseID == "" {
		return outputs
	}
	if state.WebSearchToolUseIDs == nil {
		state.WebSearchToolUseIDs = make(map[string]struct{})
	}
	query := webSearchQuery(root, item)
	alreadyStarted := false
	if _, ok := state.WebSearchToolUseIDs[toolUseID]; ok {
		alreadyStarted = true
		if query == "" {
			return outputs
		}
	}

	if !alreadyStarted {
		outputs = append(outputs, finalizeClaudeThinkingBlock(state)...)
		outputs = append(outputs, stopClaudeTextBlock(state)...)
		outputs = append(outputs, shared.SSEEvent("content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": state.BlockIndex,
			"content_block": map[string]any{
				"type":  "server_tool_use",
				"id":    toolUseID,
				"name":  "web_search",
				"input": map[string]any{},
			},
		}))
	}

	if query != "" {
		partialJSON, _ := json.Marshal(map[string]string{"query": query})
		outputs = append(outputs, shared.SSEEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": state.BlockIndex,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": string(partialJSON),
			},
		}))
	}

	if !alreadyStarted {
		outputs = append(outputs, shared.SSEEvent("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": state.BlockIndex,
		}))
		state.WebSearchToolUseIDs[toolUseID] = struct{}{}
		state.BlockIndex++
	}
	return outputs
}

func appendWebSearchToolResult(outputs []string, state *responsesToClaudeStreamState, root, item gjson.Result) []string {
	toolUseID := webSearchToolUseID(state, root, item)
	if toolUseID == "" {
		return outputs
	}
	outputs = appendWebSearchServerToolUse(outputs, state, root, item)
	if state.WebSearchToolResultIDs == nil {
		state.WebSearchToolResultIDs = make(map[string]struct{})
	}
	if _, ok := state.WebSearchToolResultIDs[toolUseID]; ok {
		return outputs
	}
	if webSearchQuery(root, item) == "" && len(webSearchResultContent(root, item)) == 0 && !item.Get("action").Exists() {
		return outputs
	}

	content := []any{}
	if raw := webSearchResultContent(root, item); len(raw) > 0 {
		_ = json.Unmarshal(raw, &content)
	}
	outputs = append(outputs, shared.SSEEvent("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": state.BlockIndex,
		"content_block": map[string]any{
			"type":       "web_search_tool_result",
			"tool_use_id": toolUseID,
			"content":    content,
		},
	}))
	outputs = append(outputs, shared.SSEEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": state.BlockIndex,
	}))
	state.WebSearchToolResultIDs[toolUseID] = struct{}{}
	state.BlockIndex++
	if toolUseID == state.LastWebSearchToolUseID {
		state.LastWebSearchToolUseID = ""
	}
	return outputs
}

func webSearchToolUseID(state *responsesToClaudeStreamState, root, item gjson.Result) string {
	for _, path := range []string{"id", "output_item_id", "call_id"} {
		if value := strings.TrimSpace(item.Get(path).String()); value != "" {
			return value
		}
		if value := strings.TrimSpace(root.Get(path).String()); value != "" {
			return value
		}
	}
	if state.LastWebSearchToolUseID != "" {
		return state.LastWebSearchToolUseID
	}
	for _, path := range []string{"item_id"} {
		if value := strings.TrimSpace(item.Get(path).String()); value != "" {
			return value
		}
		if value := strings.TrimSpace(root.Get(path).String()); value != "" {
			return value
		}
	}
	id := fmt.Sprintf("web_search_%d", state.BlockIndex)
	state.LastWebSearchToolUseID = id
	return id
}

func webSearchQuery(root, item gjson.Result) string {
	for _, path := range []string{"action.query", "query", "input.query"} {
		if value := strings.TrimSpace(item.Get(path).String()); value != "" {
			return value
		}
		if value := strings.TrimSpace(root.Get(path).String()); value != "" {
			return value
		}
	}
	return ""
}

func webSearchResultContent(root, item gjson.Result) []byte {
	results := item.Get("results")
	if !results.IsArray() {
		results = root.Get("results")
	}
	if !results.IsArray() {
		return nil
	}
	content := []byte(`[]`)
	for _, result := range results.Array() {
		url := strings.TrimSpace(result.Get("url").String())
		if url == "" {
			continue
		}
		block := []byte(`{"type":"web_search_result","title":"","url":"","page_age":null}`)
		block, _ = sjson.SetBytes(block, "url", url)
		title := strings.TrimSpace(result.Get("title").String())
		if title == "" {
			title = url
		}
		block, _ = sjson.SetBytes(block, "title", title)
		content, _ = sjson.SetRawBytes(content, "-1", block)
	}
	return content
}

// appendWebSearchNonStreamContent 非流式 content 追加 web_search 块。
func appendWebSearchNonStreamContent(content []any, item gjson.Result, seen map[string]struct{}) []any {
	id := strings.TrimSpace(item.Get("id").String())
	if id == "" {
		return content
	}
	if seen == nil {
		return content
	}
	if _, ok := seen[id]; ok {
		return content
	}
	empty := gjson.Result{}
	query := webSearchQuery(empty, item)
	resultContent := webSearchResultContent(empty, item)
	if query == "" && len(resultContent) == 0 {
		return content
	}

	input := map[string]any{}
	if query != "" {
		input["query"] = query
	}
	content = append(content, map[string]any{
		"type":  "server_tool_use",
		"id":    id,
		"name":  "web_search",
		"input": input,
	})

	results := []any{}
	if len(resultContent) > 0 {
		_ = json.Unmarshal(resultContent, &results)
	}
	content = append(content, map[string]any{
		"type":        "web_search_tool_result",
		"tool_use_id": id,
		"content":     results,
	})
	seen[id] = struct{}{}
	return content
}
