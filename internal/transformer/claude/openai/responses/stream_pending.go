package responses

import (
	"github.com/tidwall/gjson"
)

type pendingFunctionCall struct {
	CallID                    string
	Arguments                 string
	HasReceivedArgumentsDelta bool
	StartEmitted              bool
}

func functionCallKey(rootResult, itemResult gjson.Result) string {
	if outputIndex := rootResult.Get("output_index"); outputIndex.Exists() {
		return "output:" + outputIndex.Raw
	}
	if callID := functionCallID(itemResult); callID != "" {
		return "call:" + callID
	}
	return "last"
}

func functionCallID(itemResult gjson.Result) string {
	return itemResult.Get("call_id").String()
}

func functionCallIDKey(callID string) string {
	if callID == "" {
		return ""
	}
	return "call:" + callID
}

func argumentsFunctionCallKey(state *responsesToClaudeStreamState, rootResult gjson.Result) string {
	if outputIndex := rootResult.Get("output_index"); outputIndex.Exists() {
		return "output:" + outputIndex.Raw
	}
	return state.LastPendingFunctionCallKey
}

func recordPendingFunctionCall(state *responsesToClaudeStreamState, rootResult, itemResult gjson.Result) {
	if state.PendingFunctionCalls == nil {
		state.PendingFunctionCalls = map[string]*pendingFunctionCall{}
	}
	pending := &pendingFunctionCall{CallID: functionCallID(itemResult)}
	key := functionCallKey(rootResult, itemResult)
	state.PendingFunctionCalls[key] = pending
	if callIDKey := functionCallIDKey(pending.CallID); callIDKey != "" {
		state.PendingFunctionCalls[callIDKey] = pending
	}
	state.LastPendingFunctionCallKey = key
}

func pendingFunctionCallForKey(state *responsesToClaudeStreamState, key string) (*pendingFunctionCall, string) {
	if state == nil || state.PendingFunctionCalls == nil || key == "" {
		return nil, ""
	}
	pending, ok := state.PendingFunctionCalls[key]
	if !ok {
		return nil, ""
	}
	return pending, key
}

func pendingFunctionCallForDone(state *responsesToClaudeStreamState, rootResult, itemResult gjson.Result) (*pendingFunctionCall, []string) {
	if state == nil || state.PendingFunctionCalls == nil {
		return nil, nil
	}
	keys := []string{functionCallKey(rootResult, itemResult)}
	callID := functionCallID(itemResult)
	if callID != "" {
		keys = appendUniqueKey(keys, functionCallIDKey(callID))
	} else if !rootResult.Get("output_index").Exists() && state.LastPendingFunctionCallKey != "" {
		keys = appendUniqueKey(keys, state.LastPendingFunctionCallKey)
	}
	for _, key := range keys {
		if pending, ok := state.PendingFunctionCalls[key]; ok {
			return pending, keysForPendingFunctionCall(state, pending)
		}
	}
	return nil, nil
}

func appendUniqueKey(keys []string, key string) []string {
	if key == "" {
		return keys
	}
	for _, existing := range keys {
		if existing == key {
			return keys
		}
	}
	return append(keys, key)
}

func keysForPendingFunctionCall(state *responsesToClaudeStreamState, pending *pendingFunctionCall) []string {
	if state == nil || pending == nil || state.PendingFunctionCalls == nil {
		return nil
	}
	keys := make([]string, 0, 2)
	for key, candidate := range state.PendingFunctionCalls {
		if candidate == pending {
			keys = append(keys, key)
		}
	}
	return keys
}

func deletePendingFunctionCallAliases(state *responsesToClaudeStreamState, keys []string) {
	if state == nil || state.PendingFunctionCalls == nil {
		return
	}
	for _, key := range keys {
		delete(state.PendingFunctionCalls, key)
		if state.LastPendingFunctionCallKey == key {
			state.LastPendingFunctionCallKey = ""
		}
	}
}

func clearPendingFunctionCalls(state *responsesToClaudeStreamState) {
	if state == nil || state.PendingFunctionCalls == nil {
		return
	}
	for key := range state.PendingFunctionCalls {
		delete(state.PendingFunctionCalls, key)
	}
	state.LastPendingFunctionCallKey = ""
}

func pendingFunctionCallForTerminalItem(state *responsesToClaudeStreamState, outputIndex, item gjson.Result) (*pendingFunctionCall, []string) {
	if state == nil || state.PendingFunctionCalls == nil {
		return nil, nil
	}
	keys := make([]string, 0, 3)
	if callID := functionCallID(item); callID != "" {
		keys = appendUniqueKey(keys, functionCallIDKey(callID))
	}
	if itemOutputIndex := item.Get("output_index"); itemOutputIndex.Exists() {
		keys = appendUniqueKey(keys, "output:"+itemOutputIndex.Raw)
	}
	if outputIndex.Exists() {
		keys = appendUniqueKey(keys, "output:"+outputIndex.Raw)
	}
	for _, key := range keys {
		if pending, ok := state.PendingFunctionCalls[key]; ok {
			return pending, keysForPendingFunctionCall(state, pending)
		}
	}
	return nil, nil
}

func resolveClaudeToolUseName(originalRequestRawJSON []byte, name string) string {
	rev := buildReverseMapFromClaudeOriginalShortToOriginal(originalRequestRawJSON)
	if orig, ok := rev[name]; ok {
		return orig
	}
	return name
}
