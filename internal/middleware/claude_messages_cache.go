package middleware

import (
	"encoding/json"
	"strconv"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ensureCacheControl 自动注入 cache_control 断点以启用 prompt caching。
// 注入策略（Anthropic 评估顺序）: last tool → last system → 倒数第二 user turn。
func ensureCacheControl(body []byte) []byte {
	body = injectToolsCacheControl(body)
	body = injectSystemCacheControl(body)
	body = injectMessagesCacheControl(body)
	return body
}

// enforceCacheControlLimit 确保 cache_control 断点不超过 maxBlocks（Anthropic 限制为 4）。
// 移除优先级（从低价值到高价值）:
// 1. system 块（保留最后一个）2. tool 块（保留最后一个）
// 3. message content 块  4. 剩余 system  5. 剩余 tool
func enforceCacheControlLimit(body []byte, maxBlocks int) []byte {
	root, ok := parsePayloadMap(body)
	if !ok {
		return body
	}
	total := countCacheControlsInMap(root)
	if total <= maxBlocks {
		return body
	}
	excess := total - maxBlocks

	system, _ := asSlice(root["system"])
	tools, _ := asSlice(root["tools"])
	messages, _ := asSlice(root["messages"])

	// Phase 1-5: 按优先级逐步移除
	phases := []struct {
		arr       []any
		keepLast  bool
		isMessage bool
	}{
		{system, true, false},
		{tools, true, false},
		{messages, false, true},
		{system, false, false},
		{tools, false, false},
	}
	for _, ph := range phases {
		if excess <= 0 {
			break
		}
		if ph.isMessage {
			stripMessageCacheControl(ph.arr, &excess)
		} else if ph.keepLast {
			last := findLastCacheControlIndex(ph.arr)
			stripCacheControlExceptIndex(ph.arr, last, &excess)
		} else {
			stripAllCacheControl(ph.arr, &excess)
		}
	}
	return marshalPayloadMap(body, root)
}

// normalizeCacheControlTTL 确保 TTL 值符合 prompt-caching-scope 的排序约束:
// 1h-TTL 块不能出现在 5m-TTL 块之后（按评估顺序: tools → system → messages）。
func normalizeCacheControlTTL(body []byte) []byte {
	root, ok := parsePayloadMap(body)
	if !ok {
		return body
	}
	seen5m := false
	modified := false

	// 按评估顺序遍历
	if tools, ok := asSlice(root["tools"]); ok {
		for _, t := range tools {
			if obj, ok := t.(map[string]any); ok {
				if normalizeTTLForBlock(obj, &seen5m) {
					modified = true
				}
			}
		}
	}
	if system, ok := asSlice(root["system"]); ok {
		for _, s := range system {
			if obj, ok := s.(map[string]any); ok {
				if normalizeTTLForBlock(obj, &seen5m) {
					modified = true
				}
			}
		}
	}
	if messages, ok := asSlice(root["messages"]); ok {
		for _, m := range messages {
			msg, ok := m.(map[string]any)
			if !ok {
				continue
			}
			content, ok := asSlice(msg["content"])
			if !ok {
				continue
			}
			for _, c := range content {
				if obj, ok := c.(map[string]any); ok {
					if normalizeTTLForBlock(obj, &seen5m) {
						modified = true
					}
				}
			}
		}
	}
	if !modified {
		return body
	}
	return marshalPayloadMap(body, root)
}

// --- 内部辅助函数 ---

// injectToolsCacheControl 在最后一个 tool 上注入 cache_control（仅当无已有 cache_control 时）。
func injectToolsCacheControl(body []byte) []byte {
	tools := gjson.GetBytes(body, "tools")
	if !tools.Exists() || !tools.IsArray() {
		return body
	}
	arr := tools.Array()
	if len(arr) == 0 {
		return body
	}
	for _, t := range arr {
		if t.Get("cache_control").Exists() {
			return body
		}
	}
	lastIdx := len(arr) - 1
	path := "tools." + itoa(lastIdx) + ".cache_control"
	body, _ = sjson.SetBytes(body, path, map[string]string{"type": "ephemeral"})
	return body
}

// injectSystemCacheControl 在最后一个 system 元素上注入 cache_control。
// 如果 system 是字符串，先转换为数组格式。
func injectSystemCacheControl(body []byte) []byte {
	sysResult := gjson.GetBytes(body, "system")
	if !sysResult.Exists() {
		return body
	}
	// string → array 转换
	if sysResult.Type == gjson.String {
		arr := []map[string]any{{
			"type":          "text",
			"text":          sysResult.String(),
			"cache_control": map[string]string{"type": "ephemeral"},
		}}
		body, _ = sjson.SetBytes(body, "system", arr)
		return body
	}
	if !sysResult.IsArray() {
		return body
	}
	arr := sysResult.Array()
	if len(arr) == 0 {
		return body
	}
	for _, s := range arr {
		if s.Get("cache_control").Exists() {
			return body
		}
	}
	lastIdx := len(arr) - 1
	path := "system." + itoa(lastIdx) + ".cache_control"
	body, _ = sjson.SetBytes(body, path, map[string]string{"type": "ephemeral"})
	return body
}

// injectMessagesCacheControl 在倒数第二个 user turn 的最后一个 content 元素上注入 cache_control。
func injectMessagesCacheControl(body []byte) []byte {
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return body
	}
	arr := messages.Array()
	// 查找倒数第二个 user message
	userIndices := make([]int, 0, 4)
	for i, m := range arr {
		if m.Get("role").String() == "user" {
			userIndices = append(userIndices, i)
		}
	}
	if len(userIndices) < 2 {
		return body
	}
	targetIdx := userIndices[len(userIndices)-2]

	// 检查该消息 content 中是否已有 cache_control
	content := arr[targetIdx].Get("content")
	if content.IsArray() {
		for _, c := range content.Array() {
			if c.Get("cache_control").Exists() {
				return body
			}
		}
		lastContentIdx := len(content.Array()) - 1
		if lastContentIdx >= 0 {
			path := "messages." + itoa(targetIdx) + ".content." + itoa(lastContentIdx) + ".cache_control"
			body, _ = sjson.SetBytes(body, path, map[string]string{"type": "ephemeral"})
		}
	}
	return body
}

