package backend

import (
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

// ReplayScope 标识 (model, session) 连续对话边界；不绑定账号。
type ReplayScope struct {
	ModelName  string
	SessionKey string
}

func (s ReplayScope) Valid() bool {
	return strings.TrimSpace(s.ModelName) != "" && strings.TrimSpace(s.SessionKey) != ""
}

func reasoningReplayCacheKey(modelName, sessionKey string) string {
	modelName = strings.TrimSpace(modelName)
	sessionKey = strings.TrimSpace(sessionKey)
	if modelName == "" || sessionKey == "" {
		return ""
	}
	return strings.Join([]string{"xai-reasoning-replay", modelName, sessionKey}, "\x00")
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

// GetReasoningReplayItems 读取并滑动续期。
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

// DeleteReasoningReplayItems 删除条目。
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
		if encrypted != strings.TrimSpace(encrypted) || InspectGrokEncryptedContent(encrypted) != nil {
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
