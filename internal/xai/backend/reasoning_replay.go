package backend

// Reasoning replay / normalize。

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"math"
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
	maxGrokEncryptedContentLen          = 8 * 1024 * 1024
	minGrokEncryptedContentDecodedLen   = 50
	minGrokEncryptedContentEntropyRatio = 0.85
)

// InspectGrokEncryptedContent 校验 Grok reasoning/compaction encrypted_content 传输形态。
// 拒 GPT/Codex、Claude、Gemini 异源签名。
func InspectGrokEncryptedContent(raw string) error {
	sig := strings.TrimSpace(raw)
	if sig == "" {
		return fmt.Errorf("empty Grok encrypted_content")
	}
	if len(sig) > maxGrokEncryptedContentLen {
		return fmt.Errorf("Grok encrypted_content exceeds maximum length")
	}
	if sig != raw {
		return fmt.Errorf("Grok encrypted_content has leading or trailing whitespace")
	}
	if strings.HasPrefix(sig, "gAAAA") {
		return fmt.Errorf("Grok encrypted_content looks like GPT/Codex reasoning signature")
	}
	if strings.Contains(sig, "=") {
		return fmt.Errorf("invalid Grok encrypted_content: expected unpadded standard base64")
	}
	for index, r := range sig {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '+', r == '/':
		default:
			return fmt.Errorf("invalid Grok encrypted_content: non-base64 character at %d", index)
		}
	}
	// 异源快速拒绝：须在熵校验前，避免「看起来像密文」的 Claude/Gemini 漏过。
	if looksLikeClaudeThinkingSignature(sig) {
		return fmt.Errorf("Grok encrypted_content looks like Claude thinking signature")
	}
	if looksLikeGeminiThoughtSignature(sig) {
		return fmt.Errorf("Grok encrypted_content looks like Gemini thoughtSignature")
	}
	decoded, err := base64.RawStdEncoding.DecodeString(sig)
	if err != nil {
		return fmt.Errorf("invalid Grok encrypted_content: base64 decode failed: %w", err)
	}
	if len(decoded) < minGrokEncryptedContentDecodedLen {
		return fmt.Errorf("invalid Grok encrypted_content: decoded payload too short")
	}
	if byteEntropyRatio(decoded) < minGrokEncryptedContentEntropyRatio {
		return fmt.Errorf("invalid Grok encrypted_content: entropy too low")
	}
	return nil
}

// looksLikeClaudeThinkingSignature 识别 Claude E/R thinking signature（含无填充变体）。
// 要求：E/R 前缀 + 解码后 0x12 + Field2 容器 + channel_id ∈ {11,12}，避免误杀高熵 Grok blob。
func looksLikeClaudeThinkingSignature(sig string) bool {
	s := strings.TrimSpace(sig)
	if s == "" {
		return false
	}
	if idx := strings.IndexByte(s, '#'); idx >= 0 {
		s = strings.TrimSpace(s[idx+1:])
	}
	if s == "" {
		return false
	}
	var payload []byte
	switch s[0] {
	case 'E':
		decoded, ok := decodeStdOrRawBase64(s)
		if !ok || len(decoded) == 0 {
			return false
		}
		payload = decoded
	case 'R':
		outer, ok := decodeStdOrRawBase64(s)
		if !ok || len(outer) == 0 || outer[0] != 'E' {
			return false
		}
		inner, ok := decodeStdOrRawBase64(string(outer))
		if !ok || len(inner) == 0 {
			return false
		}
		payload = inner
	default:
		return false
	}
	return isClaudeThinkingPayload(payload)
}

// isClaudeThinkingPayload 校验 Claude 顶层 Field2 → channel Field1 → channel_id(11|12)。
func isClaudeThinkingPayload(payload []byte) bool {
	if len(payload) < 6 || payload[0] != 0x12 {
		return false
	}
	container, ok := extractProtoBytesField(payload, 2)
	if !ok || len(container) == 0 {
		return false
	}
	channel, ok := extractProtoBytesField(container, 1)
	if !ok || len(channel) == 0 {
		return false
	}
	channelID, ok := extractProtoVarintField(channel, 1)
	if !ok {
		return false
	}
	return channelID == 11 || channelID == 12
}

// extractProtoBytesField 从 protobuf 消息中取指定 field 的 length-delimited 值。
func extractProtoBytesField(msg []byte, fieldNum int) ([]byte, bool) {
	off := 0
	for off < len(msg) {
		tag, n := readProtoVarint(msg[off:])
		if n <= 0 {
			return nil, false
		}
		off += n
		num := int(tag >> 3)
		wire := int(tag & 7)
		switch wire {
		case 0: // varint
			_, n2 := readProtoVarint(msg[off:])
			if n2 <= 0 {
				return nil, false
			}
			off += n2
		case 1: // 64-bit
			if off+8 > len(msg) {
				return nil, false
			}
			off += 8
		case 2: // length-delimited
			ln, n2 := readProtoVarint(msg[off:])
			if n2 <= 0 {
				return nil, false
			}
			off += n2
			end := off + int(ln)
			if end > len(msg) {
				return nil, false
			}
			if num == fieldNum {
				return msg[off:end], true
			}
			off = end
		case 5: // 32-bit
			if off+4 > len(msg) {
				return nil, false
			}
			off += 4
		default:
			return nil, false
		}
	}
	return nil, false
}

