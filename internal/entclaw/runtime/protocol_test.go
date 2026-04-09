package entclawruntime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func testToolRound(id, name, arguments, result string, isError bool) ToolRound {
	return ToolRound{
		Call: ToolCall{
			ID:        id,
			Name:      name,
			Arguments: json.RawMessage(arguments),
		},
		Result: ToolResult{
			Content: json.RawMessage(result),
			IsError: isError,
		},
	}
}

func testAssistantTurn(parts ...AssistantTurnPart) AssistantTurn {
	return AssistantTurn{Parts: append([]AssistantTurnPart(nil), parts...)}
}

func testAssistantCallPart(id, name, arguments string) AssistantTurnPart {
	return assistantToolCallPart(ToolCall{
		ID:        id,
		Name:      name,
		Arguments: json.RawMessage(arguments),
	})
}

func TestChatAdapterParsesToolCalls(t *testing.T) {
	adapter := adapterForFormat(FormatChatCompletions)
	raw := []byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"skill_list","arguments":"{}"}}]}}]}`)

	turn, err := adapter.ParseToolCalls(raw)
	if err != nil {
		t.Fatalf("ParseToolCalls: %v", err)
	}
	calls := turn.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1", len(calls))
	}
	finalText := turn.FinalText()
	if finalText != "" {
		t.Fatalf("finalText = %q, want empty", finalText)
	}
}

func TestChatAdapterParseToolCallsAggregatesFinalText(t *testing.T) {
	adapter := adapterForFormat(FormatChatCompletions)
	raw := []byte(`{"choices":[{"message":{"content":"first second third","tool_calls":[{"id":"call_1","type":"function","function":{"name":"skill_list","arguments":"{}"}}]}}]}`)

	turn, err := adapter.ParseToolCalls(raw)
	if err != nil {
		t.Fatalf("ParseToolCalls: %v", err)
	}
	calls := turn.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1", len(calls))
	}
	finalText := turn.FinalText()
	if finalText != "first second third" {
		t.Fatalf("finalText = %q, want aggregated content", finalText)
	}
}

func TestResponsesAdapterForcesStreamFlag(t *testing.T) {
	adapter := adapterForFormat(FormatResponses)

	body, err := adapter.WithStreamFlag([]byte(`{"model":"gpt-5.4","stream":false}`), true)
	if err != nil {
		t.Fatalf("WithStreamFlag: %v", err)
	}
	if string(body) != `{"model":"gpt-5.4","stream":true}` {
		t.Fatalf("body = %s", body)
	}
}

func TestResponsesAdapterParseToolCallsUnquotesStringifiedArguments(t *testing.T) {
	adapter := adapterForFormat(FormatResponses)
	raw := []byte(`{"output":[{"type":"function_call","call_id":"call_1","name":"skill_list","arguments":"{\"limit\":1,\"filters\":{\"active\":true}}"}]}`)

	turn, err := adapter.ParseToolCalls(raw)
	if err != nil {
		t.Fatalf("ParseToolCalls: %v", err)
	}
	calls := turn.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1", len(calls))
	}
	finalText := turn.FinalText()
	if finalText != "" {
		t.Fatalf("finalText = %q, want empty", finalText)
	}

	var arguments struct {
		Limit   int `json:"limit"`
		Filters struct {
			Active bool `json:"active"`
		} `json:"filters"`
	}
	if err := json.Unmarshal(calls[0].Arguments, &arguments); err != nil {
		t.Fatalf("json.Unmarshal(arguments): %v, raw=%s", err, calls[0].Arguments)
	}
	if arguments.Limit != 1 || !arguments.Filters.Active {
		t.Fatalf("arguments = %+v, want decoded JSON object", arguments)
	}
}

func TestResponsesAdapterParseToolCallsAggregatesFinalText(t *testing.T) {
	adapter := adapterForFormat(FormatResponses)
	raw := []byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"first "},{"type":"output_text","text":"second"},{"type":"output_text","text":" third"}]},{"type":"function_call","call_id":"call_1","name":"skill_list","arguments":"{}"}]}`)

	turn, err := adapter.ParseToolCalls(raw)
	if err != nil {
		t.Fatalf("ParseToolCalls: %v", err)
	}
	calls := turn.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1", len(calls))
	}
	finalText := turn.FinalText()
	if finalText != "first second third" {
		t.Fatalf("finalText = %q, want aggregated message text", finalText)
	}
}

