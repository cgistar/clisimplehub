package responses

import (
	"bytes"
	"fmt"
	"strings"

	"clisimplehub/internal/transformer/shared"

	"github.com/tidwall/gjson"
)

func transformResponsesLineToClaudeSSE(originalRequestRawJSON []byte, modelName string, rawLine []byte, state *responsesToClaudeStreamState) ([]string, error) {
	if state == nil {
		return nil, fmt.Errorf("nil stream state")
	}

	line := bytes.TrimSpace(rawLine)
	if len(line) == 0 || bytes.HasPrefix(line, []byte("event:")) || bytes.HasPrefix(line, []byte(":")) {
		return nil, nil
	}

	payload := line
	if p, ok := shared.SSEDataPayload(line); ok {
		payload = p
	}

	if bytes.Equal(payload, []byte("[DONE]")) {
		if state.SentMessageStop {
			return nil, nil
		}
		outs := append([]string{}, flushOpenClaudeBlocks(state)...)
		outs = append(outs, shared.SSEEvent("message_stop", map[string]any{"type": "message_stop"}))
		state.SentMessageStop = true
		return outs, nil
	}
	if !gjson.ValidBytes(payload) {
		return nil, nil
	}

	root := gjson.ParseBytes(payload)
	// thinking 延迟关闭：在 text/completed 边界真正 finalize
	if state.ThinkingBlockOpen && state.ThinkingStopPending {
		switch root.Get("type").String() {
		case "response.content_part.added", "response.completed", "response.incomplete":
			// fallthrough handled below by explicit finalize
		}
	}

	typeStr := root.Get("type").String()
	outputs := make([]string, 0, 6)

	// pending thinking close on certain events
	if state.ThinkingBlockOpen && state.ThinkingStopPending {
		switch typeStr {
		case "response.content_part.added", "response.completed", "response.incomplete":
			outputs = append(outputs, finalizeClaudeThinkingBlock(state)...)
		}
	}

	switch typeStr {
	case "error":
		outputs = append(outputs, codexStreamErrorToClaudeSSE(root)...)

	case "response.created":
		resp := root.Get("response")
		model := strings.TrimSpace(modelName)
		if model == "" {
			model = strings.TrimSpace(resp.Get("model").String())
		}
		outputs = append(outputs, shared.SSEEvent("message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            resp.Get("id").String(),
				"type":          "message",
				"role":          "assistant",
				"model":         model,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  0,
					"output_tokens": 0,
				},
				"content":     []any{},
				"stop_reason": nil,
			},
		}))

	case "response.reasoning_summary_part.added":
		if state.ThinkingBlockOpen && state.ThinkingStopPending {
			outputs = append(outputs, finalizeClaudeThinkingBlock(state)...)
		}
		state.ThinkingSummarySeen = true
		outputs = append(outputs, startClaudeThinkingBlock(state)...)

	case "response.reasoning_summary_text.delta":
		outputs = append(outputs, startClaudeThinkingBlock(state)...)
		if state.ThinkingBlockOpen {
			outputs = append(outputs, shared.SSEEvent("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": state.BlockIndex,
				"delta": map[string]any{
					"type":     "thinking_delta",
					"thinking": root.Get("delta").String(),
				},
			}))
		}

	case "response.reasoning_summary_part.done":
		state.ThinkingStopPending = true

	case "response.content_part.added":
		if root.Get("part.type").String() == "output_text" {
			outputs = append(outputs, finalizeClaudeThinkingBlock(state)...)
			outputs = append(outputs, startClaudeTextBlock(state)...)
		}

	case "response.output_text.delta":
		state.HasTextDelta = true
		outputs = append(outputs, finalizeClaudeThinkingBlock(state)...)
		outputs = append(outputs, startClaudeTextBlock(state)...)
		if state.TextBlockOpen {
			outputs = append(outputs, shared.SSEEvent("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": state.BlockIndex,
				"delta": map[string]any{
					"type": "text_delta",
					"text": root.Get("delta").String(),
				},
			}))
		}

	case "response.content_part.done":
		if root.Get("part.type").String() == "output_text" {
			outputs = append(outputs, stopClaudeTextBlock(state)...)
		}

	case "response.web_search_call.searching", "response.web_search_call.completed", "response.web_search_call.in_progress":
		// 等 output_item.done 再发完整 server_tool_use / result

	case "response.output_item.added":
		item := root.Get("item")
		switch item.Get("type").String() {
		case "function_call":
			outputs = append(outputs, finalizeClaudeThinkingBlock(state)...)
			outputs = append(outputs, stopClaudeTextBlock(state)...)
			state.HasReceivedArgumentsDelta = false

			callID := functionCallID(item)
			name := item.Get("name").String()
			if name == "" {
				// 延迟到 done / terminal 再 start
				recordPendingFunctionCall(state, root, item)
				break
			}
			if pending, pendingKeys := pendingFunctionCallForDone(state, root, item); pending != nil {
				deletePendingFunctionCallAliases(state, pendingKeys)
			}
			blockIndex := state.BlockIndex
			outputs = append(outputs, appendFunctionCallStart(originalRequestRawJSON, callID, name, blockIndex)...)
			state.HasEmittedToolUse = true
			outputs = append(outputs, appendFunctionCallArgumentDelta("", blockIndex)...)
			state.FunctionCallBlockOpen = true
			state.FunctionCallBlockCallID = callID
			state.FunctionCallBlockIndex = blockIndex

		case "reasoning":
			state.ThinkingSummarySeen = false
			// 即使为空也覆盖，避免串用上一段 signature
			state.ThinkingSignature = item.Get("encrypted_content").String()
		case "web_search_call":
			// defer to done
		}

	case "response.output_item.done":
		item := root.Get("item")
		switch item.Get("type").String() {
		case "message":
			if state.HasTextDelta {
				break
			}
			var textBuilder strings.Builder
			content := item.Get("content")
			if content.IsArray() {
				for _, part := range content.Array() {
					if part.Get("type").String() != "output_text" {
						continue
					}
					if txt := part.Get("text").String(); txt != "" {
						textBuilder.WriteString(txt)
					}
				}
			}
			if text := textBuilder.String(); text != "" {
				outputs = append(outputs, finalizeClaudeThinkingBlock(state)...)
				outputs = append(outputs, startClaudeTextBlock(state)...)
				if state.TextBlockOpen {
					outputs = append(outputs, shared.SSEEvent("content_block_delta", map[string]any{
						"type":  "content_block_delta",
						"index": state.BlockIndex,
						"delta": map[string]any{"type": "text_delta", "text": text},
					}))
				}
				outputs = append(outputs, stopClaudeTextBlock(state)...)
				state.HasTextDelta = true
			}

		case "function_call":
			if pending, pendingKeys := pendingFunctionCallForDone(state, root, item); pending != nil && !pending.StartEmitted {
				name := item.Get("name").String()
				if name == "" {
					break
				}
				callID := pending.CallID
				if callID == "" {
					callID = functionCallID(item)
				}
				blockIndex := state.BlockIndex
				outputs = append(outputs, appendFunctionCallStart(originalRequestRawJSON, callID, name, blockIndex)...)
				state.HasEmittedToolUse = true
				pending.StartEmitted = true
				args := pending.Arguments
				if args == "" {
					args = item.Get("arguments").String()
				}
				if args != "" {
					outputs = append(outputs, appendFunctionCallArgumentDelta(args, blockIndex)...)
				}
				outputs = append(outputs, appendFunctionCallStop(blockIndex)...)
				state.BlockIndex++
				deletePendingFunctionCallAliases(state, pendingKeys)
			} else if state.FunctionCallBlockOpen {
				if !state.HasReceivedArgumentsDelta {
					if args := item.Get("arguments").String(); args != "" {
						outputs = append(outputs, appendFunctionCallArgumentDelta(args, state.FunctionCallBlockIndex)...)
						state.HasReceivedArgumentsDelta = true
					}
				}
				outputs = append(outputs, stopClaudeFunctionCallBlock(state)...)
			}

		case "reasoning":
			if sig := item.Get("encrypted_content").String(); sig != "" {
				state.ThinkingSignature = sig
			}
			if state.ThinkingSummarySeen {
				outputs = append(outputs, finalizeClaudeThinkingBlock(state)...)
			} else {
				outputs = append(outputs, finalizeSignatureOnlyThinkingBlock(state)...)
			}
			state.ThinkingSignature = ""
			state.ThinkingSummarySeen = false

		case "web_search_call":
			outputs = appendWebSearchToolResult(outputs, state, root, item)
		}

	case "response.function_call_arguments.delta":
		delta := root.Get("delta").String()
		key := argumentsFunctionCallKey(state, root)
		if pending, _ := pendingFunctionCallForKey(state, key); pending != nil && !pending.StartEmitted {
			pending.HasReceivedArgumentsDelta = true
			pending.Arguments += delta
			break
		}
		state.HasReceivedArgumentsDelta = true
		idx := state.BlockIndex
		if state.FunctionCallBlockOpen {
			idx = state.FunctionCallBlockIndex
		}
		outputs = append(outputs, appendFunctionCallArgumentDelta(delta, idx)...)

	case "response.function_call_arguments.done":
		key := argumentsFunctionCallKey(state, root)
		if pending, _ := pendingFunctionCallForKey(state, key); pending != nil && !pending.StartEmitted {
			if !pending.HasReceivedArgumentsDelta {
				pending.Arguments = root.Get("arguments").String()
			}
			break
		}
		if !state.HasReceivedArgumentsDelta {
			if args := root.Get("arguments").String(); args != "" {
				idx := state.BlockIndex
				if state.FunctionCallBlockOpen {
					idx = state.FunctionCallBlockIndex
				}
				outputs = append(outputs, appendFunctionCallArgumentDelta(args, idx)...)
				state.HasReceivedArgumentsDelta = true
			}
		}

	case "response.completed", "response.incomplete":
		responseData := root.Get("response")
		outputs = hydrateOpenFunctionCallFromTerminal(outputs, state, responseData)
		outputs = append(outputs, flushOpenClaudeBlocks(state)...)
		outputs = appendPendingFunctionCallsFromTerminal(outputs, state, originalRequestRawJSON, responseData)

		stopReason := mapCodexStopReasonToClaude(codexStopReason(responseData), state.HasEmittedToolUse)
		delta := map[string]any{
			"stop_reason": stopReason,
		}
		setClaudeStopSequenceInMap(delta, responseData)

		inputTokens, outputTokens, cachedRead, reasoning := extractResponsesUsage(responseData.Get("usage"))
		usage := map[string]any{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		}
		if cachedRead > 0 {
			usage["cache_read_input_tokens"] = cachedRead
		}
		if reasoning > 0 {
			usage["reasoning_tokens"] = reasoning
		}

		outputs = append(outputs, shared.SSEEvent("message_delta", map[string]any{
			"type":  "message_delta",
			"delta": delta,
			"usage": usage,
		}))
		outputs = append(outputs, shared.SSEEvent("message_stop", map[string]any{"type": "message_stop"}))
		state.SentMessageStop = true
	}

	return outputs, nil
}