func extractProtoVarintField(msg []byte, fieldNum int) (uint64, bool) {
	off := 0
	for off < len(msg) {
		tag, n := readProtoVarint(msg[off:])
		if n <= 0 {
			return 0, false
		}
		off += n
		num := int(tag >> 3)
		wire := int(tag & 7)
		switch wire {
		case 0:
			v, n2 := readProtoVarint(msg[off:])
			if n2 <= 0 {
				return 0, false
			}
			off += n2
			if num == fieldNum {
				return v, true
			}
		case 1:
			if off+8 > len(msg) {
				return 0, false
			}
			off += 8
		case 2:
			ln, n2 := readProtoVarint(msg[off:])
			if n2 <= 0 {
				return 0, false
			}
			off += n2
			end := off + int(ln)
			if end > len(msg) {
				return 0, false
			}
			off = end
		case 5:
			if off+4 > len(msg) {
				return 0, false
			}
			off += 4
		default:
			return 0, false
		}
	}
	return 0, false
}

// looksLikeGeminiThoughtSignature 识别 Gemini thoughtSignature 的已知 protobuf 包络。
func looksLikeGeminiThoughtSignature(sig string) bool {
	decoded, ok := decodeStdOrRawBase64(strings.TrimSpace(sig))
	if !ok || len(decoded) == 0 {
		return false
	}
	// 拒绝 ASCII UUID 形态（非 Grok 密文，也非合法 Gemini envelope，但也不应当 Grok）
	// 此处仅匹配已知 Gemini envelope（与参考 RequireKnownEnvelope 一致）。
	return isGeminiField1Envelope(decoded) || isGeminiField2Envelope(decoded)
}

func decodeStdOrRawBase64(s string) ([]byte, bool) {
	if s == "" {
		return nil, false
	}
	if d, err := base64.StdEncoding.DecodeString(s); err == nil {
		return d, true
	}
	if d, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return d, true
	}
	return nil, false
}

func readProtoVarint(b []byte) (uint64, int) {
	var x uint64
	var s uint
	for i := 0; i < len(b) && i < 10; i++ {
		c := b[i]
		if c < 0x80 {
			if i == 9 && c > 1 {
				return 0, -1
			}
			return x | uint64(c)<<s, i + 1
		}
		x |= uint64(c&0x7f) << s
		s += 7
	}
	return 0, -1
}

// isGeminiField2Envelope：field 2 length-delimited → 内层 field 1 length-delimited + 非空 opaque。
func isGeminiField2Envelope(decoded []byte) bool {
	if len(decoded) < 4 || decoded[0] != 0x12 {
		return false
	}
	n, size := readProtoVarint(decoded[1:])
	if size <= 0 {
		return false
	}
	start := 1 + size
	end := start + int(n)
	if end > len(decoded) || n < 2 {
		return false
	}
	inner := decoded[start:end]
	// 内层 field 1 (0x0a)
	if len(inner) < 2 || inner[0] != 0x0a {
		return false
	}
	n2, size2 := readProtoVarint(inner[1:])
	if size2 <= 0 {
		return false
	}
	start2 := 1 + size2
	end2 := start2 + int(n2)
	return end2 <= len(inner) && n2 > 0 && end == len(decoded)
}

// isGeminiField1Envelope：一个或多个 field 1 length-delimited 记录。
func isGeminiField1Envelope(decoded []byte) bool {
	if len(decoded) == 0 || decoded[0] != 0x0a {
		return false
	}
	records := 0
	off := 0
	for off < len(decoded) {
		if decoded[off] != 0x0a {
			return false
		}
		n, size := readProtoVarint(decoded[off+1:])
		if size <= 0 {
			return false
		}
		start := off + 1 + size
		end := start + int(n)
		if end > len(decoded) || n == 0 {
			return false
		}
		records++
		off = end
	}
	return records > 0 && off == len(decoded)
}

func IsValidGrokEncryptedContent(raw string) bool {
	return InspectGrokEncryptedContent(raw) == nil
}

func byteEntropyRatio(buf []byte) float64 {
	if len(buf) == 0 {
		return 0
	}
	var counts [256]int
	for _, b := range buf {
		counts[b]++
	}
	n := float64(len(buf))
	entropy := 0.0
	for _, count := range counts {
		if count == 0 {
			continue
		}
		p := float64(count) / n
		entropy -= p * math.Log2(p)
	}
	maxSymbols := len(buf)
	if maxSymbols > 256 {
		maxSymbols = 256
	}
	if maxSymbols <= 1 {
		return 0
	}
	return entropy / math.Log2(float64(maxSymbols))
}

const ClaudeCodeSessionHeader = "X-Claude-Code-Session-Id"

var claudeCodeSessionSuffixPattern = regexp.MustCompile(`_session_([a-f0-9-]+)$`)

