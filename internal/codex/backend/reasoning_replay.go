package backend

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	ReasoningReplayCacheTTL            = 1 * time.Hour
	ReasoningReplayCacheMaxEntries     = 10240
	ReasoningReplayCacheEvictBatchSize = 128
)

type reasoningReplayEntry struct {
	Items     [][]byte
	Timestamp time.Time
}

var (
	reasoningReplayMu      sync.Mutex
	reasoningReplayEntries = make(map[string]reasoningReplayEntry)
)

// ReplayScope 标识一次可缓存的 Claude→Codex 会话。
type ReplayScope struct {
	ModelName  string
	SessionKey string
}

func (s ReplayScope) Valid() bool {
	return strings.TrimSpace(s.ModelName) != "" && strings.TrimSpace(s.SessionKey) != ""
}

func reasoningReplayCacheKey(modelName, sessionKey string) string {
	modelName = BaseModelName(modelName)
	sessionKey = strings.TrimSpace(sessionKey)
	if modelName == "" || sessionKey == "" {
		return ""
	}
	return strings.Join([]string{"codex-reasoning-replay", modelName, sessionKey}, "\x00")
}

// CacheReasoningReplayItems 写入归一化后的 replay items。
func CacheReasoningReplayItems(modelName, sessionKey string, items [][]byte) bool {
	key := reasoningReplayCacheKey(modelName, sessionKey)
	if key == "" {
		return false
	}
	normalized, ok := normalizeReasoningReplayItems(items)
	if !ok {
		return false
	}
	now := time.Now()
	reasoningReplayMu.Lock()
	defer reasoningReplayMu.Unlock()
	reasoningReplayEntries[key] = reasoningReplayEntry{Items: normalized, Timestamp: now}
	if len(reasoningReplayEntries) > ReasoningReplayCacheMaxEntries {
		evictOldestReasoningReplayEntriesLocked(ReasoningReplayCacheEvictBatchSize)
	}
	return true
}

// GetReasoningReplayItems 读取缓存。
func GetReasoningReplayItems(modelName, sessionKey string) ([][]byte, bool) {
	key := reasoningReplayCacheKey(modelName, sessionKey)
	if key == "" {
		return nil, false
	}
	now := time.Now()
	reasoningReplayMu.Lock()
	defer reasoningReplayMu.Unlock()
	entry, ok := reasoningReplayEntries[key]
	if !ok {
		return nil, false
	}
	if now.Sub(entry.Timestamp) > ReasoningReplayCacheTTL {
		delete(reasoningReplayEntries, key)
		return nil, false
	}
	entry.Timestamp = now
	reasoningReplayEntries[key] = entry
	return cloneReasoningReplayItems(entry.Items), true
}

// DeleteReasoningReplayItems 删除缓存。
func DeleteReasoningReplayItems(modelName, sessionKey string) {
	key := reasoningReplayCacheKey(modelName, sessionKey)
	if key == "" {
		return
	}
	reasoningReplayMu.Lock()
	delete(reasoningReplayEntries, key)
	reasoningReplayMu.Unlock()
}

// ClearReasoningReplayCache 测试用。
func ClearReasoningReplayCache() {
	reasoningReplayMu.Lock()
	reasoningReplayEntries = make(map[string]reasoningReplayEntry)
	reasoningReplayMu.Unlock()
}

func normalizeReasoningReplayItems(items [][]byte) ([][]byte, bool) {
	normalized := make([][]byte, 0, len(items))
	for _, item := range items {
		if n, ok := normalizeReasoningReplayItem(item); ok {
			normalized = append(normalized, n)
		}
	}
	return normalized, len(normalized) > 0
}

