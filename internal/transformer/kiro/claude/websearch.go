package claude

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"clisimplehub/internal/transformer/shared"

	"github.com/google/uuid"
)

const webSearchQueryPrefix = "Perform a web search for the query: "

type WebSearchResults struct {
	Results      []WebSearchResult `json:"results"`
	TotalResults *int              `json:"totalResults,omitempty"`
	Query        *string           `json:"query,omitempty"`
	Error        *string           `json:"error,omitempty"`
}

type WebSearchResult struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Snippet *string `json:"snippet,omitempty"`
}

type mcpResponse struct {
	Error  *mcpError  `json:"error,omitempty"`
	Result *mcpResult `json:"result,omitempty"`
}

type mcpError struct {
	Code    *int    `json:"code,omitempty"`
	Message *string `json:"message,omitempty"`
}

type mcpResult struct {
	Content []mcpContent `json:"content,omitempty"`
	IsError bool         `json:"isError,omitempty"`
}

type mcpContent struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}

func ParseClaudeWebSearchOnlyRequest(body []byte) (model string, query string, ok bool) {
	if len(body) == 0 {
		return "", "", false
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return "", "", false
	}

	rawTools, _ := req["tools"].([]any)
	if len(rawTools) != 1 {
		return "", "", false
	}
	tool0, _ := rawTools[0].(map[string]any)
	if tool0 == nil {
		return "", "", false
	}

	rawMessages, _ := req["messages"].([]any)
	if len(rawMessages) == 0 {
		return "", "", false
	}
	msg0, _ := rawMessages[0].(map[string]any)
	if msg0 == nil {
		return "", "", false
	}

	text := extractFirstMessageText(msg0)
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, webSearchQueryPrefix) {
		text = strings.TrimSpace(text[len(webSearchQueryPrefix):])
	}
	if text == "" {
		return "", "", false
	}

	model = strings.TrimSpace(shared.StringFromAny(req["model"]))
	return model, text, true
}

func extractFirstMessageText(msg map[string]any) string {
	if msg == nil {
		return ""
	}
	content := msg["content"]
	switch c := content.(type) {
	case string:
		return c
	case []any:
		first := c
		if len(first) == 0 {
			return ""
		}
		block, _ := first[0].(map[string]any)
		if block == nil {
			return ""
		}
		if strings.TrimSpace(shared.StringFromAny(block["type"])) != "text" {
			return ""
		}
		return shared.StringFromAny(block["text"])
	default:
		return ""
	}
}

func ParseKiroMcpWebSearchResults(body []byte) (*WebSearchResults, error) {
	var resp mcpResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		code := -1
		if resp.Error.Code != nil {
			code = *resp.Error.Code
		}
		msg := ""
		if resp.Error.Message != nil {
			msg = *resp.Error.Message
		}
		return nil, fmt.Errorf("mcp error: code=%d message=%s", code, msg)
	}
	if resp.Result == nil || resp.Result.IsError {
		return nil, fmt.Errorf("mcp result error")
	}
	if len(resp.Result.Content) == 0 || strings.TrimSpace(resp.Result.Content[0].Text) == "" {
		return nil, fmt.Errorf("mcp empty content")
	}

	var results WebSearchResults
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &results); err != nil {
		return nil, err
	}
	return &results, nil
}

func BuildWebSearchMcpRequest(query string) (toolUseID string, body []byte, err error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", nil, fmt.Errorf("empty query")
	}

	toolUseID = newWebSearchToolUseID()
	req := map[string]any{
		"id":      newWebSearchMcpRequestID(),
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"params": map[string]any{
			"name": "web_search",
			"arguments": map[string]any{
				"query": query,
			},
		},
	}

	body, err = shared.MarshalNoEscapeHTML(req)
	if err != nil {
		return "", nil, err
	}
	return toolUseID, body, nil
}