func TestResponsesAdapterParseToolCallsFromStreamingEvents(t *testing.T) {
	adapter := adapterForFormat(FormatResponses)
	raw := []byte("event: response.output_item.added\n" +
		"data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"status\":\"in_progress\",\"call_id\":\"call_1\",\"name\":\"skill_list\",\"arguments\":\"\"}}\n\n" +
		"event: response.output_item.done\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"checking skills\"}]}}\n\n" +
		"event: response.output_item.done\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"status\":\"completed\",\"call_id\":\"call_1\",\"name\":\"skill_list\",\"arguments\":\"{}\"}}\n\n")

	turn, err := adapter.ParseToolCalls(raw)
	if err != nil {
		t.Fatalf("ParseToolCalls: %v", err)
	}
	if turn.FinalText() != "checking skills" {
		t.Fatalf("finalText = %q, want %q", turn.FinalText(), "checking skills")
	}
	calls := turn.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1", len(calls))
	}
	if calls[0].Name != "skill_list" {
		t.Fatalf("call name = %q, want %q", calls[0].Name, "skill_list")
	}
	if string(calls[0].Arguments) != "{}" {
		t.Fatalf("arguments = %s, want {}", calls[0].Arguments)
	}
}

func TestResponsesAdapterParseToolCallsCapturesResponseIDFromStreamingEvents(t *testing.T) {
	adapter := adapterForFormat(FormatResponses)
	raw := []byte("event: response.output_item.done\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"status\":\"completed\",\"call_id\":\"call_1\",\"name\":\"skill_list\",\"arguments\":\"{}\"}}\n\n" +
		"event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_123\",\"status\":\"completed\"}}\n\n")

	turn, err := adapter.ParseToolCalls(raw)
	if err != nil {
		t.Fatalf("ParseToolCalls: %v", err)
	}
	if turn.ResponseID != "resp_123" {
		t.Fatalf("turn.ResponseID = %q, want %q", turn.ResponseID, "resp_123")
	}
}

