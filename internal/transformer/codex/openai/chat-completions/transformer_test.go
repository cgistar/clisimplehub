package chat_completions

import (
	"context"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestTransformRequest_ThirdPartyParity(t *testing.T) {
	tr := Transformer{}
	raw := []byte(`{
		"instructions":"be concise",
		"max_output_tokens":128,
		"parallel_tool_calls":true,
		"reasoning":{"effort":"HIGH"},
		"tool_choice":"auto",
		"input":[
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"internal note"}]},
			{"type":"message","role":"user","content":[
				{"type":"input_text","text":"hello"},
				{"type":"input_image","image_url":"https://example.com/image.png"}
			]},
			{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"q\":\"x\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"done"}
		],
		"tools":[
			{"type":"function","name":"lookup","description":"Look up data","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}
		]
	}`)

	got, err := tr.TransformRequest("gpt-4.1", raw, true)
	if err != nil {
		t.Fatalf("TransformRequest() error = %v", err)
	}

	root := gjson.ParseBytes(got)
	if root.Get("model").String() != "gpt-4.1" {
		t.Fatalf("model = %q", root.Get("model").String())
	}
	if !root.Get("stream").Bool() {
		t.Fatalf("stream = false")
	}
	if root.Get("max_tokens").Int() != 128 {
		t.Fatalf("max_tokens = %d", root.Get("max_tokens").Int())
	}
	if root.Get("reasoning_effort").String() != "high" {
		t.Fatalf("reasoning_effort = %q", root.Get("reasoning_effort").String())
	}
	if root.Get("messages.0.role").String() != "system" || root.Get("messages.0.content").String() != "be concise" {
		t.Fatalf("system message mismatch: %s", root.Get("messages.0").Raw)
	}
	if root.Get("messages.1.role").String() != "user" {
		t.Fatalf("developer role not downgraded: %s", root.Get("messages.1").Raw)
	}
	if root.Get("messages.1.content.0.type").String() != "text" {
		t.Fatalf("developer content not preserved as array: %s", root.Get("messages.1").Raw)
	}
	if root.Get("messages.2.content.1.image_url.url").String() != "https://example.com/image.png" {
		t.Fatalf("image content mismatch: %s", root.Get("messages.2").Raw)
	}
	if root.Get("messages.3.tool_calls.0.function.name").String() != "lookup" {
		t.Fatalf("tool call missing: %s", root.Get("messages.3").Raw)
	}
	if root.Get("messages.4.role").String() != "tool" || root.Get("messages.4.tool_call_id").String() != "call_1" {
		t.Fatalf("tool output mismatch: %s", root.Get("messages.4").Raw)
	}
	if root.Get("tools.0.function.parameters.properties.q.type").String() != "string" {
		t.Fatalf("tools mismatch: %s", root.Get("tools.0").Raw)
	}
}

func TestTransformRequest_ToolParametersDefaultToEmptyObject(t *testing.T) {
	tr := Transformer{}
	raw := []byte(`{
		"model":"gpt-4.1",
		"input":[{"type":"message","role":"user","content":"hello"}],
		"tools":[
			{"type":"function","name":"lookup","description":"Look up data"}
		]
	}`)

	got, err := tr.TransformRequest("gpt-4.1", raw, false)
	if err != nil {
		t.Fatalf("TransformRequest() error = %v", err)
	}

	root := gjson.ParseBytes(got)
	if !root.Get("tools.0.function.parameters").Exists() {
		t.Fatalf("parameters missing: %s", root.Get("tools.0").Raw)
	}
	if root.Get("tools.0.function.parameters").Raw != "{}" {
		t.Fatalf("parameters = %s, want {}", root.Get("tools.0.function.parameters").Raw)
	}
}

func TestTransformResponseStream_HandlesReasoningAndTools(t *testing.T) {
	tr := Transformer{}
	originalRequest := []byte(`{
		"model":"gpt-4.1",
		"instructions":"be concise",
		"max_output_tokens":256,
		"reasoning":{"effort":"medium"},
		"parallel_tool_calls":true
	}`)
	requestRaw := []byte(`{"model":"gpt-4.1","messages":[],"stream":true,"max_tokens":256}`)

	lines := [][]byte{
		[]byte(`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4.1","choices":[{"index":0,"delta":{"reasoning_content":"plan"},"finish_reason":null}]}`),
		[]byte(`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4.1","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`),
		[]byte(`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4.1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\""}}]},"finish_reason":null}]}`),
		[]byte(`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4.1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"x\"}"}}]},"finish_reason":null}]}`),
		[]byte(`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4.1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":2},"completion_tokens_details":{"reasoning_tokens":1}}}`),
	}

	var state any
	var events []parsedSSE
	for _, line := range lines {
		outs, err := tr.TransformResponseStream(context.Background(), "gpt-4.1", originalRequest, requestRaw, line, &state)
		if err != nil {
			t.Fatalf("TransformResponseStream() error = %v", err)
		}
		events = append(events, parseSSEOutputs(t, outs)...)
	}

	names := make([]string, 0, len(events))
	for _, event := range events {
		names = append(names, event.Event)
	}
	joined := strings.Join(names, ",")
	for _, want := range []string{
		"response.created",
		"response.in_progress",
		"response.reasoning_summary_text.delta",
		"response.reasoning_summary_text.done",
		"response.output_text.delta",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.completed",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing event %q in %s", want, joined)
		}
	}

	completed := findEvent(t, events, "response.completed").Data.Get("response")
	if completed.Get("instructions").String() != "be concise" {
		t.Fatalf("instructions mismatch: %s", completed.Raw)
	}
	if completed.Get("output.0.type").String() != "reasoning" {
		t.Fatalf("reasoning output missing: %s", completed.Raw)
	}
	if completed.Get("output.1.type").String() != "message" || completed.Get("output.1.content.0.text").String() != "Hello" {
		t.Fatalf("message output mismatch: %s", completed.Raw)
	}
	if completed.Get("output.2.type").String() != "function_call" || completed.Get("output.2.arguments").String() != "{\"q\":\"x\"}" {
		t.Fatalf("function output mismatch: %s", completed.Raw)
	}
	if completed.Get("usage.input_tokens").Int() != 10 || completed.Get("usage.output_tokens").Int() != 5 || completed.Get("usage.total_tokens").Int() != 15 {
		t.Fatalf("usage mismatch: %s", completed.Get("usage").Raw)
	}
	if completed.Get("usage.input_tokens_details.cached_tokens").Int() != 2 {
		t.Fatalf("cached usage mismatch: %s", completed.Get("usage").Raw)
	}
	if completed.Get("usage.output_tokens_details.reasoning_tokens").Int() != 1 {
		t.Fatalf("reasoning usage mismatch: %s", completed.Get("usage").Raw)
	}
}