func BuildWebSearchSSEEvents(model string, query string, toolUseID string, results *WebSearchResults, inputTokens int) ([]string, int) {
	if inputTokens < 0 {
		inputTokens = 0
	}

	messageID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]

	events := make([]string, 0, 32)
	events = append(events, shared.SSEEvent("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":                inputTokens,
				"output_tokens":               0,
				"cache_creation_input_tokens": 0,
				"cache_read_input_tokens":     0,
			},
		},
	}))

	events = append(events, shared.SSEEvent("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]any{
			"id":    toolUseID,
			"type":  "server_tool_use",
			"name":  "web_search",
			"input": map[string]any{},
		},
	}))

	inputJSON, _ := shared.MarshalNoEscapeHTML(map[string]any{"query": query})
	events = append(events, shared.SSEEvent("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{
			"type":         "input_json_delta",
			"partial_json": string(inputJSON),
		},
	}))

	events = append(events, shared.SSEEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": 0,
	}))

	events = append(events, shared.SSEEvent("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": 1,
		"content_block": map[string]any{
			"type":        "web_search_tool_result",
			"tool_use_id": toolUseID,
			"content":     buildWebSearchToolResultContent(results),
		},
	}))
	events = append(events, shared.SSEEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": 1,
	}))

	events = append(events, shared.SSEEvent("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": 2,
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	}))

	summary := buildWebSearchSummary(query, results)
	for _, chunk := range chunkStringByRunes(summary, 100) {
		events = append(events, shared.SSEEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": 2,
			"delta": map[string]any{
				"type": "text_delta",
				"text": chunk,
			},
		}))
	}
	events = append(events, shared.SSEEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": 2,
	}))

	outputTokens := (len(summary) + 3) / 4
	events = append(events, shared.SSEEvent("message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"output_tokens": outputTokens,
		},
	}))
	events = append(events, shared.SSEEvent("message_stop", map[string]any{
		"type": "message_stop",
	}))

	return events, outputTokens
}

func BuildWebSearchNonStreamMessage(model string, query string, toolUseID string, results *WebSearchResults, inputTokens int) ([]byte, int, error) {
	if inputTokens < 0 {
		inputTokens = 0
	}

	messageID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	summary := buildWebSearchSummary(query, results)
	outputTokens := (len(summary) + 3) / 4

	payload := map[string]any{
		"id":    messageID,
		"type":  "message",
		"role":  "assistant",
		"model": model,
		"content": []any{
			map[string]any{
				"id":    toolUseID,
				"type":  "server_tool_use",
				"name":  "web_search",
				"input": map[string]any{"query": query},
			},
			map[string]any{
				"type":        "web_search_tool_result",
				"tool_use_id": toolUseID,
				"content":     buildWebSearchToolResultContent(results),
			},
			map[string]any{
				"type": "text",
				"text": summary,
			},
		},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":                inputTokens,
			"output_tokens":               outputTokens,
			"cache_creation_input_tokens": 0,
			"cache_read_input_tokens":     0,
		},
	}

	b, err := shared.MarshalNoEscapeHTML(payload)
	if err != nil {
		return nil, 0, err
	}
	return b, outputTokens, nil
}

func buildWebSearchToolResultContent(results *WebSearchResults) []any {
	if results == nil || len(results.Results) == 0 {
		return []any{}
	}
	out := make([]any, 0, len(results.Results))
	for _, r := range results.Results {
		snippet := ""
		if r.Snippet != nil {
			snippet = *r.Snippet
		}
		out = append(out, map[string]any{
			"type":              "web_search_result",
			"title":             r.Title,
			"url":               r.URL,
			"encrypted_content": snippet,
			"page_age":          nil,
		})
	}
	return out
}

func buildWebSearchSummary(query string, results *WebSearchResults) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Here are the search results for %q:\n\n", query))

	if results != nil {
		for i, r := range results.Results {
			b.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, r.Title))
			if r.Snippet != nil && strings.TrimSpace(*r.Snippet) != "" {
				snippet := *r.Snippet
				if len(snippet) > 200 {
					snippet = snippet[:200] + "..."
				}
				b.WriteString("   " + snippet + "\n")
			}
			b.WriteString(fmt.Sprintf("   Source: %s\n\n", r.URL))
		}
	} else {
		b.WriteString("No results found.\n")
	}

	b.WriteString("\nPlease note that these are web search results and may not be fully accurate or up-to-date.")
	return b.String()
}

func chunkStringByRunes(s string, size int) []string {
	if size <= 0 || s == "" {
		if s == "" {
			return nil
		}
		return []string{s}
	}
	r := []rune(s)
	if len(r) <= size {
		return []string{s}
	}
	var out []string
	for i := 0; i < len(r); i += size {
		j := i + size
		if j > len(r) {
			j = len(r)
		}
		out = append(out, string(r[i:j]))
	}
	return out
}

func newWebSearchToolUseID() string {
	u := strings.ReplaceAll(uuid.NewString(), "-", "")
	if len(u) > 32 {
		u = u[:32]
	}
	return "srvtoolu_" + u
}

func newWebSearchMcpRequestID() string {
	random22 := randomFromCharset(22, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789")
	random8 := randomFromCharset(8, "abcdefghijklmnopqrstuvwxyz0123456789")
	return fmt.Sprintf("web_search_tooluse_%s_%d_%s", random22, time.Now().UnixMilli(), random8)
}

func randomFromCharset(n int, charset string) string {
	if n <= 0 || charset == "" {
		return ""
	}
	b := make([]byte, n)
	_, _ = rand.Read(b)
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = charset[int(b[i])%len(charset)]
	}
	return string(out)
}