func TestMessagesAdapterAppendToolResultsAddsToolUseAndToolResultBlocks(t *testing.T) {
	adapter := adapterForFormat(FormatMessages)
	turn := testAssistantTurn(
		assistantTextPart("working "),
		testAssistantCallPart("call_1", "skill_list", `{"limit":1}`),
		testAssistantCallPart("call_2", "skill_run", `{"id":"job_1"}`),
	)
	rounds := []ToolRound{
		testToolRound("call_1", "skill_list", `{"limit":1}`, `{"ok":true}`, false),
		testToolRound("call_2", "skill_run", `{"id":"job_1"}`, `"failed"`, true),
	}

	body, err := adapter.AppendToolResults([]byte(`{"messages":[{"role":"user","content":"hello"}]}`), turn, rounds)
	if err != nil {
		t.Fatalf("AppendToolResults: %v", err)
	}

	root := gjson.ParseBytes(body)
	if root.Get("messages.1.role").String() != "assistant" {
		t.Fatalf("assistant role = %s", root.Get("messages.1").Raw)
	}
	if root.Get("messages.1.content.0.type").String() != "text" {
		t.Fatalf("assistant tool_use block = %s", root.Get("messages.1").Raw)
	}
	if root.Get("messages.1.content.0.text").String() != "working " {
		t.Fatalf("assistant text block = %s", root.Get("messages.1.content.0").Raw)
	}
	if root.Get("messages.1.content.1.type").String() != "tool_use" {
		t.Fatalf("assistant first tool_use block = %s", root.Get("messages.1").Raw)
	}
	if root.Get("messages.1.content.1.id").String() != "call_1" {
		t.Fatalf("assistant tool_use id = %s", root.Get("messages.1.content.0").Raw)
	}
	if root.Get("messages.1.content.1.name").String() != "skill_list" {
		t.Fatalf("assistant tool_use name = %s", root.Get("messages.1.content.0").Raw)
	}
	if root.Get("messages.1.content.1.input.limit").Int() != 1 {
		t.Fatalf("assistant tool_use input = %s", root.Get("messages.1.content.0.input").Raw)
	}
	if root.Get("messages.1.content.2.type").String() != "tool_use" {
		t.Fatalf("second assistant tool_use block = %s", root.Get("messages.1").Raw)
	}
	if root.Get("messages.1.content.2.id").String() != "call_2" {
		t.Fatalf("second assistant tool_use id = %s", root.Get("messages.1.content.1").Raw)
	}
	if root.Get("messages.1.content.2.name").String() != "skill_run" {
		t.Fatalf("second assistant tool_use name = %s", root.Get("messages.1.content.1").Raw)
	}
	if root.Get("messages.1.content.2.input.id").String() != "job_1" {
		t.Fatalf("second assistant tool_use input = %s", root.Get("messages.1.content.1.input").Raw)
	}
	if root.Get("messages.2.role").String() != "user" {
		t.Fatalf("tool result role = %s", root.Get("messages.2").Raw)
	}
	if root.Get("messages.2.content.0.type").String() != "tool_result" {
		t.Fatalf("tool result block = %s", root.Get("messages.2").Raw)
	}
	if root.Get("messages.2.content.0.tool_use_id").String() != "call_1" {
		t.Fatalf("tool result tool_use_id = %s", root.Get("messages.2.content.0").Raw)
	}
	if root.Get("messages.2.content.0.content").Type.String() != "String" {
		t.Fatalf("tool result content type = %s", root.Get("messages.2.content.0").Raw)
	}
	if root.Get("messages.2.content.0.content").String() != `{"ok":true}` {
		t.Fatalf("tool result content = %s", root.Get("messages.2.content.0").Raw)
	}
	if root.Get("messages.2.content.1.type").String() != "tool_result" {
		t.Fatalf("second tool result block = %s", root.Get("messages.2").Raw)
	}
	if root.Get("messages.2.content.1.tool_use_id").String() != "call_2" {
		t.Fatalf("second tool result tool_use_id = %s", root.Get("messages.2.content.1").Raw)
	}
	if root.Get("messages.2.content.1.content").String() != "failed" {
		t.Fatalf("second tool result content = %s", root.Get("messages.2.content.1").Raw)
	}
	if !root.Get("messages.2.content.1.is_error").Bool() {
		t.Fatalf("second tool result is_error = %s", root.Get("messages.2.content.1").Raw)
	}
}

func TestMessagesAdapterParseToolCallsAggregatesFinalText(t *testing.T) {
	adapter := adapterForFormat(FormatMessages)
	raw := []byte(`{"content":[{"type":"text","text":"first "},{"type":"tool_use","id":"call_1","name":"skill_list","input":{"limit":1}},{"type":"text","text":"second"},{"type":"text","text":" third"}]}`)

	turn, err := adapter.ParseToolCalls(raw)
	if err != nil {
		t.Fatalf("ParseToolCalls: %v", err)
	}
	calls := turn.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1", len(calls))
	}
	finalText := turn.FinalText()
	if finalText != "first second third" {
		t.Fatalf("finalText = %q, want aggregated text blocks", finalText)
	}
}