func TestTransformResponseNonStream_ChatCompletionJSON(t *testing.T) {
	tr := Transformer{}
	originalRequest := []byte(`{
		"model":"gpt-4.1",
		"instructions":"be concise",
		"max_output_tokens":64,
		"reasoning":{"effort":"medium"}
	}`)
	requestRaw := []byte(`{"model":"gpt-4.1","messages":[],"max_tokens":64}`)

	body := []byte(`{
		"id":"chatcmpl_2",
		"object":"chat.completion",
		"created":1700000001,
		"model":"gpt-4.1",
		"choices":[
			{
				"index":0,
				"message":{
					"role":"assistant",
					"content":"Done",
					"reasoning_content":"analysis",
					"tool_calls":[
						{"id":"call_2","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"done\"}"}}
					]
				},
				"finish_reason":"tool_calls"
			}
		],
		"usage":{
			"prompt_tokens":4,
			"completion_tokens":6,
			"total_tokens":10,
			"prompt_tokens_details":{"cached_tokens":1},
			"completion_tokens_details":{"reasoning_tokens":2}
		}
	}`)

	got, err := tr.TransformResponseNonStream(context.Background(), "gpt-4.1", originalRequest, requestRaw, body, nil)
	if err != nil {
		t.Fatalf("TransformResponseNonStream() error = %v", err)
	}

	root := gjson.ParseBytes(got)
	if root.Get("object").String() != "response" {
		t.Fatalf("object = %q", root.Get("object").String())
	}
	if root.Get("instructions").String() != "be concise" {
		t.Fatalf("instructions mismatch: %s", root.Raw)
	}
	if root.Get("max_output_tokens").Int() != 64 {
		t.Fatalf("max_output_tokens mismatch: %s", root.Raw)
	}
	if root.Get("output.0.type").String() != "reasoning" || root.Get("output.0.summary.0.text").String() != "analysis" {
		t.Fatalf("reasoning output mismatch: %s", root.Raw)
	}
	if root.Get("output.1.type").String() != "message" || root.Get("output.1.content.0.text").String() != "Done" {
		t.Fatalf("message output mismatch: %s", root.Raw)
	}
	if root.Get("output.2.type").String() != "function_call" || root.Get("output.2.call_id").String() != "call_2" {
		t.Fatalf("function output mismatch: %s", root.Raw)
	}
	if root.Get("usage.output_tokens_details.reasoning_tokens").Int() != 2 {
		t.Fatalf("usage mismatch: %s", root.Get("usage").Raw)
	}
}