func normalizeReasoningReplayItem(item []byte) ([]byte, bool) {
	itemResult := gjson.ParseBytes(item)
	switch strings.TrimSpace(itemResult.Get("type").String()) {
	case "reasoning":
		enc := itemResult.Get("encrypted_content")
		if enc.Type != gjson.String {
			return nil, false
		}
		encrypted := enc.String()
		if encrypted != strings.TrimSpace(encrypted) || InspectGPTReasoningSignature(encrypted) != nil {
			return nil, false
		}
		out := []byte(`{"type":"reasoning","summary":[],"content":null}`)
		out, _ = sjson.SetBytes(out, "encrypted_content", encrypted)
		return out, true
	case "function_call":
		callID := strings.TrimSpace(itemResult.Get("call_id").String())
		name := strings.TrimSpace(itemResult.Get("name").String())
		arguments := itemResult.Get("arguments")
		if callID == "" || name == "" || arguments.Type != gjson.String {
			return nil, false
		}
		out := []byte(`{"type":"function_call"}`)
		out, _ = sjson.SetBytes(out, "call_id", callID)
		out, _ = sjson.SetBytes(out, "name", name)
		out, _ = sjson.SetBytes(out, "arguments", arguments.String())
		return out, true
	case "custom_tool_call":
		callID := strings.TrimSpace(itemResult.Get("call_id").String())
		name := strings.TrimSpace(itemResult.Get("name").String())
		input := itemResult.Get("input")
		if callID == "" || name == "" || !input.Exists() {
			return nil, false
		}
		out := []byte(`{"type":"custom_tool_call","status":"completed"}`)
		if status := strings.TrimSpace(itemResult.Get("status").String()); status != "" {
			out, _ = sjson.SetBytes(out, "status", status)
		}
		out, _ = sjson.SetBytes(out, "call_id", callID)
		out, _ = sjson.SetBytes(out, "name", name)
		if input.Type == gjson.String {
			out, _ = sjson.SetBytes(out, "input", input.String())
		} else {
			out, _ = sjson.SetRawBytes(out, "input", []byte(input.Raw))
		}
		return out, true
	default:
		return nil, false
	}
}

func cloneReasoningReplayItems(items [][]byte) [][]byte {
	out := make([][]byte, 0, len(items))
	for _, item := range items {
		out = append(out, append([]byte(nil), item...))
	}
	return out
}

func evictOldestReasoningReplayEntriesLocked(count int) {
	if count <= 0 || len(reasoningReplayEntries) == 0 {
		return
	}
	type candidate struct {
		key       string
		timestamp time.Time
	}
	candidates := make([]candidate, 0, len(reasoningReplayEntries))
	for key, entry := range reasoningReplayEntries {
		candidates = append(candidates, candidate{key: key, timestamp: entry.Timestamp})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].timestamp.Before(candidates[j].timestamp)
	})
	if count > len(candidates) {
		count = len(candidates)
	}
	for i := 0; i < count; i++ {
		delete(reasoningReplayEntries, candidates[i].key)
	}
}

// ApplyReasoningReplay 注入缓存的 reasoning / tool_call（仅 Claude 源启用）。
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