func TestChatAdapterAppendToolResultsAddsAssistantAndToolMessages(t *testing.T) {
	adapter := adapterForFormat(FormatChatCompletions)
	turn := testAssistantTurn(
		assistantTextPart("working "),
		testAssistantCallPart("call_1", "skill_list", `{"limit":1}`),
		testAssistantCallPart("call_2", "skill_run", `{"id":"job_1"}`),
	)
	rounds := []ToolRound{
		testToolRound("call_1", "skill_list", `{"limit":1}`, `"done"`, false),
		testToolRound("call_2", "skill_run", `{"id":"job_1"}`, `{"ok":true}`, false),
	}

	body, err := adapter.AppendToolResults([]byte(`{"messages":[{"role":"user","content":"hello"}]}`), turn, rounds)
	if err != nil {
		t.Fatalf("AppendToolResults: %v", err)
	}

	root := gjson.ParseBytes(body)
	if root.Get("messages.1.role").String() != "assistant" {
		t.Fatalf("assistant role = %s", root.Get("messages.1").Raw)
	}
	if root.Get("messages.1.content").String() != "working " {
		t.Fatalf("assistant content = %s", root.Get("messages.1").Raw)
	}
	if root.Get("messages.1.tool_calls.0.id").String() != "call_1" {
		t.Fatalf("assistant tool call id = %s", root.Get("messages.1").Raw)
	}
	if root.Get("messages.1.tool_calls.0.function.name").String() != "skill_list" {
		t.Fatalf("assistant tool call name = %s", root.Get("messages.1").Raw)
	}
	if root.Get("messages.1.tool_calls.0.function.arguments").String() != `{"limit":1}` {
		t.Fatalf("assistant tool call args = %s", root.Get("messages.1").Raw)
	}
	if root.Get("messages.1.tool_calls.1.id").String() != "call_2" {
		t.Fatalf("second assistant tool call id = %s", root.Get("messages.1").Raw)
	}
	if root.Get("messages.1.tool_calls.1.function.name").String() != "skill_run" {
		t.Fatalf("second assistant tool call name = %s", root.Get("messages.1").Raw)
	}
	if root.Get("messages.1.tool_calls.1.function.arguments").String() != `{"id":"job_1"}` {
		t.Fatalf("second assistant tool call args = %s", root.Get("messages.1").Raw)
	}
	if root.Get("messages.2.role").String() != "tool" {
		t.Fatalf("tool role = %s", root.Get("messages.2").Raw)
	}
	if root.Get("messages.2.tool_call_id").String() != "call_1" {
		t.Fatalf("tool call id = %s", root.Get("messages.2").Raw)
	}
	if root.Get("messages.2.content").String() != "done" {
		t.Fatalf("tool content = %s", root.Get("messages.2").Raw)
	}
	if root.Get("messages.3.role").String() != "tool" {
		t.Fatalf("second tool role = %s", root.Get("messages.3").Raw)
	}
	if root.Get("messages.3.tool_call_id").String() != "call_2" {
		t.Fatalf("second tool call id = %s", root.Get("messages.3").Raw)
	}
	if root.Get("messages.3.content").Type.String() != "String" {
		t.Fatalf("second tool content type = %s", root.Get("messages.3").Raw)
	}
	if root.Get("messages.3.content").String() != `{"ok":true}` {
		t.Fatalf("second tool content = %s", root.Get("messages.3").Raw)
	}
}

func TestMessagesAdapterRoundTripsAssistantTextAndToolUseWhenAppendingResults(t *testing.T) {
	adapter := adapterForFormat(FormatMessages)
	assistantRaw := []byte(`{"content":[{"type":"text","text":"plan: "},{"type":"tool_use","id":"call_1","name":"skill_list","input":{"limit":1}},{"type":"text","text":" after"}]}`)

	turn, err := adapter.ParseToolCalls(assistantRaw)
	if err != nil {
		t.Fatalf("ParseToolCalls: %v", err)
	}
	body, err := adapter.AppendToolResults([]byte(`{"messages":[{"role":"user","content":"hello"}]}`), turn, []ToolRound{
		testToolRound("call_1", "skill_list", `{"limit":1}`, `{"ok":true}`, false),
	})
	if err != nil {
		t.Fatalf("AppendToolResults: %v", err)
	}

	root := gjson.ParseBytes(body)
	if root.Get("messages.1.role").String() != "assistant" {
		t.Fatalf("assistant role = %s", root.Get("messages.1").Raw)
	}
	if root.Get("messages.1.content.0.type").String() != "text" || root.Get("messages.1.content.0.text").String() != "plan: " {
		t.Fatalf("assistant first content block = %s", root.Get("messages.1").Raw)
	}
	if root.Get("messages.1.content.1.type").String() != "tool_use" || root.Get("messages.1.content.1.id").String() != "call_1" {
		t.Fatalf("assistant tool_use block = %s", root.Get("messages.1").Raw)
	}
	if root.Get("messages.1.content.2.type").String() != "text" || root.Get("messages.1.content.2.text").String() != " after" {
		t.Fatalf("assistant trailing text block = %s", root.Get("messages.1").Raw)
	}
	if root.Get("messages.2.content.0.content").String() != `{"ok":true}` {
		t.Fatalf("tool result content = %s", root.Get("messages.2").Raw)
	}
}