func TestTransformResponseNonStream_ChatCompletionJSON_MultipleToolCalls(t *testing.T) {
	tr := Transformer{}
	originalRequest := []byte(`{
		"model":"gpt-4.1",
		"instructions":"be concise"
	}`)
	requestRaw := []byte(`{"model":"gpt-4.1","messages":[]}`)

	body := []byte(`{
		"id":"chatcmpl_multi",
		"object":"chat.completion",
		"created":1700000001,
		"model":"gpt-4.1",
		"choices":[
			{
				"index":0,
				"message":{
					"role":"assistant",
					"content":"Done",
					"tool_calls":[
						{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"one\"}"}},
						{"id":"call_2","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"two\"}"}}
					]
				},
				"finish_reason":"tool_calls"
			}
		]
	}`)

	got, err := tr.TransformResponseNonStream(context.Background(), "gpt-4.1", originalRequest, requestRaw, body, nil)
	if err != nil {
		t.Fatalf("TransformResponseNonStream() error = %v", err)
	}

	root := gjson.ParseBytes(got)
	if root.Get("output.#").Int() != 3 {
		t.Fatalf("output length = %d, want 3: %s", root.Get("output.#").Int(), root.Raw)
	}
	if root.Get("output.1.type").String() != "function_call" || root.Get("output.1.call_id").String() != "call_1" {
		t.Fatalf("first function output mismatch: %s", root.Raw)
	}
	if root.Get("output.2.type").String() != "function_call" || root.Get("output.2.call_id").String() != "call_2" {
		t.Fatalf("second function output mismatch: %s", root.Raw)
	}
}

func TestTransformResponseNonStream_TranscriptAggregation(t *testing.T) {
	tr := Transformer{}
	originalRequest := []byte(`{
		"model":"gpt-4.1",
		"instructions":"be concise",
		"max_output_tokens":256,
		"reasoning":{"effort":"medium"}
	}`)
	requestRaw := []byte(`{"model":"gpt-4.1","messages":[],"stream":true,"max_tokens":256}`)

	transcript := strings.Join([]string{
		`data: {"id":"chatcmpl_3","object":"chat.completion.chunk","created":1700000002,"model":"gpt-4.1","choices":[{"index":0,"delta":{"reasoning_content":"plan"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_3","object":"chat.completion.chunk","created":1700000002,"model":"gpt-4.1","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_3","object":"chat.completion.chunk","created":1700000002,"model":"gpt-4.1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_3","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_3","object":"chat.completion.chunk","created":1700000002,"model":"gpt-4.1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		`data: [DONE]`,
	}, "\n")

	got, err := tr.TransformResponseNonStream(context.Background(), "gpt-4.1", originalRequest, requestRaw, []byte(transcript), nil)
	if err != nil {
		t.Fatalf("TransformResponseNonStream() error = %v", err)
	}

	root := gjson.ParseBytes(got)
	if root.Get("id").String() != "chatcmpl_3" {
		t.Fatalf("id mismatch: %s", root.Raw)
	}
	if root.Get("output.0.type").String() != "reasoning" || root.Get("output.0.summary.0.text").String() != "plan" {
		t.Fatalf("reasoning transcript mismatch: %s", root.Raw)
	}
	if root.Get("output.1.content.0.text").String() != "Hello" {
		t.Fatalf("message transcript mismatch: %s", root.Raw)
	}
	if root.Get("output.2.call_id").String() != "call_3" {
		t.Fatalf("tool transcript mismatch: %s", root.Raw)
	}
	if root.Get("usage.total_tokens").Int() != 15 {
		t.Fatalf("usage transcript mismatch: %s", root.Get("usage").Raw)
	}
}

type parsedSSE struct {
	Event string
	Data  gjson.Result
}

func parseSSEOutputs(t *testing.T, outs []string) []parsedSSE {
	t.Helper()
	events := make([]parsedSSE, 0)
	for _, out := range outs {
		chunks := strings.Split(strings.TrimSpace(out), "\n\n")
		for _, chunk := range chunks {
			chunk = strings.TrimSpace(chunk)
			if chunk == "" {
				continue
			}
			lines := strings.Split(chunk, "\n")
			var eventName string
			var data string
			for _, line := range lines {
				line = strings.TrimSpace(line)
				switch {
				case strings.HasPrefix(line, "event: "):
					eventName = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
				case strings.HasPrefix(line, "data: "):
					data = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
				}
			}
			if eventName == "" || data == "" {
				t.Fatalf("invalid sse chunk: %q", chunk)
			}
			events = append(events, parsedSSE{
				Event: eventName,
				Data:  gjson.Parse(data),
			})
		}
	}
	return events
}

func findEvent(t *testing.T, events []parsedSSE, name string) parsedSSE {
	t.Helper()
	for _, event := range events {
		if event.Event == name {
			return event
		}
	}
	t.Fatalf("event %q not found", name)
	return parsedSSE{}
}