func codexStreamErrorToClaudeSSE(root gjson.Result) []string {
	errorResult := root.Get("error")
	errType := strings.TrimSpace(errorResult.Get("type").String())
	if errType == "" {
		errType = strings.TrimSpace(root.Get("error_type").String())
	}
	if errType == "" {
		errType = "api_error"
	}
	code := strings.TrimSpace(errorResult.Get("code").String())
	message := strings.TrimSpace(errorResult.Get("message").String())
	if message == "" {
		message = strings.TrimSpace(root.Get("message").String())
	}
	if message == "" {
		message = code
	}
	if message == "" {
		message = errType
	}
	if code == "cyber_policy" || errType == "invalid_request" {
		errType = "invalid_request_error"
	}
	return []string{shared.SSEEvent("error", map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    errType,
			"message": message,
		},
	})}
}

func appendFunctionCallStart(originalRequestRawJSON []byte, callID, name string, blockIndex int) []string {
	return []string{shared.SSEEvent("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": blockIndex,
		"content_block": map[string]any{
			"type":  "tool_use",
			"id":    shortenCodexCallIDIfNeeded(sanitizeClaudeToolID(callID)),
			"name":  resolveClaudeToolUseName(originalRequestRawJSON, name),
			"input": map[string]any{},
		},
	})}
}