func TestResponsesAdapterAppendToolResultsReplaysInputWithoutAssistantText(t *testing.T) {
	adapter := adapterForFormat(FormatResponses)
	turn, err := adapter.ParseToolCalls([]byte(`{"id":"resp_123","output":[{"type":"message","content":[{"type":"output_text","text":"working "}]}]}`))
	if err != nil {
		t.Fatalf("ParseToolCalls: %v", err)
	}
	turn.Parts = []AssistantTurnPart{
		testAssistantCallPart("call_1", "skill_list", `{"limit":1}`),
		testAssistantCallPart("call_2", "skill_run", `{"id":"job_1"}`),
	}
	rounds := []ToolRound{
		testToolRound("call_1", "skill_list", `{"limit":1}`, `{"ok":true}`, false),
		testToolRound("call_2", "skill_run", `{"id":"job_1"}`, `"done"`, false),
	}

	body, err := adapter.AppendToolResults([]byte(`{"model":"gpt-5.4","input":"hello"}`), turn, rounds)
	if err != nil {
		t.Fatalf("AppendToolResults: %v", err)
	}

	root := gjson.ParseBytes(body)
	if root.Get("previous_response_id").Exists() {
		t.Fatalf("previous_response_id should not be sent for replay continuation: %s", root.Raw)
	}
	if root.Get("input.#").Int() != 5 {
		t.Fatalf("input length = %d, want original user item + function calls + outputs", root.Get("input.#").Int())
	}
	if root.Get("input.0.type").String() != "message" {
		t.Fatalf("first input type = %s", root.Get("input.0").Raw)
	}
	if root.Get("input.0.content").String() != "hello" {
		t.Fatalf("original user input should be preserved: %s", root.Get("input.0").Raw)
	}
	if root.Get("input.1.type").String() != "function_call" {
		t.Fatalf("first replayed function_call = %s", root.Get("input.1").Raw)
	}
	if root.Get("input.1.call_id").String() != "call_1" {
		t.Fatalf("first replayed call_id = %s", root.Get("input.1").Raw)
	}
	if root.Get("input.2.type").String() != "function_call" {
		t.Fatalf("second replayed function_call = %s", root.Get("input.2").Raw)
	}
	if root.Get("input.3.type").String() != "function_call_output" {
		t.Fatalf("first output body = %s", root.Get("input.3").Raw)
	}
	if root.Get("input.3.output").String() != `{"ok":true}` {
		t.Fatalf("first output payload = %s", root.Get("input.3").Raw)
	}
	if root.Get("input.4.type").String() != "function_call_output" {
		t.Fatalf("second output body = %s", root.Get("input.4").Raw)
	}
	if root.Get("input.4.output").String() != "done" {
		t.Fatalf("second output payload = %s", root.Get("input.4").Raw)
	}
}

