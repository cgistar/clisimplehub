package chat_completions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const codexDebugLogGlob = "/var/folders/dl/0j4pntmj5nv57r3f44sf46vm0000gn/T/clisimplehub_debug_logs/*-codex-CodexProvider.log"

func TestLogReplay_RequestParityWithThirdParty(t *testing.T) {
	files, err := filepath.Glob(codexDebugLogGlob)
	if err != nil {
		t.Fatalf("glob logs: %v", err)
	}
	if len(files) == 0 {
		t.Skipf("no log files matched %s", codexDebugLogGlob)
	}

	tr := Transformer{}
	for _, file := range files {
		file := file
		t.Run(filepath.Base(file), func(t *testing.T) {
			rawLog, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read log: %v", err)
			}

			originalRequest, ok := extractLogSection(string(rawLog), "--- OriginalRequest ---")
			if !ok {
				t.Fatalf("missing OriginalRequest section")
			}

			modelName := gjson.Get(originalRequest, "model").String()
			if modelName == "" {
				t.Fatalf("missing model in OriginalRequest")
			}
			stream := strings.Contains(string(rawLog), "Streamed=true")

			got, err := tr.TransformRequest(modelName, []byte(originalRequest), stream)
			if err != nil {
				t.Fatalf("TransformRequest() error = %v", err)
			}
			want := thirdPartyConvertResponsesRequestToChatCompletions(modelName, []byte(originalRequest), stream)

			assertJSONEqual(t, got, want)
		})
	}
}

func TestLogReplay_ResponseParityWithThirdParty(t *testing.T) {
	files, err := filepath.Glob(codexDebugLogGlob)
	if err != nil {
		t.Fatalf("glob logs: %v", err)
	}
	if len(files) == 0 {
		t.Skipf("no log files matched %s", codexDebugLogGlob)
	}

	tr := Transformer{}
	supported := 0
	for _, file := range files {
		rawLog, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read log %s: %v", file, err)
		}

		originalRequest, ok := extractLogSection(string(rawLog), "--- OriginalRequest ---")
		if !ok {
			t.Fatalf("%s: missing OriginalRequest section", filepath.Base(file))
		}
		upstreamResponseRaw, ok := extractLogSection(string(rawLog), "--- UpstreamResponseRaw ---")
		if !ok {
			t.Fatalf("%s: missing UpstreamResponseRaw section", filepath.Base(file))
		}
		if !looksLikeChatCompletionJSONObject(upstreamResponseRaw) {
			t.Logf("%s: skip response parity, upstream raw is not chat-completions JSON", filepath.Base(file))
			continue
		}

		supported++
		modelName := gjson.Get(originalRequest, "model").String()
		if modelName == "" {
			t.Fatalf("%s: missing model in OriginalRequest", filepath.Base(file))
		}

		requestRaw, err := tr.TransformRequest(modelName, []byte(originalRequest), false)
		if err != nil {
			t.Fatalf("%s: TransformRequest() error = %v", filepath.Base(file), err)
		}

		got, err := tr.TransformResponseNonStream(context.Background(), modelName, []byte(originalRequest), requestRaw, []byte(upstreamResponseRaw), nil)
		if err != nil {
			t.Fatalf("%s: TransformResponseNonStream() error = %v", filepath.Base(file), err)
		}
		want := []byte(thirdPartyConvertChatCompletionsResponseToResponsesNonStream([]byte(originalRequest), requestRaw, []byte(upstreamResponseRaw)))

		t.Run(filepath.Base(file), func(t *testing.T) {
			assertJSONEqual(t, got, want)
		})
	}

	if supported == 0 {
		t.Skip("no log contains chat-completions JSON upstream response; existing codex logs are /responses payloads")
	}
}

func extractLogSection(logText, marker string) (string, bool) {
	idx := strings.Index(logText, marker)
	if idx < 0 {
		return "", false
	}

	rest := logText[idx+len(marker):]
	rest = strings.TrimLeft(rest, "\r\n")
	next := strings.Index(rest, "\n--- ")
	if next >= 0 {
		rest = rest[:next]
	}
	return strings.TrimSpace(rest), true
}