// ExtractClaudeCodeSessionID 从 header / Claude payload 解析 session_id。
// 1) X-Claude-Code-Session-Id
// 2) metadata.user_id JSON 内 session_id
// 3) metadata.user_id 后缀 _session_<uuid>
func ExtractClaudeCodeSessionID(payload []byte, headers http.Header) string {
	if headers != nil {
		if sid := strings.TrimSpace(headers.Get(ClaudeCodeSessionHeader)); sid != "" {
			return sid
		}
		// 兼容大小写/别名
		if sid := headerGet(headers, ClaudeCodeSessionHeader); sid != "" {
			return sid
		}
	}
	return extractClaudeCodeSessionIDFromPayload(payload)
}

func extractClaudeCodeSessionIDFromPayload(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	userID := strings.TrimSpace(gjson.GetBytes(payload, "metadata.user_id").String())
	if userID == "" {
		return ""
	}
	if matches := claudeCodeSessionSuffixPattern.FindStringSubmatch(userID); len(matches) >= 2 {
		return matches[1]
	}
	if len(userID) > 0 && userID[0] == '{' {
		return strings.TrimSpace(gjson.Get(userID, "session_id").String())
	}
	return ""
}

// ClaudeReplaySessionKey 返回 "claude:<session_id>"（无 session 则空）。
func ClaudeReplaySessionKey(payload []byte, headers http.Header) string {
	sid := ExtractClaudeCodeSessionID(payload, headers)
	if sid == "" {
		return ""
	}
	return "claude:" + sid
}

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

// ApplyReasoningReplay 在 sanitize 之前注入缓存的 reasoning / tool_call。
// enable 通常仅 Claude 源打开。
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

