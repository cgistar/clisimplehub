package backend

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// body: prompt_cache_key / installation-id / turn-metadata / window-id
// header: Session_id / Conversation_id / X-Client-Request-Id / Thread-Id / X-Codex-Window-Id / Turn-Metadata
// 不改写 client_metadata.session_id / thread_id
func applyIdentityConfuse(authID string, body []byte, headers http.Header) ([]byte, http.Header, IdentityState) {
	authID = strings.TrimSpace(authID)
	if authID == "" || len(body) == 0 {
		return body, headers, IdentityState{}
	}
	if headers == nil {
		headers = http.Header{}
	}

	state := IdentityState{enabled: true, authID: authID}
	if promptCacheKey := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String()); promptCacheKey != "" {
		state.originalPromptCacheKey = promptCacheKey
		state.promptCacheKey = identityConfuseUUID(authID, "prompt-cache", promptCacheKey)
		body, _ = sjson.SetBytes(body, "prompt_cache_key", state.promptCacheKey)
	}
	// 混淆 installation-id，响应侧不回写。
	if installationID := strings.TrimSpace(gjson.GetBytes(body, "client_metadata.x-codex-installation-id").String()); installationID != "" {
		body, _ = sjson.SetBytes(body, "client_metadata.x-codex-installation-id", identityConfuseUUID(authID, "installation", installationID))
	}
	if turnMetadata := strings.TrimSpace(gjson.GetBytes(body, "client_metadata.x-codex-turn-metadata").String()); turnMetadata != "" {
		body, _ = sjson.SetBytes(body, "client_metadata.x-codex-turn-metadata", applyTurnMetadataIdentityConfuse(turnMetadata, &state))
	}
	if state.promptCacheKey != "" {
		if windowID := strings.TrimSpace(gjson.GetBytes(body, "client_metadata.x-codex-window-id").String()); windowID != "" {
			body, _ = sjson.SetBytes(body, "client_metadata.x-codex-window-id", state.promptCacheKey+":0")
		}
	}

	applyIdentityConfuseHeaders(headers, &state)
	return body, headers, state
}

func applyIdentityConfuseHeaders(headers http.Header, state *IdentityState) {
	if headers == nil || state == nil || !state.enabled {
		return
	}
	if rawTurnMetadata := strings.TrimSpace(headerValueCaseInsensitive(headers, "X-Codex-Turn-Metadata")); rawTurnMetadata != "" {
		setHeaderCasePreserved(headers, "X-Codex-Turn-Metadata", applyTurnMetadataIdentityConfuse(rawTurnMetadata, state))
	}
	if state.promptCacheKey == "" {
		return
	}
	removeHeaderCaseInsensitive(headers, "Session-Id")
	setHeaderCasePreserved(headers, "Session_id", state.promptCacheKey)
	if headerValueCaseInsensitive(headers, "Conversation_id") != "" {
		setHeaderCasePreserved(headers, "Conversation_id", state.promptCacheKey)
	}
	headers.Set("X-Client-Request-Id", state.promptCacheKey)
	headers.Set("Thread-Id", state.promptCacheKey)
	headers.Set("X-Codex-Window-Id", state.promptCacheKey+":0")
}

// 仅 remap prompt_cache_key / turn_id / window_id。
func applyTurnMetadataIdentityConfuse(rawTurnMetadata string, state *IdentityState) string {
	updated := rawTurnMetadata
	if state == nil || !state.enabled {
		return updated
	}
	if state.promptCacheKey != "" && gjson.Get(rawTurnMetadata, "prompt_cache_key").Exists() {
		updated, _ = sjson.Set(updated, "prompt_cache_key", state.promptCacheKey)
	} else if state.promptCacheKey != "" && state.originalPromptCacheKey != "" {
		updated = strings.ReplaceAll(updated, state.originalPromptCacheKey, state.promptCacheKey)
	}
	if turnID := strings.TrimSpace(gjson.Get(rawTurnMetadata, "turn_id").String()); turnID != "" {
		updated, _ = sjson.Set(updated, "turn_id", state.confuseTurnID(turnID))
	}
	if state.promptCacheKey != "" && gjson.Get(rawTurnMetadata, "window_id").Exists() {
		updated, _ = sjson.Set(updated, "window_id", state.promptCacheKey+":0")
	}
	return updated
}

func (state *IdentityState) confuseTurnID(turnID string) string {
	turnID = strings.TrimSpace(turnID)
	if state == nil || !state.enabled || strings.TrimSpace(state.authID) == "" || turnID == "" {
		return turnID
	}
	for _, replacement := range state.turnIDs {
		if replacement.original == turnID || replacement.confused == turnID {
			return replacement.confused
		}
	}
	confused := identityConfuseUUID(state.authID, "turn", turnID)
	state.turnIDs = append(state.turnIDs, identityReplacement{original: turnID, confused: confused})
	return confused
}

func identityConfuseUUID(authID string, kind string, value string) string {
	name := strings.Join([]string{"clisimplehub", "codex", "identity-confuse", kind, strings.TrimSpace(authID), strings.TrimSpace(value)}, ":")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)).String()
}

// ExposeIdentityPayload 将上游响应中的混淆 ID 还原为客户端原始 ID。
// 仅还原 prompt_cache_key 与 turn_id；installation-id 不回写。
func ExposeIdentityPayload(payload []byte, state IdentityState) []byte {
	payload = replaceIdentityPayload(payload, state.promptCacheKey, state.originalPromptCacheKey)
	for _, replacement := range state.turnIDs {
		payload = replaceIdentityPayload(payload, replacement.confused, replacement.original)
	}
	return payload
}

// ConfuseIdentityPayload 将载荷中的客户端原始 ID 替换为混淆 ID（日志/内部处理用）。
func ConfuseIdentityPayload(payload []byte, state IdentityState) []byte {
	payload = replaceIdentityPayload(payload, state.originalPromptCacheKey, state.promptCacheKey)
	for _, replacement := range state.turnIDs {
		payload = replaceIdentityPayload(payload, replacement.original, replacement.confused)
	}
	return payload
}

func replaceIdentityPayload(payload []byte, from string, to string) []byte {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if len(payload) == 0 || from == "" || to == "" || from == to || !bytes.Contains(payload, []byte(from)) {
		return payload
	}
	return bytes.ReplaceAll(payload, []byte(from), []byte(to))
}

func removeHeaderCaseInsensitive(headers http.Header, key string) {
	key = strings.TrimSpace(key)
	if headers == nil || key == "" {
		return
	}
	for existingKey := range headers {
		if strings.EqualFold(existingKey, key) {
			delete(headers, existingKey)
		}
	}
}

func NewIdentityExposeReadCloser(upstream io.ReadCloser, state IdentityState) io.ReadCloser {
	if upstream == nil || !state.enabled {
		return upstream
	}
	return &identityExposeReadCloser{
		upstream: upstream,
		reader:   bufio.NewReader(upstream),
		state:    state,
	}
}

type identityExposeReadCloser struct {
	upstream io.ReadCloser
	reader   *bufio.Reader
	state    IdentityState
	pending  []byte
}

func (r *identityExposeReadCloser) Read(p []byte) (int, error) {
	for len(r.pending) == 0 {
		line, err := r.reader.ReadBytes('\n')
		if len(line) > 0 {
			r.pending = ExposeIdentityPayload(line, r.state)
		}
		if err != nil {
			if len(r.pending) > 0 {
				break
			}
			return 0, err
		}
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func (r *identityExposeReadCloser) Close() error {
	return r.upstream.Close()
}