func appendFunctionCallArgumentDelta(partialJSON string, blockIndex int) []string {
	return []string{shared.SSEEvent("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": blockIndex,
		"delta": map[string]any{
			"type":         "input_json_delta",
			"partial_json": partialJSON,
		},
	})}
}

func appendFunctionCallStop(blockIndex int) []string {
	return []string{shared.SSEEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": blockIndex,
	})}
}

func hydrateOpenFunctionCallFromTerminal(outputs []string, state *responsesToClaudeStreamState, responseData gjson.Result) []string {
	if state == nil || !state.FunctionCallBlockOpen || state.HasReceivedArgumentsDelta {
		return outputs
	}
	for _, item := range responseData.Get("output").Array() {
		if item.Get("type").String() != "function_call" || functionCallID(item) != state.FunctionCallBlockCallID {
			continue
		}
		if args := item.Get("arguments").String(); args != "" {
			outputs = append(outputs, appendFunctionCallArgumentDelta(args, state.FunctionCallBlockIndex)...)
			state.HasReceivedArgumentsDelta = true
		}
		break
	}
	return outputs
}

func appendPendingFunctionCallsFromTerminal(outputs []string, state *responsesToClaudeStreamState, originalRequestRawJSON []byte, responseData gjson.Result) []string {
	if state == nil || len(state.PendingFunctionCalls) == 0 {
		return outputs
	}
	outputArr := responseData.Get("output")
	if !outputArr.IsArray() {
		clearPendingFunctionCalls(state)
		return outputs
	}
	for i, item := range outputArr.Array() {
		if item.Get("type").String() != "function_call" {
			continue
		}
		// reconstruct output index as gjson-like
		idxResult := gjson.Parse(fmt.Sprintf("%d", i))
		pending, pendingKeys := pendingFunctionCallForTerminalItem(state, idxResult, item)
		if pending == nil {
			continue
		}
		if pending.StartEmitted {
			deletePendingFunctionCallAliases(state, pendingKeys)
			continue
		}
		name := item.Get("name").String()
		if name == "" {
			deletePendingFunctionCallAliases(state, pendingKeys)
			continue
		}
		callID := pending.CallID
		if callID == "" {
			callID = functionCallID(item)
		}
		blockIndex := state.BlockIndex
		outputs = append(outputs, appendFunctionCallStart(originalRequestRawJSON, callID, name, blockIndex)...)
		state.HasEmittedToolUse = true
		pending.StartEmitted = true
		args := item.Get("arguments").String()
		if args == "" {
			args = pending.Arguments
		}
		if args != "" {
			outputs = append(outputs, appendFunctionCallArgumentDelta(args, blockIndex)...)
		}
		outputs = append(outputs, appendFunctionCallStop(blockIndex)...)
		state.BlockIndex++
		deletePendingFunctionCallAliases(state, pendingKeys)
	}
	clearPendingFunctionCalls(state)
	return outputs
}