// CacheReasoningReplayFromCompleted 从 response.completed 或 response 对象写缓存。
func CacheReasoningReplayFromCompleted(scope ReplayScope, completedData []byte) {
	if !scope.Valid() || len(completedData) == 0 {
		return
	}
	output := gjson.GetBytes(completedData, "response.output")
	if !output.IsArray() {
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

// ClearReasoningReplayOnInvalidSignature 上游拒绝 thinking signature 时清缓存。
func ClearReasoningReplayOnInvalidSignature(scope ReplayScope, statusCode int, body []byte) {
	if !scope.Valid() {
		return
	}
	if IsThinkingSignatureInvalid(statusCode, body) {
		DeleteReasoningReplayItems(scope.ModelName, scope.SessionKey)
	}
}

// IsThinkingSignatureInvalid 识别 invalid encrypted reasoning 错误。
func IsThinkingSignatureInvalid(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest && statusCode != 0 {
		// 仍检查 body 文本
	}
	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "invalid signature in thinking block") ||
		strings.Contains(lower, "invalid_encrypted_content") ||
		strings.Contains(lower, "thinking_signature_invalid")
}

// ResolveReplaySessionKey 解析连续对话 key。
func ResolveReplaySessionKey(body []byte, headers http.Header, explicit string) string {
	return ResolveReplaySessionKeyWithClaude(body, nil, headers, explicit)
}

// ResolveReplaySessionKeyWithClaude 优先用 Claude 原始 payload 解析 session，并做 caller 隔离。
func ResolveReplaySessionKeyWithClaude(body, originalClaudeBody []byte, headers http.Header, explicit string) string {
	key := resolveReplaySessionKeyRaw(body, originalClaudeBody, headers, explicit)
	return IsolateReplaySessionKey(key, CallerAPIKeyFromHeaders(headers))
}

func resolveReplaySessionKeyRaw(body, originalClaudeBody []byte, headers http.Header, explicit string) string {
	if v := strings.TrimSpace(explicit); v != "" {
		if strings.Contains(v, ":") {
			return v
		}
		return "session:" + v
	}
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
		if turn := headerValueCaseInsensitive(headers, "X-Codex-Turn-Metadata"); turn != "" {
			if k := strings.TrimSpace(gjson.Get(turn, "prompt_cache_key").String()); k != "" {
				return "prompt-cache:" + k
			}
			if k := strings.TrimSpace(gjson.Get(turn, "window_id").String()); k != "" {
				return "window:" + k
			}
		}
		if k := headerValueCaseInsensitive(headers, "X-Codex-Window-Id"); k != "" {
			return "window:" + k
		}
		for _, name := range []string{"Session_id", "session_id", "Session-Id", "x-session-id"} {
			if k := headerValueCaseInsensitive(headers, name); k != "" {
				return "session-id:" + k
			}
		}
		if k := headerValueCaseInsensitive(headers, "Conversation_id"); k != "" {
			return "conversation_id:" + k
		}
	}
	return ""
}

var claudeCodeSessionSuffixPattern = regexp.MustCompile(`_session_([a-f0-9-]+)$`)

// ExtractClaudeCodeSessionID 从 Claude metadata / headers 提取 session。
func ExtractClaudeCodeSessionID(payload []byte, headers http.Header) string {
	if sid := extractClaudeCodeSessionIDFromPayload(payload); sid != "" {
		return sid
	}
	if headers == nil {
		return ""
	}
	for _, name := range []string{"X-Claude-Code-Session-Id", "anthropic-beta", "X-Session-Id"} {
		if v := headerValueCaseInsensitive(headers, name); v != "" {
			if matches := claudeCodeSessionSuffixPattern.FindStringSubmatch(v); len(matches) >= 2 {
				return matches[1]
			}
		}
	}
	return ""
}

func extractClaudeCodeSessionIDFromPayload(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	userID := strings.TrimSpace(gjson.GetBytes(payload, "metadata.user_id").String())
	if userID == "" {
		userID = strings.TrimSpace(gjson.GetBytes(payload, "metadata.userId").String())
	}
	if matches := claudeCodeSessionSuffixPattern.FindStringSubmatch(userID); len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

// ClaudeReplaySessionKey 返回 "claude:<session_id>"。
func ClaudeReplaySessionKey(payload []byte, headers http.Header) string {
	sid := ExtractClaudeCodeSessionID(payload, headers)
	if sid == "" {
		return ""
	}
	return "claude:" + sid
}

// CallerAPIKeyFromHeaders 提取下游调用方 API key。
func CallerAPIKeyFromHeaders(h http.Header) string {
	if h == nil {
		return ""
	}
	if k := headerValueCaseInsensitive(h, "x-api-key"); k != "" {
		return k
	}
	auth := headerValueCaseInsensitive(h, "Authorization")
	if auth == "" {
		return ""
	}
	const prefix = "bearer "
	if len(auth) > len(prefix) && strings.EqualFold(auth[:len(prefix)], prefix) {
		return strings.TrimSpace(auth[len(prefix):])
	}
	return ""
}

// IsolateReplaySessionKey 按 caller API key 隔离 client-controlled session。
func IsolateReplaySessionKey(sessionKey, callerAPIKey string) string {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return ""
	}
	if strings.HasPrefix(sessionKey, "execution:") || strings.HasPrefix(sessionKey, "caller:") {
		return sessionKey
	}
	callerAPIKey = strings.TrimSpace(callerAPIKey)
	if callerAPIKey == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(callerAPIKey))
	return "caller:" + hex.EncodeToString(sum[:8]) + ":" + sessionKey
}