func TestResponsesAdapterAppendToolResultsStringifiesStructuredOutput(t *testing.T) {
	adapter := adapterForFormat(FormatResponses)
	rounds := []ToolRound{
		testToolRound("call_1", "skill_list", `{"limit":1}`, `{"ok":true,"items":[1,2]}`, false),
	}

	body, err := adapter.AppendToolResults([]byte(`{"model":"gpt-5.4","input":[]}`), testAssistantTurn(testAssistantCallPart("call_1", "skill_list", `{"limit":1}`)), rounds)
	if err != nil {
		t.Fatalf("AppendToolResults: %v", err)
	}

	root := gjson.ParseBytes(body)
	if root.Get("input.0.type").String() != "function_call" {
		t.Fatalf("replayed function call = %s", root.Get("input").Raw)
	}
	if root.Get("input.1.type").String() != "function_call_output" {
		t.Fatalf("function call output = %s", root.Get("input").Raw)
	}
	if root.Get("input.1.output").Type.String() != "String" {
		t.Fatalf("function call output type = %s", root.Get("input.1").Raw)
	}
	if root.Get("input.1.output").String() != `{"ok":true,"items":[1,2]}` {
		t.Fatalf("function call output content = %s", root.Get("input.1").Raw)
	}
}

func TestResponsesAdapterAppendToolResultsDoesNotReplayPriorMessages(t *testing.T) {
	adapter := adapterForFormat(FormatResponses)
	turn := testAssistantTurn(testAssistantCallPart("call_1", "skill_list", `{"limit":1}`))
	rounds := []ToolRound{
		testToolRound("call_1", "skill_list", `{"limit":1}`, `"done"`, false),
	}

	body, err := adapter.AppendToolResults([]byte(`{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":"hello"},{"type":"message","role":"assistant","content":"working"}]}`), turn, rounds)
	if err != nil {
		t.Fatalf("AppendToolResults: %v", err)
	}

	root := gjson.ParseBytes(body)
	if root.Get("input.#").Int() != 4 {
		t.Fatalf("input length = %d, want original messages + function call + output", root.Get("input.#").Int())
	}
	if root.Get("input.0.type").String() != "message" || root.Get("input.0.content").String() != "hello" {
		t.Fatalf("original user message = %s", root.Get("input.0").Raw)
	}
	if root.Get("input.1.type").String() != "message" || root.Get("input.1.content").String() != "working" {
		t.Fatalf("original assistant message = %s", root.Get("input.1").Raw)
	}
	if root.Get("input.2.type").String() != "function_call" {
		t.Fatalf("replayed function call = %s", root.Get("input.2").Raw)
	}
	if root.Get("input.3.type").String() != "function_call_output" {
		t.Fatalf("function call output = %s", root.Get("input").Raw)
	}
}

func TestResponsesAdapterParseToolCallsRejectsUnexpectedJSONPayload(t *testing.T) {
	adapter := adapterForFormat(FormatResponses)

	_, err := adapter.ParseToolCalls([]byte(`{"error":"schema drift"}`))
	if err == nil {
		t.Fatal("ParseToolCalls error = nil, want unexpected responses payload")
	}
	if !strings.Contains(err.Error(), "unexpected responses payload") {
		t.Fatalf("err = %v, want unexpected responses payload", err)
	}
}

func TestEncodeResponsesProgressStreamIncludesToolCallAndOutput(t *testing.T) {
	events := []OrchestrationEvent{
		NewAssistantToolCallEvent("call_1", "skill_run", json.RawMessage(`{"name":"demo","script":"run.sh"}`)),
		NewToolStartedEvent("call_1"),
		NewToolCompletedEvent("call_1", json.RawMessage(`{"stdout":"done","stderr":"","exitCode":0}`), false),
		NewCompletionEvent(),
	}

	body := BuildResponsesProgressStream("resp_entclaw", events)
	if !strings.Contains(body, "\"type\":\"function_call\"") {
		t.Fatalf("body = %s, want function_call item", body)
	}
	if !strings.Contains(body, "\"type\":\"function_call_output\"") {
		t.Fatalf("body = %s, want function_call_output item", body)
	}
	if !strings.Contains(body, "\"type\":\"response.completed\"") {
		t.Fatalf("body = %s, want response.completed event", body)
	}
}