func normalizeTTLForBlock(obj map[string]any, seen5m *bool) bool {
	ccRaw, exists := obj["cache_control"]
	if !exists {
		return false
	}
	cc, ok := ccRaw.(map[string]any)
	if !ok {
		*seen5m = true
		return false
	}
	ttl, _ := cc["ttl"].(string)
	if ttl != "1h" {
		*seen5m = true
		return false
	}
	if *seen5m {
		delete(cc, "ttl") // 降级为默认 5m
		return true
	}
	return false
}

func countCacheControlsInMap(root map[string]any) int {
	count := 0
	if system, ok := asSlice(root["system"]); ok {
		for _, s := range system {
			if obj, ok := s.(map[string]any); ok {
				if _, has := obj["cache_control"]; has {
					count++
				}
			}
		}
	}
	if tools, ok := asSlice(root["tools"]); ok {
		for _, t := range tools {
			if obj, ok := t.(map[string]any); ok {
				if _, has := obj["cache_control"]; has {
					count++
				}
			}
		}
	}
	if messages, ok := asSlice(root["messages"]); ok {
		for _, m := range messages {
			msg, ok := m.(map[string]any)
			if !ok {
				continue
			}
			content, ok := asSlice(msg["content"])
			if !ok {
				continue
			}
			for _, c := range content {
				if obj, ok := c.(map[string]any); ok {
					if _, has := obj["cache_control"]; has {
						count++
					}
				}
			}
		}
	}
	return count
}

func findLastCacheControlIndex(arr []any) int {
	last := -1
	for i, item := range arr {
		if obj, ok := item.(map[string]any); ok {
			if _, has := obj["cache_control"]; has {
				last = i
			}
		}
	}
	return last
}

func stripCacheControlExceptIndex(arr []any, keepIdx int, excess *int) {
	for i, item := range arr {
		if *excess <= 0 {
			return
		}
		if i == keepIdx {
			continue
		}
		if obj, ok := item.(map[string]any); ok {
			if _, has := obj["cache_control"]; has {
				delete(obj, "cache_control")
				*excess--
			}
		}
	}
}

func stripAllCacheControl(arr []any, excess *int) {
	for _, item := range arr {
		if *excess <= 0 {
			return
		}
		if obj, ok := item.(map[string]any); ok {
			if _, has := obj["cache_control"]; has {
				delete(obj, "cache_control")
				*excess--
			}
		}
	}
}

func stripMessageCacheControl(messages []any, excess *int) {
	for _, m := range messages {
		if *excess <= 0 {
			return
		}
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		content, ok := asSlice(msg["content"])
		if !ok {
			continue
		}
		for _, c := range content {
			if *excess <= 0 {
				return
			}
			if obj, ok := c.(map[string]any); ok {
				if _, has := obj["cache_control"]; has {
					delete(obj, "cache_control")
					*excess--
				}
			}
		}
	}
}

// --- 通用工具函数 ---

func parsePayloadMap(body []byte) (map[string]any, bool) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, false
	}
	return m, true
}

func marshalPayloadMap(original []byte, m map[string]any) []byte {
	data, err := json.Marshal(m)
	if err != nil {
		return original
	}
	return data
}

func asSlice(v any) ([]any, bool) {
	s, ok := v.([]any)
	return s, ok && len(s) > 0
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