func looksLikeChatCompletionJSONObject(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || !gjson.Valid(raw) {
		return false
	}
	root := gjson.Parse(raw)
	return root.Get("choices").Exists() && root.Get("object").String() == "chat.completion"
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()

	gotValue := decodeJSONValue(t, got)
	wantValue := decodeJSONValue(t, want)
	if reflect.DeepEqual(gotValue, wantValue) {
		return
	}

	if path, gotAt, wantAt, ok := findFirstDiff("$", gotValue, wantValue); ok {
		t.Fatalf("json mismatch at %s\nwant: %#v\ngot: %#v", path, wantAt, gotAt)
	}

	gotPretty, _ := json.MarshalIndent(gotValue, "", "  ")
	wantPretty, _ := json.MarshalIndent(wantValue, "", "  ")
	t.Fatalf("json mismatch\nwant:\n%s\n\ngot:\n%s", string(wantPretty), string(gotPretty))
}

func decodeJSONValue(t *testing.T, raw []byte) any {
	t.Helper()

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("unmarshal json: %v\nraw=%s", err, string(raw))
	}
	return value
}

func findFirstDiff(path string, got, want any) (string, any, any, bool) {
	if reflect.DeepEqual(got, want) {
		return "", nil, nil, false
	}

	gotMap, gotIsMap := got.(map[string]any)
	wantMap, wantIsMap := want.(map[string]any)
	if gotIsMap && wantIsMap {
		seen := make(map[string]struct{}, len(gotMap)+len(wantMap))
		for key := range wantMap {
			seen[key] = struct{}{}
		}
		for key := range gotMap {
			seen[key] = struct{}{}
		}
		for _, key := range sortedStringKeys(seen) {
			gotVal, gotOK := gotMap[key]
			wantVal, wantOK := wantMap[key]
			if !gotOK || !wantOK {
				return path + "." + key, gotVal, wantVal, true
			}
			if diffPath, diffGot, diffWant, ok := findFirstDiff(path+"."+key, gotVal, wantVal); ok {
				return diffPath, diffGot, diffWant, true
			}
		}
	}

	gotSlice, gotIsSlice := got.([]any)
	wantSlice, wantIsSlice := want.([]any)
	if gotIsSlice && wantIsSlice {
		if len(gotSlice) != len(wantSlice) {
			return path + ".length", len(gotSlice), len(wantSlice), true
		}
		for i := range wantSlice {
			if diffPath, diffGot, diffWant, ok := findFirstDiff(fmt.Sprintf("%s[%d]", path, i), gotSlice[i], wantSlice[i]); ok {
				return diffPath, diffGot, diffWant, true
			}
		}
	}

	return path, got, want, true
}

func sortedStringKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func thirdPartyConvertResponsesRequestToChatCompletions(modelName string, inputRawJSON []byte, stream bool) []byte {
	rawJSON := inputRawJSON
	out := `{"model":"","messages":[],"stream":false}`

	root := gjson.ParseBytes(rawJSON)

	out, _ = sjson.Set(out, "model", modelName)
	out, _ = sjson.Set(out, "stream", stream)

	if maxTokens := root.Get("max_output_tokens"); maxTokens.Exists() {
		out, _ = sjson.Set(out, "max_tokens", maxTokens.Int())
	}

	if parallelToolCalls := root.Get("parallel_tool_calls"); parallelToolCalls.Exists() {
		out, _ = sjson.Set(out, "parallel_tool_calls", parallelToolCalls.Bool())
	}

	if instructions := root.Get("instructions"); instructions.Exists() {
		systemMessage := `{"role":"system","content":""}`
		systemMessage, _ = sjson.Set(systemMessage, "content", instructions.String())
		out, _ = sjson.SetRaw(out, "messages.-1", systemMessage)
	}

	if input := root.Get("input"); input.Exists() && input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool {
			itemType := item.Get("type").String()
			if itemType == "" && item.Get("role").String() != "" {
				itemType = "message"
			}

			switch itemType {
			case "message", "":
				role := item.Get("role").String()
				if role == "developer" {
					role = "user"
				}
				message := `{"role":"","content":[]}`
				message, _ = sjson.Set(message, "role", role)

				if content := item.Get("content"); content.Exists() && content.IsArray() {
					content.ForEach(func(_, contentItem gjson.Result) bool {
						contentType := contentItem.Get("type").String()
						if contentType == "" {
							contentType = "input_text"
						}

						switch contentType {
						case "input_text", "output_text":
							text := contentItem.Get("text").String()
							contentPart := `{"type":"text","text":""}`
							contentPart, _ = sjson.Set(contentPart, "text", text)
							message, _ = sjson.SetRaw(message, "content.-1", contentPart)
						case "input_image":
							imageURL := contentItem.Get("image_url").String()
							contentPart := `{"type":"image_url","image_url":{"url":""}}`
							contentPart, _ = sjson.Set(contentPart, "image_url.url", imageURL)
							message, _ = sjson.SetRaw(message, "content.-1", contentPart)
						}
						return true
					})
				} else if content.Type == gjson.String {
					message, _ = sjson.Set(message, "content", content.String())
				}

				out, _ = sjson.SetRaw(out, "messages.-1", message)

			case "function_call":
				assistantMessage := `{"role":"assistant","tool_calls":[]}`
				toolCall := `{"id":"","type":"function","function":{"name":"","arguments":""}}`

				if callID := item.Get("call_id"); callID.Exists() {
					toolCall, _ = sjson.Set(toolCall, "id", callID.String())
				}
				if name := item.Get("name"); name.Exists() {
					toolCall, _ = sjson.Set(toolCall, "function.name", name.String())
				}
				if arguments := item.Get("arguments"); arguments.Exists() {
					toolCall, _ = sjson.Set(toolCall, "function.arguments", arguments.String())
				}

				assistantMessage, _ = sjson.SetRaw(assistantMessage, "tool_calls.0", toolCall)
				out, _ = sjson.SetRaw(out, "messages.-1", assistantMessage)

			case "function_call_output":
				toolMessage := `{"role":"tool","tool_call_id":"","content":""}`

				if callID := item.Get("call_id"); callID.Exists() {
					toolMessage, _ = sjson.Set(toolMessage, "tool_call_id", callID.String())
				}
				if output := item.Get("output"); output.Exists() {
					toolMessage, _ = sjson.Set(toolMessage, "content", output.String())
				}

				out, _ = sjson.SetRaw(out, "messages.-1", toolMessage)
			}

			return true
		})
	} else if input.Type == gjson.String {
		msg := "{}"
		msg, _ = sjson.Set(msg, "role", "user")
		msg, _ = sjson.Set(msg, "content", input.String())
		out, _ = sjson.SetRaw(out, "messages.-1", msg)
	}

	if tools := root.Get("tools"); tools.Exists() && tools.IsArray() {
		var chatCompletionsTools []interface{}

		tools.ForEach(func(_, tool gjson.Result) bool {
			toolType := tool.Get("type").String()
			if toolType != "" && toolType != "function" && tool.IsObject() {
				return true
			}

			chatTool := `{"type":"function","function":{}}`
			function := `{"name":"","description":"","parameters":{}}`

			if name := tool.Get("name"); name.Exists() {
				function, _ = sjson.Set(function, "name", name.String())
			}
			if description := tool.Get("description"); description.Exists() {
				function, _ = sjson.Set(function, "description", description.String())
			}
			if parameters := tool.Get("parameters"); parameters.Exists() {
				function, _ = sjson.SetRaw(function, "parameters", parameters.Raw)
			}

			chatTool, _ = sjson.SetRaw(chatTool, "function", function)
			chatCompletionsTools = append(chatCompletionsTools, gjson.Parse(chatTool).Value())
			return true
		})

		if len(chatCompletionsTools) > 0 {
			out, _ = sjson.Set(out, "tools", chatCompletionsTools)
		}
	}

	if reasoningEffort := root.Get("reasoning.effort"); reasoningEffort.Exists() {
		effort := strings.ToLower(strings.TrimSpace(reasoningEffort.String()))
		if effort != "" {
			out, _ = sjson.Set(out, "reasoning_effort", effort)
		}
	}

	if toolChoice := root.Get("tool_choice"); toolChoice.Exists() {
		out, _ = sjson.Set(out, "tool_choice", toolChoice.String())
	}

	return []byte(out)
}