// CacheReasoningReplayFromCompleted 从 response.completed 事件写缓存。
func CacheReasoningReplayFromCompleted(scope ReplayScope, completedData []byte) {
	if !scope.Valid() || len(completedData) == 0 {
		return
	}
	output := gjson.GetBytes(completedData, "response.output")
	if !output.IsArray() {
		// 也可能是已聚合的 response 对象
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

// ResolveReplaySessionKey 解析连续对话 key
// originalClaudeBody：Claude 原始 body（转换前），用于提取 session；可与 body 相同。
func ResolveReplaySessionKey(body []byte, headers http.Header, explicit string) string {
	return ResolveReplaySessionKeyWithClaude(body, nil, headers, explicit)
}

// ResolveReplaySessionKeyWithClaude 优先用 Claude 原始 payload 解析 session，并做 caller 隔离。
func ResolveReplaySessionKeyWithClaude(body, originalClaudeBody []byte, headers http.Header, explicit string) string {
	key := resolveReplaySessionKeyRaw(body, originalClaudeBody, headers, explicit)
	return IsolateReplaySessionKey(key, CallerAPIKeyFromHeaders(headers))
}

// resolveReplaySessionKeyRaw 解析未隔离的 session key（内部用）。
func resolveReplaySessionKeyRaw(body, originalClaudeBody []byte, headers http.Header, explicit string) string {
	if v := strings.TrimSpace(explicit); v != "" {
		// 若调用方已给出完整 key（claude:/prompt-cache:/caller:/execution:）则直接用
		if strings.Contains(v, ":") {
			return v
		}
		return "session:" + v
	}
	// 跨请求 execution session：可信前缀，Isolate 时不要求 caller API key
	if execID := ExecutionSessionIDFromHeaders(headers); execID != "" {
		return "execution:" + execID
	}
	// Claude Code session（必须在 Messages→Responses 转换前解析）
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
		if turn := headerGet(headers, "X-Codex-Turn-Metadata"); turn != "" {
			if k := strings.TrimSpace(gjson.Get(turn, "prompt_cache_key").String()); k != "" {
				return "prompt-cache:" + k
			}
			if k := strings.TrimSpace(gjson.Get(turn, "window_id").String()); k != "" {
				return "window:" + k
			}
		}
		if k := headerGet(headers, "X-Codex-Window-Id"); k != "" {
			return "window:" + k
		}
		for _, name := range []string{"Session_id", "session_id", "Session-Id", "x-session-id"} {
			if k := headerGet(headers, name); k != "" {
				return "session-id:" + k
			}
		}
		if k := headerGet(headers, "Conversation_id"); k != "" {
			return "conversation_id:" + k
		}
		// 不读入站 x-grok-conv-id：该头仅由本代理写出到上游，不可作为客户端可信会话源。
	}
	return ""
}

// CallerAPIKeyFromHeaders 提取下游调用方 API key（用于 replay 隔离）。
// 优先 x-api-key，其次 Authorization: Bearer。
func CallerAPIKeyFromHeaders(h http.Header) string {
	if h == nil {
		return ""
	}
	if k := headerGet(h, "x-api-key"); k != "" {
		return k
	}
	auth := headerGet(h, "Authorization")
	if auth == "" {
		return ""
	}
	const prefix = "bearer "
	if len(auth) > len(prefix) && strings.EqualFold(auth[:len(prefix)], prefix) {
		return strings.TrimSpace(auth[len(prefix):])
	}
	return ""
}

// IsolateReplaySessionKey 按 caller API key 命名空间隔离 client-controlled session，避免多租户串 reasoning 缓存。
// execution:/caller: 前缀视为已可信/已隔离；无 caller key 时禁用（返回空）而非全局共享。
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

func inputHasValidGrokReasoningEncrypted(body []byte) bool {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return false
	}
	for _, item := range input.Array() {
		if strings.TrimSpace(item.Get("type").String()) != "reasoning" {
			continue
		}
		enc := item.Get("encrypted_content")
		if enc.Type == gjson.String && IsValidGrokEncryptedContent(enc.String()) {
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
	hasInputReasoning := inputHasValidGrokReasoningEncrypted(body)
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
		for index, inputItem := range inputItems {
			itemType := strings.TrimSpace(inputItem.Get("type").String())
			if itemType != "function_call_output" && itemType != "custom_tool_call_output" {
				continue
			}
			callID := strings.TrimSpace(inputItem.Get("call_id").String())
			if callID == "" || replayCallIDs[callID] {
				return index
			}
		}
	}
	for index := len(inputItems) - 1; index >= 0; index-- {
		inputItem := inputItems[index]
		if strings.TrimSpace(inputItem.Get("type").String()) == "message" &&
			strings.TrimSpace(inputItem.Get("role").String()) == "assistant" {
			return index
		}
	}
	for index, inputItem := range inputItems {
		if shouldInsertReasoningReplayBefore(inputItem) {
			return index
		}
	}
	return len(inputItems)
}

func alignReasoningReplayToolCallIDs(inputItems []gjson.Result, replayItems [][]byte) [][]byte {
	outputCallIDs := make(map[string]string)
	for _, inputItem := range inputItems {
		itemType := strings.TrimSpace(inputItem.Get("type").String())
		if itemType != "function_call_output" && itemType != "custom_tool_call_output" {
			continue
		}
		callID := strings.TrimSpace(inputItem.Get("call_id").String())
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
	aligned := make([][]byte, 0, len(replayItems))
	for _, replayItem := range replayItems {
		itemResult := gjson.ParseBytes(replayItem)
		itemType := strings.TrimSpace(itemResult.Get("type").String())
		if itemType != "function_call" && itemType != "custom_tool_call" {
			aligned = append(aligned, replayItem)
			continue
		}
		callID := strings.TrimSpace(itemResult.Get("call_id").String())
		outputCallID := ""
		for _, candidate := range replayComparableCallIDs(callID) {
			if value := outputCallIDs[candidate]; value != "" {
				outputCallID = value
				break
			}
		}
		if outputCallID == "" || outputCallID == callID {
			aligned = append(aligned, replayItem)
			continue
		}
		updated, err := sjson.SetBytes(replayItem, "call_id", outputCallID)
		if err != nil {
			aligned = append(aligned, replayItem)
			continue
		}
		aligned = append(aligned, updated)
	}
	return aligned
}

func shouldInsertReasoningReplayBefore(item gjson.Result) bool {
	if strings.TrimSpace(item.Get("type").String()) != "message" {
		return true
	}
	switch strings.TrimSpace(item.Get("role").String()) {
	case "developer", "system":
		return false
	default:
		return true
	}
}

func replayToolCallKeys(item gjson.Result) []string {
	itemType := strings.TrimSpace(item.Get("type").String())
	if itemType != "function_call" && itemType != "custom_tool_call" {
		return nil
	}
	callIDs := replayComparableCallIDs(item.Get("call_id").String())
	if len(callIDs) == 0 {
		return nil
	}
	keys := make([]string, 0, len(callIDs))
	for _, callID := range callIDs {
		keys = append(keys, itemType+":"+callID)
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
	// Claude tool id 可能被截断到 64
	if len(callID) > 64 {
		return []string{callID, callID[:64]}
	}
	return []string{callID}
}

var (
	dataTag  = []byte("data:")
	eventTag = []byte("event:")
)

// NormalizeReasoningSummaryData 将 xAI reasoning_text 形态转为 Codex summary 形态。
func NormalizeReasoningSummaryData(eventData []byte) []byte {
	if len(eventData) == 0 || !gjson.ValidBytes(eventData) {
		return eventData
	}

	normalized := eventData
	switch gjson.GetBytes(normalized, "type").String() {
	case "response.reasoning_text.delta":
		normalized, _ = sjson.SetBytes(normalized, "type", "response.reasoning_summary_text.delta")
		normalized = normalizeReasoningSummaryIndex(normalized)
	case "response.reasoning_text.done":
		normalized, _ = sjson.SetBytes(normalized, "type", "response.reasoning_summary_part.done")
		normalized, _ = sjson.SetBytes(normalized, "part.type", "summary_text")
		if text := gjson.GetBytes(normalized, "text"); text.Exists() {
			normalized, _ = sjson.SetBytes(normalized, "part.text", text.String())
		}
		normalized, _ = sjson.DeleteBytes(normalized, "text")
		normalized = normalizeReasoningSummaryIndex(normalized)
	case "response.content_part.added":
		if gjson.GetBytes(normalized, "part.type").String() == "reasoning_text" {
			normalized, _ = sjson.SetBytes(normalized, "type", "response.reasoning_summary_part.added")
			normalized, _ = sjson.SetBytes(normalized, "part.type", "summary_text")
			normalized = normalizeReasoningSummaryIndex(normalized)
		}
	case "response.content_part.done":
		if gjson.GetBytes(normalized, "part.type").String() == "reasoning_text" {
			normalized, _ = sjson.SetBytes(normalized, "type", "response.reasoning_summary_part.done")
			normalized, _ = sjson.SetBytes(normalized, "part.type", "summary_text")
			normalized = normalizeReasoningSummaryIndex(normalized)
		}
	}

	if item := gjson.GetBytes(normalized, "item"); item.Exists() && item.Type == gjson.JSON {
		updatedItem := normalizeReasoningOutputItem([]byte(item.Raw))
		if !bytes.Equal(updatedItem, []byte(item.Raw)) {
			normalized, _ = sjson.SetRawBytes(normalized, "item", updatedItem)
		}
	}
	if output := gjson.GetBytes(normalized, "response.output"); output.IsArray() {
		updatedOutput, changed := normalizeReasoningOutputItems(output.Array())
		if changed {
			normalized, _ = sjson.SetRawBytes(normalized, "response.output", updatedOutput)
		}
	}
	return normalized
}

// NormalizeReasoningSummaryDataEvents 对 reasoning_text.done 展开为 text.done + part.done。
func NormalizeReasoningSummaryDataEvents(eventData []byte) [][]byte {
	if len(eventData) == 0 || !gjson.ValidBytes(eventData) {
		return [][]byte{eventData}
	}
	if gjson.GetBytes(eventData, "type").String() != "response.reasoning_text.done" {
		return [][]byte{NormalizeReasoningSummaryData(eventData)}
	}
	textDone, _ := sjson.SetBytes(eventData, "type", "response.reasoning_summary_text.done")
	textDone = normalizeReasoningSummaryIndex(textDone)
	partDone := NormalizeReasoningSummaryData(eventData)
	return [][]byte{textDone, partDone}
}

func NormalizeReasoningSummaryEventName(eventName string) string {
	switch eventName {
	case "response.reasoning_text.delta":
		return "response.reasoning_summary_text.delta"
	case "response.reasoning_text.done":
		return "response.reasoning_summary_part.done"
	default:
		return eventName
	}
}

func NormalizeReasoningSummaryEventLine(line []byte, eventName string) []byte {
	if eventName == "" && bytes.HasPrefix(line, eventTag) {
		eventName = strings.TrimSpace(string(line[len(eventTag):]))
	}
	eventName = NormalizeReasoningSummaryEventName(eventName)
	if eventName == "" {
		return bytes.Clone(line)
	}
	return []byte("event: " + eventName)
}

// NormalizeNonStreamReasoning 非流完整 body 内 reasoning 形态归一。
func NormalizeNonStreamReasoning(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	// 可能是单条 completed JSON，或 SSE 拼接文本
	if gjson.ValidBytes(body) {
		return NormalizeReasoningSummaryData(body)
	}
	// SSE 块：逐 data 行处理
	return normalizeSSEPayload(body)
}

func normalizeSSEPayload(data []byte) []byte {
	lines := bytes.Split(data, []byte("\n"))
	var out bytes.Buffer
	for i, line := range lines {
		if i > 0 {
			out.WriteByte('\n')
		}
		trimmed := bytes.TrimSpace(line)
		if bytes.HasPrefix(trimmed, dataTag) {
			payload := bytes.TrimSpace(trimmed[len(dataTag):])
			// reasoning_text.done 可能展开为两条
			events := NormalizeReasoningSummaryDataEvents(payload)
			for j, ev := range events {
				if j > 0 {
					out.WriteByte('\n')
				}
				out.Write(dataTag)
				out.WriteByte(' ')
				out.Write(ev)
			}
			continue
		}
		if bytes.HasPrefix(trimmed, eventTag) {
			out.Write(NormalizeReasoningSummaryEventLine(trimmed, ""))
			continue
		}
		out.Write(line)
	}
	return out.Bytes()
}

func normalizeReasoningSummaryIndex(eventData []byte) []byte {
	contentIndex := gjson.GetBytes(eventData, "content_index")
	if contentIndex.Exists() && contentIndex.Raw != "" && !gjson.GetBytes(eventData, "summary_index").Exists() {
		eventData, _ = sjson.SetRawBytes(eventData, "summary_index", []byte(contentIndex.Raw))
	}
	eventData, _ = sjson.DeleteBytes(eventData, "content_index")
	return eventData
}

func normalizeReasoningOutputItems(items []gjson.Result) ([]byte, bool) {
	var buf bytes.Buffer
	buf.WriteByte('[')
	changed := false
	for i, item := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		updated := normalizeReasoningOutputItem([]byte(item.Raw))
		if !bytes.Equal(updated, []byte(item.Raw)) {
			changed = true
		}
		buf.Write(updated)
	}
	buf.WriteByte(']')
	return buf.Bytes(), changed
}

func normalizeReasoningOutputItem(item []byte) []byte {
	if !gjson.ValidBytes(item) || gjson.GetBytes(item, "type").String() != "reasoning" {
		return item
	}
	normalized := item
	if summary := gjson.GetBytes(normalized, "summary"); summary.IsArray() {
		updated, changed := normalizeReasoningSummaryItems(summary.Array())
		if changed {
			normalized, _ = sjson.SetRawBytes(normalized, "summary", updated)
		}
	}
	content := gjson.GetBytes(normalized, "content")
	if !content.IsArray() {
		return normalized
	}
	summaryItems := make([]gjson.Result, 0, len(content.Array()))
	for _, part := range content.Array() {
		if part.Get("type").String() == "reasoning_text" {
			summaryItems = append(summaryItems, part)
		}
	}
	if len(summaryItems) == 0 {
		return normalized
	}
	updatedSummary, _ := normalizeReasoningSummaryItems(summaryItems)
	normalized, _ = sjson.SetRawBytes(normalized, "summary", updatedSummary)
	normalized, _ = sjson.DeleteBytes(normalized, "content")
	return normalized
}

func normalizeReasoningSummaryItems(items []gjson.Result) ([]byte, bool) {
	var buf bytes.Buffer
	buf.WriteByte('[')
	changed := false
	for i, item := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		itemRaw := []byte(item.Raw)
		if item.Get("type").String() == "reasoning_text" {
			if next, err := sjson.SetBytes(itemRaw, "type", "summary_text"); err == nil {
				itemRaw = next
				changed = true
			}
		}
		buf.Write(itemRaw)
	}
	buf.WriteByte(']')
	return buf.Bytes(), changed
}

// reasoningStreamWrapper 对流式 SSE 做 reasoning 归一（行缓冲）。
type reasoningStreamWrapper struct {
	src     io.ReadCloser
	buf     bytes.Buffer
	pending []byte
	srcErr  error
	done    bool
}

func WrapReasoningStream(src io.ReadCloser) io.ReadCloser {
	if src == nil {
		return nil
	}
	return &reasoningStreamWrapper{src: src}
}

func (w *reasoningStreamWrapper) Read(p []byte) (int, error) {
	if w == nil {
		return 0, io.EOF
	}
	for {
		if len(w.pending) > 0 {
			n := copy(p, w.pending)
			w.pending = w.pending[n:]
			return n, nil
		}
		if w.done {
			if w.srcErr != nil {
				return 0, w.srcErr
			}
			return 0, io.EOF
		}

		tmp := make([]byte, 4096)
		n, err := w.src.Read(tmp)
		if n > 0 {
			w.buf.Write(tmp[:n])
			w.drainLines()
		}
		if err != nil {
			if w.buf.Len() > 0 {
				w.pending = append(w.pending, normalizeSSELine(w.buf.Bytes())...)
				w.buf.Reset()
			}
			w.srcErr = err
			w.done = true
			if len(w.pending) > 0 {
				n2 := copy(p, w.pending)
				w.pending = w.pending[n2:]
				return n2, nil
			}
			return 0, err
		}
		if len(w.pending) == 0 {
			// 需要更多字节才能凑成完整行
			continue
		}
	}
}

func (w *reasoningStreamWrapper) drainLines() {
	for {
		data := w.buf.Bytes()
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			return
		}
		line := append([]byte(nil), data[:idx+1]...)
		w.buf.Next(idx + 1)
		w.pending = append(w.pending, normalizeSSELine(line)...)
	}
}

