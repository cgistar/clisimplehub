package converters

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const webSearchQueryPrefix = "Perform a web search for the query: "

// ============================================================================
// Types
// ============================================================================

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

// WebSearchDetection holds the result of detecting a web_search-only request.
type WebSearchDetection struct {
	Model string
	Query string
}

// ============================================================================
// Detection: is this a web_search-only request?
// ============================================================================

// DetectWebSearchRequest checks if a raw JSON request body is a web_search-only
// request. Works for both Anthropic and OpenAI formats.
//
// A request is considered web_search-only when:
//   - tools contains exactly 1 tool named "web_search"
//   - the first message has non-empty text content
//
// Returns nil if the request is not a web_search-only request.
func DetectWebSearchRequest(body []byte) *WebSearchDetection {
	if len(body) == 0 {
		return nil
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil
	}

	// Check tools: must be exactly 1 tool named "web_search"
	if !isSingleWebSearchTool(req) {
		return nil
	}

	// Extract query from first message
	rawMessages, _ := req["messages"].([]any)
	if len(rawMessages) == 0 {
		return nil
	}

	query := extractQueryFromFirstMessage(rawMessages)
	if query == "" {
		return nil
	}

	model, _ := req["model"].(string)
	return &WebSearchDetection{
		Model: strings.TrimSpace(model),
		Query: query,
	}
}

func isSingleWebSearchTool(req map[string]any) bool {
	rawTools, _ := req["tools"].([]any)
	if len(rawTools) != 1 {
		return false
	}
	tool0, _ := rawTools[0].(map[string]any)
	if tool0 == nil {
		return false
	}

	// Anthropic format: {"name": "web_search", ...}
	name, _ := tool0["name"].(string)
	if strings.TrimSpace(name) == "web_search" {
		return true
	}

	// OpenAI format: {"type": "function", "function": {"name": "web_search", ...}}
	fn, _ := tool0["function"].(map[string]any)
	if fn != nil {
		fnName, _ := fn["name"].(string)
		if strings.TrimSpace(fnName) == "web_search" {
			return true
		}
	}

	return false
}

func extractQueryFromFirstMessage(messages []any) string {
	// Skip system messages to find the first user/tool message
	for _, msgRaw := range messages {
		msg, ok := msgRaw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if role == "system" {
			continue
		}

		text := extractFirstTextFromMessage(msg)
		text = strings.TrimSpace(text)
		if strings.HasPrefix(text, webSearchQueryPrefix) {
			text = strings.TrimSpace(text[len(webSearchQueryPrefix):])
		}
		return text
	}
	return ""
}

func extractFirstTextFromMessage(msg map[string]any) string {
	content := msg["content"]
	if content == nil {
		return ""
	}

	switch c := content.(type) {
	case string:
		return c
	case []any:
		if len(c) == 0 {
			return ""
		}
		block, _ := c[0].(map[string]any)
		if block == nil {
			return ""
		}
		t, _ := block["type"].(string)
		if strings.TrimSpace(t) != "text" {
			return ""
		}
		text, _ := block["text"].(string)
		return text
	}
	return ""
}

// ============================================================================
// MCP Request Building
// ============================================================================

// BuildWebSearchMcpRequest constructs a JSON-RPC 2.0 request for the Kiro MCP
// web_search endpoint. Returns (toolUseID, requestBody, error).
func BuildWebSearchMcpRequest(query string) (string, []byte, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", nil, fmt.Errorf("empty query")
	}

	toolUseID := newWebSearchToolUseID()
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

	body, err := marshalNoEscapeHTML(req)
	if err != nil {
		return "", nil, err
	}
	return toolUseID, body, nil
}

// ============================================================================
// MCP Response Parsing
// ============================================================================

// ParseMcpWebSearchResults parses a JSON-RPC response from the Kiro MCP endpoint
// and extracts the web search results.
func ParseMcpWebSearchResults(body []byte) (*WebSearchResults, error) {
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

// ============================================================================
// Response Building (Anthropic / Claude SSE format)
// ============================================================================

// BuildWebSearchSSEEvents builds a complete Anthropic-compatible SSE event
// stream for a web search response.
// Returns (events, outputTokens).
func BuildWebSearchSSEEvents(model, query, toolUseID string, results *WebSearchResults, inputTokens int) ([]string, int) {
	if inputTokens < 0 {
		inputTokens = 0
	}

	messageID := newMessageID()
	events := make([]string, 0, 32)

	// message_start
	events = append(events, sseEvent("message_start", map[string]any{
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

	// Block 0: server_tool_use (web_search call)
	events = append(events, sseEvent("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]any{
			"id":    toolUseID,
			"type":  "server_tool_use",
			"name":  "web_search",
			"input": map[string]any{},
		},
	}))

	inputJSON, _ := marshalNoEscapeHTML(map[string]any{"query": query})
	events = append(events, sseEvent("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{
			"type":         "input_json_delta",
			"partial_json": string(inputJSON),
		},
	}))
	events = append(events, sseEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": 0,
	}))

	// Block 1: web_search_tool_result
	events = append(events, sseEvent("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": 1,
		"content_block": map[string]any{
			"type":        "web_search_tool_result",
			"tool_use_id": toolUseID,
			"content":     buildWebSearchToolResultContent(results),
		},
	}))
	events = append(events, sseEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": 1,
	}))

	// Block 2: text summary
	events = append(events, sseEvent("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": 2,
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	}))

	summary := buildWebSearchSummary(query, results)
	for _, chunk := range chunkStringByRunes(summary, 100) {
		events = append(events, sseEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": 2,
			"delta": map[string]any{
				"type": "text_delta",
				"text": chunk,
			},
		}))
	}
	events = append(events, sseEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": 2,
	}))

	outputTokens := (len(summary) + 3) / 4

	// message_delta + message_stop
	events = append(events, sseEvent("message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"output_tokens": outputTokens,
		},
	}))
	events = append(events, sseEvent("message_stop", map[string]any{
		"type": "message_stop",
	}))

	return events, outputTokens
}

// BuildWebSearchNonStreamMessage builds a complete Anthropic-compatible JSON
// response for a web search (non-streaming).
// Returns (body, outputTokens, error).
func BuildWebSearchNonStreamMessage(model, query, toolUseID string, results *WebSearchResults, inputTokens int) ([]byte, int, error) {
	if inputTokens < 0 {
		inputTokens = 0
	}

	messageID := newMessageID()
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

	b, err := marshalNoEscapeHTML(payload)
	if err != nil {
		return nil, 0, err
	}
	return b, outputTokens, nil
}

// ============================================================================
// Internal helpers
// ============================================================================

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

	if results != nil && len(results.Results) > 0 {
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
	if s == "" {
		return nil
	}
	if size <= 0 {
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

func newMessageID() string {
	return "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
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

// sseEvent formats a server-sent event.
func sseEvent(event string, data any) string {
	b, err := marshalNoEscapeHTML(data)
	if err != nil {
		b, _ = json.Marshal(data)
	}
	return "event: " + event + "\ndata: " + string(b) + "\n\n"
}