func thirdPartyConvertChatCompletionsResponseToResponsesNonStream(originalRequestRawJSON, requestRawJSON, rawJSON []byte) string {
	root := gjson.ParseBytes(rawJSON)
	resp := `{"id":"","object":"response","created_at":0,"status":"completed","background":false,"error":null,"incomplete_details":null}`

	id := root.Get("id").String()
	if id == "" {
		id = fmt.Sprintf("resp_%d", time.Now().UnixNano())
	}
	resp, _ = sjson.Set(resp, "id", id)

	created := root.Get("created").Int()
	if created == 0 {
		created = time.Now().Unix()
	}
	resp, _ = sjson.Set(resp, "created_at", created)

	if len(requestRawJSON) > 0 {
		req := gjson.ParseBytes(requestRawJSON)
		if v := req.Get("instructions"); v.Exists() {
			resp, _ = sjson.Set(resp, "instructions", v.String())
		}
		if v := req.Get("max_output_tokens"); v.Exists() {
			resp, _ = sjson.Set(resp, "max_output_tokens", v.Int())
		} else if v = req.Get("max_tokens"); v.Exists() {
			resp, _ = sjson.Set(resp, "max_output_tokens", v.Int())
		}
		if v := req.Get("max_tool_calls"); v.Exists() {
			resp, _ = sjson.Set(resp, "max_tool_calls", v.Int())
		}
		if v := req.Get("model"); v.Exists() {
			resp, _ = sjson.Set(resp, "model", v.String())
		} else if v = root.Get("model"); v.Exists() {
			resp, _ = sjson.Set(resp, "model", v.String())
		}
		if v := req.Get("parallel_tool_calls"); v.Exists() {
			resp, _ = sjson.Set(resp, "parallel_tool_calls", v.Bool())
		}
		if v := req.Get("previous_response_id"); v.Exists() {
			resp, _ = sjson.Set(resp, "previous_response_id", v.String())
		}
		if v := req.Get("prompt_cache_key"); v.Exists() {
			resp, _ = sjson.Set(resp, "prompt_cache_key", v.String())
		}
		if v := req.Get("reasoning"); v.Exists() {
			resp, _ = sjson.Set(resp, "reasoning", v.Value())
		}
		if v := req.Get("safety_identifier"); v.Exists() {
			resp, _ = sjson.Set(resp, "safety_identifier", v.String())
		}
		if v := req.Get("service_tier"); v.Exists() {
			resp, _ = sjson.Set(resp, "service_tier", v.String())
		}
		if v := req.Get("store"); v.Exists() {
			resp, _ = sjson.Set(resp, "store", v.Bool())
		}
		if v := req.Get("temperature"); v.Exists() {
			resp, _ = sjson.Set(resp, "temperature", v.Float())
		}
		if v := req.Get("text"); v.Exists() {
			resp, _ = sjson.Set(resp, "text", v.Value())
		}
		if v := req.Get("tool_choice"); v.Exists() {
			resp, _ = sjson.Set(resp, "tool_choice", v.Value())
		}
		if v := req.Get("tools"); v.Exists() {
			resp, _ = sjson.Set(resp, "tools", v.Value())
		}
		if v := req.Get("top_logprobs"); v.Exists() {
			resp, _ = sjson.Set(resp, "top_logprobs", v.Int())
		}
		if v := req.Get("top_p"); v.Exists() {
			resp, _ = sjson.Set(resp, "top_p", v.Float())
		}
		if v := req.Get("truncation"); v.Exists() {
			resp, _ = sjson.Set(resp, "truncation", v.String())
		}
		if v := req.Get("user"); v.Exists() {
			resp, _ = sjson.Set(resp, "user", v.Value())
		}
		if v := req.Get("metadata"); v.Exists() {
			resp, _ = sjson.Set(resp, "metadata", v.Value())
		}
	} else if v := root.Get("model"); v.Exists() {
		resp, _ = sjson.Set(resp, "model", v.String())
	}

	outputsWrapper := `{"arr":[]}`
	rcText := gjson.GetBytes(rawJSON, "choices.0.message.reasoning_content").String()
	includeReasoning := rcText != ""
	if !includeReasoning && len(requestRawJSON) > 0 {
		includeReasoning = gjson.GetBytes(requestRawJSON, "reasoning").Exists()
	}
	if includeReasoning {
		rid := id
		if strings.HasPrefix(rid, "resp_") {
			rid = strings.TrimPrefix(rid, "resp_")
		}
		reasoningItem := `{"id":"","type":"reasoning","encrypted_content":"","summary":[]}`
		reasoningItem, _ = sjson.Set(reasoningItem, "id", fmt.Sprintf("rs_%s", rid))
		if rcText != "" {
			reasoningItem, _ = sjson.Set(reasoningItem, "summary.0.type", "summary_text")
			reasoningItem, _ = sjson.Set(reasoningItem, "summary.0.text", rcText)
		}
		outputsWrapper, _ = sjson.SetRaw(outputsWrapper, "arr.-1", reasoningItem)
	}

	if choices := root.Get("choices"); choices.Exists() && choices.IsArray() {
		choices.ForEach(func(_, choice gjson.Result) bool {
			msg := choice.Get("message")
			if msg.Exists() {
				if c := msg.Get("content"); c.Exists() && c.String() != "" {
					item := `{"id":"","type":"message","status":"completed","content":[{"type":"output_text","annotations":[],"logprobs":[],"text":""}],"role":"assistant"}`
					item, _ = sjson.Set(item, "id", fmt.Sprintf("msg_%s_%d", id, int(choice.Get("index").Int())))
					item, _ = sjson.Set(item, "content.0.text", c.String())
					outputsWrapper, _ = sjson.SetRaw(outputsWrapper, "arr.-1", item)
				}
				if tcs := msg.Get("tool_calls"); tcs.Exists() && tcs.IsArray() {
					tcs.ForEach(func(_, tc gjson.Result) bool {
						callID := tc.Get("id").String()
						name := tc.Get("function.name").String()
						args := tc.Get("function.arguments").String()
						item := `{"id":"","type":"function_call","status":"completed","arguments":"","call_id":"","name":""}`
						item, _ = sjson.Set(item, "id", fmt.Sprintf("fc_%s", callID))
						item, _ = sjson.Set(item, "arguments", args)
						item, _ = sjson.Set(item, "call_id", callID)
						item, _ = sjson.Set(item, "name", name)
						outputsWrapper, _ = sjson.SetRaw(outputsWrapper, "arr.-1", item)
						return true
					})
				}
			}
			return true
		})
	}
	if gjson.Get(outputsWrapper, "arr.#").Int() > 0 {
		resp, _ = sjson.SetRaw(resp, "output", gjson.Get(outputsWrapper, "arr").Raw)
	}

	if usage := root.Get("usage"); usage.Exists() {
		if usage.Get("prompt_tokens").Exists() || usage.Get("completion_tokens").Exists() || usage.Get("total_tokens").Exists() {
			resp, _ = sjson.Set(resp, "usage.input_tokens", usage.Get("prompt_tokens").Int())
			if d := usage.Get("prompt_tokens_details.cached_tokens"); d.Exists() {
				resp, _ = sjson.Set(resp, "usage.input_tokens_details.cached_tokens", d.Int())
			}
			resp, _ = sjson.Set(resp, "usage.output_tokens", usage.Get("completion_tokens").Int())
			if d := usage.Get("output_tokens_details.reasoning_tokens"); d.Exists() {
				resp, _ = sjson.Set(resp, "usage.output_tokens_details.reasoning_tokens", d.Int())
			}
			resp, _ = sjson.Set(resp, "usage.total_tokens", usage.Get("total_tokens").Int())
		} else {
			resp, _ = sjson.Set(resp, "usage", usage.Value())
		}
	}

	_ = originalRequestRawJSON
	return resp
}