func (w *reasoningStreamWrapper) Close() error {
	if w == nil || w.src == nil {
		return nil
	}
	return w.src.Close()
}

func normalizeSSELine(line []byte) []byte {
	// 保留换行
	hasNL := bytes.HasSuffix(line, []byte("\n"))
	trimmed := bytes.TrimRight(line, "\r\n")
	if bytes.HasPrefix(trimmed, dataTag) {
		payload := bytes.TrimSpace(trimmed[len(dataTag):])
		events := NormalizeReasoningSummaryDataEvents(payload)
		var out bytes.Buffer
		for i, ev := range events {
			if i > 0 {
				out.WriteByte('\n')
			}
			out.Write(dataTag)
			out.WriteByte(' ')
			out.Write(ev)
			if hasNL {
				out.WriteByte('\n')
			}
		}
		return out.Bytes()
	}
	if bytes.HasPrefix(trimmed, eventTag) {
		out := NormalizeReasoningSummaryEventLine(trimmed, "")
		if hasNL {
			out = append(out, '\n')
		}
		return out
	}
	return line
}

// ResponsesSSEStreamOptions 控制 SSE 下游改写行为。
type ResponsesSSEStreamOptions struct {
	// NormalizeReasoning：reasoning_text → summary 形态（Compat 开，Native 关）
	NormalizeReasoning bool
	// PatchCompleted：收集 output_item.done 并 patch response.completed.output
	PatchCompleted bool
	// ReplayScope：有效时在 completed 时写 reasoning replay 缓存
	ReplayScope ReplayScope
}