func TestEncodeResponsesProgressStreamAssociatesFunctionOutputCompletionByCallID(t *testing.T) {
	events := []OrchestrationEvent{
		NewAssistantToolCallEvent("call_1", "skill_list", json.RawMessage(`{}`)),
		NewAssistantToolCallEvent("call_2", "skill_read", json.RawMessage(`{"name":"demo"}`)),
		NewToolStartedEvent("call_1"),
		NewToolStartedEvent("call_2"),
		NewToolCompletedEvent("call_1", json.RawMessage(`"first"`), false),
		NewToolCompletedEvent("call_2", json.RawMessage(`"second"`), false),
		NewCompletionEvent(),
	}

	eventsByChunk := parseResponsesSSEForTest(t, BuildResponsesProgressStream("resp_entclaw", events))
	startIndexes := make(map[string]int)
	doneIndexes := make(map[string]int)

	for _, event := range eventsByChunk {
		if event.data.Get("item.type").String() != "function_call_output" {
			continue
		}

		callID := event.data.Get("item.call_id").String()
		switch event.event {
		case "response.output_item.added":
			startIndexes[callID] = int(event.data.Get("output_index").Int())
		case "response.output_item.done":
			doneIndexes[callID] = int(event.data.Get("output_index").Int())
		}
	}

	if startIndexes["call_1"] != 2 {
		t.Fatalf("startIndexes[call_1] = %d, want 2", startIndexes["call_1"])
	}
	if startIndexes["call_2"] != 3 {
		t.Fatalf("startIndexes[call_2] = %d, want 3", startIndexes["call_2"])
	}
	if doneIndexes["call_1"] != startIndexes["call_1"] {
		t.Fatalf("doneIndexes[call_1] = %d, want %d", doneIndexes["call_1"], startIndexes["call_1"])
	}
	if doneIndexes["call_2"] != startIndexes["call_2"] {
		t.Fatalf("doneIndexes[call_2] = %d, want %d", doneIndexes["call_2"], startIndexes["call_2"])
	}
}

func TestEncodeResponsesProgressStreamClosesPendingToolOutputOnFailure(t *testing.T) {
	events := []OrchestrationEvent{
		NewAssistantToolCallEvent("call_1", "skill_read", json.RawMessage(`[]`)),
		NewToolStartedEvent("call_1"),
		NewFailureEvent("call_1", testResponsesProgressError(`execute tool "skill_read": json: cannot unmarshal array into Go value of type struct { Name string "json:\"name\"" }`)),
	}

	eventsByChunk := parseResponsesSSEForTest(t, BuildResponsesProgressStream("resp_entclaw", events))
	var outputDone gjson.Result
	var failedResponse gjson.Result

	for _, event := range eventsByChunk {
		if event.event == "response.output_item.done" && event.data.Get("item.type").String() == "function_call_output" {
			outputDone = event.data
		}
		if event.event == "response.failed" {
			failedResponse = event.data
		}
		if event.event == "response.completed" {
			t.Fatalf("unexpected response.completed event in failure stream: %s", event.data.Raw)
		}
	}

	if !outputDone.Exists() {
		t.Fatalf("missing function_call_output done event: %+v", eventsByChunk)
	}
	if outputDone.Get("output_index").Int() != 1 {
		t.Fatalf("output_index = %d, want 1", outputDone.Get("output_index").Int())
	}
	if outputDone.Get("item.call_id").String() != "call_1" {
		t.Fatalf("call_id = %q, want call_1", outputDone.Get("item.call_id").String())
	}
	if outputDone.Get("item.status").String() != "failed" {
		t.Fatalf("item.status = %q, want failed", outputDone.Get("item.status").String())
	}
	if !outputDone.Get("item.is_error").Bool() {
		t.Fatalf("item.is_error = %s, want true", outputDone.Get("item.is_error").Raw)
	}
	outputPayload := gjson.Parse(outputDone.Get("item.output").String())
	if !strings.Contains(outputPayload.Get("error").String(), `execute tool "skill_read"`) {
		t.Fatalf("item.output = %q, want hard execution error", outputDone.Get("item.output").String())
	}
	if !failedResponse.Exists() {
		t.Fatalf("missing response.failed event: %+v", eventsByChunk)
	}
	if failedResponse.Get("response.status").String() != "failed" {
		t.Fatalf("response.status = %q, want failed", failedResponse.Get("response.status").String())
	}
}