func inputHasValidGPTReasoningEncrypted(body []byte) bool {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return false
	}
	for _, item := range input.Array() {
		if strings.TrimSpace(item.Get("type").String()) != "reasoning" {
			continue
		}
		enc := item.Get("encrypted_content")
		if enc.Type == gjson.String && IsValidGPTReasoningSignature(enc.String()) {
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
	hasInputReasoning := inputHasValidGPTReasoningEncrypted(body)
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
		for i, item := range inputItems {
			itemType := strings.TrimSpace(item.Get("type").String())
			if itemType != "function_call_output" && itemType != "custom_tool_call_output" {
				continue
			}
			callID := strings.TrimSpace(item.Get("call_id").String())
			for _, candidate := range replayComparableCallIDs(callID) {
				if replayCallIDs[candidate] {
					return i
				}
			}
		}
	}
	for i, item := range inputItems {
		if shouldInsertReasoningReplayBefore(item) {
			return i
		}
	}
	return len(inputItems)
}

func shouldInsertReasoningReplayBefore(item gjson.Result) bool {
	itemType := strings.TrimSpace(item.Get("type").String())
	switch itemType {
	case "function_call_output", "custom_tool_call_output":
		return true
	case "message":
		role := strings.TrimSpace(item.Get("role").String())
		return role == "assistant" || role == ""
	default:
		return false
	}
}

func alignReasoningReplayToolCallIDs(inputItems []gjson.Result, replayItems [][]byte) [][]byte {
	outputCallIDs := make(map[string]string)
	for _, item := range inputItems {
		itemType := strings.TrimSpace(item.Get("type").String())
		if itemType != "function_call_output" && itemType != "custom_tool_call_output" {
			continue
		}
		callID := strings.TrimSpace(item.Get("call_id").String())
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
	out := make([][]byte, 0, len(replayItems))
	for _, item := range replayItems {
		itemResult := gjson.ParseBytes(item)
		itemType := strings.TrimSpace(itemResult.Get("type").String())
		if itemType != "function_call" && itemType != "custom_tool_call" {
			out = append(out, item)
			continue
		}
		callID := strings.TrimSpace(itemResult.Get("call_id").String())
		matched := ""
		for _, candidate := range replayComparableCallIDs(callID) {
			if v, ok := outputCallIDs[candidate]; ok {
				matched = v
				break
			}
		}
		if matched == "" || matched == callID {
			out = append(out, item)
			continue
		}
		updated, err := sjson.SetBytes(item, "call_id", matched)
		if err != nil {
			out = append(out, item)
			continue
		}
		out = append(out, updated)
	}
	return out
}

func replayToolCallKeys(item gjson.Result) []string {
	itemType := strings.TrimSpace(item.Get("type").String())
	if itemType != "function_call" && itemType != "custom_tool_call" {
		return nil
	}
	keys := make([]string, 0, 4)
	callID := strings.TrimSpace(item.Get("call_id").String())
	for _, candidate := range replayComparableCallIDs(callID) {
		keys = append(keys, "call:"+candidate)
	}
	if name := strings.TrimSpace(item.Get("name").String()); name != "" {
		keys = append(keys, "name:"+name)
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
	out := []string{callID}
	if short := shortenCodexCallIDIfNeeded(callID); short != callID {
		out = append(out, short)
	}
	return out
}

func shortenCodexCallIDIfNeeded(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || len(id) <= 40 {
		return id
	}
	// 保留前缀 + 哈希尾
	sum := sha256.Sum256([]byte(id))
	prefix := id
	if i := strings.IndexByte(id, '_'); i > 0 && i < 12 {
		prefix = id[:i+1]
	} else {
		prefix = "call_"
	}
	return prefix + hex.EncodeToString(sum[:8])
}

// IsClaudeSource 判断是否 Claude→Codex 路径。
func IsClaudeSource(source string) bool {
	s := strings.ToLower(strings.TrimSpace(source))
	return s == "claude" || s == SourceClaude
}