// WrapResponsesSSEStream 扫流逻辑：
//  1. 缓存 pending event: 行
//  2. data: 归一 / patch completed 后，按 data.type 同步写出 event: 行
//  3. 展开 reasoning_text.done 时为每条 data 配对应 event
func WrapResponsesSSEStream(inner io.ReadCloser, opts ResponsesSSEStreamOptions) io.ReadCloser {
	if inner == nil {
		return nil
	}
	if !opts.NormalizeReasoning && !opts.PatchCompleted && !opts.ReplayScope.Valid() {
		return inner
	}
	return &responsesSSEStream{
		inner:               inner,
		reader:              bufio.NewReaderSize(inner, 64*1024),
		opts:                opts,
		outputItemsByIndex:  make(map[int64][]byte),
		outputItemsFallback: make([][]byte, 0, 4),
	}
}

// WrapCompletedOutputPatchStream 兼容入口：patch completed + event/data 同步（不做 reasoning 归一）。
func WrapCompletedOutputPatchStream(inner io.ReadCloser, scope ReplayScope) io.ReadCloser {
	return WrapResponsesSSEStream(inner, ResponsesSSEStreamOptions{
		NormalizeReasoning: false,
		PatchCompleted:     true,
		ReplayScope:        scope,
	})
}

// WrapReplayCacheStream 兼容入口：与 WrapCompletedOutputPatchStream 相同。
func WrapReplayCacheStream(inner io.ReadCloser, scope ReplayScope) io.ReadCloser {
	return WrapCompletedOutputPatchStream(inner, scope)
}