func TestEncodeResponsesProgressStreamDoesNotInventToolOutputForResponseLevelFailure(t *testing.T) {
	events := []OrchestrationEvent{
		NewFailureEvent("", testResponsesProgressError("probe failed")),
	}

	eventsByChunk := parseResponsesSSEForTest(t, BuildResponsesProgressStream("resp_entclaw", events))
	for _, event := range eventsByChunk {
		if event.data.Get("item.type").String() == "function_call_output" {
			t.Fatalf("unexpected synthetic function_call_output for response-level failure: %s", event.data.Raw)
		}
	}

	var failedResponse gjson.Result
	for _, event := range eventsByChunk {
		if event.event == "response.failed" {
			failedResponse = event.data
			break
		}
	}
	if !failedResponse.Exists() {
		t.Fatalf("missing response.failed event: %+v", eventsByChunk)
	}
	if failedResponse.Get("response.status").String() != "failed" {
		t.Fatalf("response.status = %q, want failed", failedResponse.Get("response.status").String())
	}
	if failedResponse.Get("error.message").String() != "probe failed" {
		t.Fatalf("error.message = %q, want probe failed", failedResponse.Get("error.message").String())
	}
}

func TestBuildChatProgressStreamIncludesSimplifiedProgressText(t *testing.T) {
	stream := BuildChatProgressStream([]OrchestrationEvent{
		NewAssistantToolCallEvent("call_1", "skill_read", json.RawMessage(`{"name":"demo"}`)),
		NewToolCompletedEvent("call_1", json.RawMessage(`{"content":"done"}`), false),
	}, []byte("data: final\n\n"))

	if !strings.Contains(stream, "Reading skill instructions") {
		t.Fatalf("stream = %s, want skill_read progress text", stream)
	}
	if !strings.Contains(stream, "Tool finished.") {
		t.Fatalf("stream = %s, want tool completion text", stream)
	}
	if !strings.Contains(stream, "data: final") {
		t.Fatalf("stream = %s, want final body", stream)
	}
}

func TestBuildMessagesProgressStreamIncludesSimplifiedProgressText(t *testing.T) {
	stream := BuildMessagesProgressStream([]OrchestrationEvent{
		NewAssistantToolCallEvent("call_1", "skill_run", json.RawMessage(`{"name":"demo","script":"run.sh"}`)),
	}, []byte("event: done\n\n"))

	if !strings.Contains(stream, "Running skill script") {
		t.Fatalf("stream = %s, want skill_run progress text", stream)
	}
	if !strings.Contains(stream, "event: content_block_delta") {
		t.Fatalf("stream = %s, want Anthropic-style delta event", stream)
	}
	if !strings.Contains(stream, "event: done") {
		t.Fatalf("stream = %s, want final body", stream)
	}
}

type testResponsesSSEEvent struct {
	event string
	data  gjson.Result
}

func parseResponsesSSEForTest(t *testing.T, body string) []testResponsesSSEEvent {
	t.Helper()

	chunks := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n")
	events := make([]testResponsesSSEEvent, 0, len(chunks))
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}

		var eventName string
		var dataLines []string
		for _, line := range strings.Split(chunk, "\n") {
			switch {
			case strings.HasPrefix(line, "event:"):
				eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if eventName == "" {
			t.Fatalf("missing event name in chunk %q", chunk)
		}
		raw := strings.Join(dataLines, "\n")
		if !gjson.Valid(raw) {
			t.Fatalf("invalid SSE json payload %q", raw)
		}
		events = append(events, testResponsesSSEEvent{
			event: eventName,
			data:  gjson.Parse(raw),
		})
	}

	return events
}

func testResponsesProgressError(message string) error {
	return testResponsesProgressFailure(message)
}

type testResponsesProgressFailure string

func (e testResponsesProgressFailure) Error() string {
	return string(e)
}