func startClaudeThinkingBlock(state *responsesToClaudeStreamState) []string {
	if state == nil || state.ThinkingBlockOpen {
		return nil
	}
	outs := stopClaudeTextBlock(state)
	outs = append(outs, shared.SSEEvent("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": state.BlockIndex,
		"content_block": map[string]any{
			"type":     "thinking",
			"thinking": "",
		},
	}))
	state.ThinkingBlockOpen = true
	state.ThinkingStopPending = false
	return outs
}

func finalizeSignatureOnlyThinkingBlock(state *responsesToClaudeStreamState) []string {
	if state == nil || state.ThinkingSignature == "" {
		return nil
	}
	outs := startClaudeThinkingBlock(state)
	outs = append(outs, finalizeClaudeThinkingBlock(state)...)
	return outs
}

func finalizeClaudeThinkingBlock(state *responsesToClaudeStreamState) []string {
	if state == nil || !state.ThinkingBlockOpen {
		return nil
	}
	outs := make([]string, 0, 2)
	if state.ThinkingSignature != "" {
		outs = append(outs, shared.SSEEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": state.BlockIndex,
			"delta": map[string]any{
				"type":      "signature_delta",
				"signature": state.ThinkingSignature,
			},
		}))
	}
	outs = append(outs, shared.SSEEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": state.BlockIndex,
	}))
	state.BlockIndex++
	state.ThinkingBlockOpen = false
	state.ThinkingStopPending = false
	return outs
}

func startClaudeTextBlock(state *responsesToClaudeStreamState) []string {
	if state == nil || state.TextBlockOpen {
		return nil
	}
	outs := finalizeClaudeThinkingBlock(state)
	outs = append(outs, shared.SSEEvent("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": state.BlockIndex,
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	}))
	state.TextBlockOpen = true
	return outs
}

func stopClaudeTextBlock(state *responsesToClaudeStreamState) []string {
	if state == nil || !state.TextBlockOpen {
		return nil
	}
	outs := []string{shared.SSEEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": state.BlockIndex,
	})}
	state.TextBlockOpen = false
	state.BlockIndex++
	return outs
}

func stopClaudeFunctionCallBlock(state *responsesToClaudeStreamState) []string {
	if state == nil || !state.FunctionCallBlockOpen {
		return nil
	}
	idx := state.FunctionCallBlockIndex
	outs := appendFunctionCallStop(idx)
	if state.BlockIndex <= idx {
		state.BlockIndex = idx + 1
	}
	state.FunctionCallBlockOpen = false
	state.FunctionCallBlockCallID = ""
	state.FunctionCallBlockIndex = 0
	state.HasReceivedArgumentsDelta = false
	return outs
}

func flushOpenClaudeBlocks(state *responsesToClaudeStreamState) []string {
	if state == nil {
		return nil
	}
	outs := make([]string, 0, 4)
	outs = append(outs, finalizeClaudeThinkingBlock(state)...)
	outs = append(outs, stopClaudeTextBlock(state)...)
	outs = append(outs, stopClaudeFunctionCallBlock(state)...)
	return outs
}