// WrapCompatResponsesSSEStream Compat 默认：reasoning 归一 + completed patch + event 同步。
func WrapCompatResponsesSSEStream(inner io.ReadCloser, scope ReplayScope) io.ReadCloser {
	return WrapResponsesSSEStream(inner, ResponsesSSEStreamOptions{
		NormalizeReasoning: true,
		PatchCompleted:     true,
		ReplayScope:        scope,
	})
}

type responsesSSEStream struct {
	inner               io.ReadCloser
	reader              *bufio.Reader
	opts                ResponsesSSEStreamOptions
	pending             []byte
	pendingEventLine    []byte
	srcErr              error
	done                bool
	outputItemsByIndex  map[int64][]byte
	outputItemsFallback [][]byte
	cachedCompleted     bool
}

func (s *responsesSSEStream) Read(p []byte) (int, error) {
	if s == nil {
		return 0, io.EOF
	}
	for len(s.pending) == 0 {
		if s.done {
			if s.srcErr != nil {
				return 0, s.srcErr
			}
			return 0, io.EOF
		}
		line, err := s.reader.ReadBytes('\n')
		if len(line) > 0 {
			s.pending = s.processLine(line)
		}
		if err != nil {
			// 冲刷未配对的 event:
			if s.pendingEventLine != nil {
				flush := s.emitEventLine(s.pendingEventLine, "")
				s.pendingEventLine = nil
				s.pending = append(flush, s.pending...)
			}
			s.srcErr = err
			s.done = true
			if len(s.pending) == 0 {
				return 0, err
			}
			break
		}
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

func (s *responsesSSEStream) processLine(line []byte) []byte {
	hasNL := bytes.HasSuffix(line, []byte("\n"))
	trimmed := bytes.TrimRight(line, "\r\n")

	// event: 缓存，等 data 后按 type 同步写出
	if bytes.HasPrefix(trimmed, eventTag) {
		var out []byte
		if s.pendingEventLine != nil {
			out = s.emitEventLine(s.pendingEventLine, "")
		}
		s.pendingEventLine = append([]byte(nil), trimmed...)
		return out
	}

	if bytes.HasPrefix(trimmed, dataTag) {
		payload := bytes.TrimSpace(trimmed[len(dataTag):])
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			return s.flushPendingEventThen(line)
		}

		var events [][]byte
		if s.opts.NormalizeReasoning {
			events = NormalizeReasoningSummaryDataEvents(payload)
		} else {
			events = [][]byte{payload}
		}

		hasPendingEvent := s.pendingEventLine != nil
		var out bytes.Buffer
		for i, ev := range events {
			if s.opts.NormalizeReasoning {
				ev = NormalizeReasoningSummaryData(ev)
			}
			typ := gjson.GetBytes(ev, "type").String()
			if s.opts.PatchCompleted {
				switch typ {
				case "response.output_item.done":
					collectOutputItemDone(ev, s.outputItemsByIndex, &s.outputItemsFallback)
				case "response.completed":
					ev = patchCompletedOutput(ev, s.outputItemsByIndex, s.outputItemsFallback)
					if s.opts.NormalizeReasoning {
						ev = NormalizeReasoningSummaryData(ev)
					}
					typ = gjson.GetBytes(ev, "type").String()
					if s.opts.ReplayScope.Valid() && !s.cachedCompleted {
						CacheReasoningReplayFromCompleted(s.opts.ReplayScope, ev)
						s.cachedCompleted = true
					}
				}
			}

			// 同步 event: 与 data.type
			if hasPendingEvent {
				var eventLine []byte
				if i == 0 {
					eventLine = s.emitEventLine(s.pendingEventLine, typ)
					s.pendingEventLine = nil
					hasPendingEvent = false
				} else {
					// 展开出的后续事件：用当前 type 生成 event 行
					eventLine = s.emitEventLine(nil, typ)
				}
				out.Write(eventLine)
			} else if i > 0 && s.opts.NormalizeReasoning {
				// 无 pending event 但展开多条：仍为每条补 event 行，避免客户端只认 type 字段时丢事件名
				out.Write(s.emitEventLine(nil, typ))
			}

			out.Write(dataTag)
			out.WriteByte(' ')
			out.Write(ev)
			if hasNL {
				out.WriteByte('\n')
			} else if i < len(events)-1 {
				out.WriteByte('\n')
			}
		}
		return out.Bytes()
	}

	// 其它行：先冲刷 pending event
	return s.flushPendingEventThen(line)
}

func (s *responsesSSEStream) flushPendingEventThen(line []byte) []byte {
	if s.pendingEventLine == nil {
		return line
	}
	out := s.emitEventLine(s.pendingEventLine, "")
	s.pendingEventLine = nil
	return append(out, line...)
}

// emitEventLine 生成带换行的 event: 行；eventName 非空时以之为准（已是 data 归一后的 type）。
func (s *responsesSSEStream) emitEventLine(pendingLine []byte, eventName string) []byte {
	line := NormalizeReasoningSummaryEventLine(pendingLine, eventName)
	if len(line) == 0 {
		return nil
	}
	if !bytes.HasSuffix(line, []byte("\n")) {
		line = append(line, '\n')
	}
	return line
}

func (s *responsesSSEStream) Close() error {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.Close()
}

func cacheReplayFromSSEBytes(scope ReplayScope, data []byte) {
	if !scope.Valid() || len(data) == 0 {
		return
	}
	byIndex := make(map[int64][]byte)
	var fallback [][]byte
	var completed []byte
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(line[len("data:"):])
		if len(payload) == 0 {
			continue
		}
		payload = NormalizeReasoningSummaryData(payload)
		switch gjson.GetBytes(payload, "type").String() {
		case "response.output_item.done":
			collectOutputItemDone(payload, byIndex, &fallback)
		case "response.completed":
			completed = payload
		}
	}
	if len(completed) == 0 {
		return
	}
	CacheReasoningReplayFromCompleted(scope, patchCompletedOutput(completed, byIndex, fallback))
}

func collectOutputItemDone(eventData []byte, byIndex map[int64][]byte, fallback *[][]byte) {
	itemResult := gjson.GetBytes(eventData, "item")
	if !itemResult.Exists() || itemResult.Type != gjson.JSON {
		return
	}
	if outputIndex := gjson.GetBytes(eventData, "output_index"); outputIndex.Exists() {
		byIndex[outputIndex.Int()] = []byte(itemResult.Raw)
		return
	}
	*fallback = append(*fallback, []byte(itemResult.Raw))
}

func patchCompletedOutput(eventData []byte, byIndex map[int64][]byte, fallback [][]byte) []byte {
	outputResult := gjson.GetBytes(eventData, "response.output")
	shouldPatch := (!outputResult.Exists() || !outputResult.IsArray() || len(outputResult.Array()) == 0) &&
		(len(byIndex) > 0 || len(fallback) > 0)
	if !shouldPatch {
		return eventData
	}
	indexes := make([]int64, 0, len(byIndex))
	for idx := range byIndex {
		indexes = append(indexes, idx)
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
	var buf bytes.Buffer
	buf.WriteByte('[')
	wrote := false
	for _, idx := range indexes {
		if wrote {
			buf.WriteByte(',')
		}
		buf.Write(byIndex[idx])
		wrote = true
	}
	for _, item := range fallback {
		if wrote {
			buf.WriteByte(',')
		}
		buf.Write(item)
		wrote = true
	}
	buf.WriteByte(']')
	if !wrote {
		return eventData
	}
	patched, _ := sjson.SetRawBytes(eventData, "response.output", buf.Bytes())
	return patched
}

// CollectOutputItemDone/PatchCompletedOutput expose the reference executor's
// completed patch primitives to the WebSocket adapter.
func CollectOutputItemDone(eventData []byte, byIndex map[int64][]byte, fallback *[][]byte) {
	collectOutputItemDone(eventData, byIndex, fallback)
}

func PatchCompletedOutput(eventData []byte, byIndex map[int64][]byte, fallback [][]byte) []byte {
	return patchCompletedOutput(eventData, byIndex, fallback)
}

// CompletedEventToNonStreamBody 将 patched response.completed 事件转为非流 Response 对象。
func CompletedEventToNonStreamBody(completedEvent []byte) []byte {
	if len(completedEvent) == 0 {
		return completedEvent
	}
	completedEvent = NormalizeReasoningSummaryData(completedEvent)
	if resp := gjson.GetBytes(completedEvent, "response"); resp.Exists() && resp.Type == gjson.JSON {
		return NormalizeNonStreamReasoning([]byte(resp.Raw))
	}
	return NormalizeNonStreamReasoning(completedEvent)
}

// ClearReasoningReplayAfterCompaction compact 后清理 reasoning replay 缓存。
func ClearReasoningReplayAfterCompaction(scope ReplayScope) {
	if !scope.Valid() {
		return
	}
	DeleteReasoningReplayItems(scope.ModelName, scope.SessionKey)
}
