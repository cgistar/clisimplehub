package codexplugin

import (
	"crypto/sha1"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// promptCacheSeedNamespace 前缀用于把派生的 UUID 与其他服务隔离开，避免误命中别的缓存分片。
const promptCacheSeedNamespace = "clisimplehub:codex:prompt-cache"

// passthroughPromptCacheKey 仅透传客户端已有的 prompt_cache_key
// 如果客户端没有提供 key，不做任何派生。
func passthroughPromptCacheKey(body []byte, clientHeaders http.Header) ([]byte, http.Header) {
	key := pickPromptCacheKeyFromRequest(body, clientHeaders)
	if key == "" {
		return body, clientHeaders
	}
	newBody := injectPromptCacheKey(body, key)
	newHeaders := cloneHeadersWithSessionID(clientHeaders, key)
	return newBody, newHeaders
}

// ensureCodexPromptCacheKey 为 Codex 请求规范化 prompt_cache_key（body）与 Session_id（header），
// 让同一个客户端/账号组合稳定地命中上游 prompt 缓存，显著降低 /responses/compact 的上游处理耗时。
//
// 优先级：
//  1. 客户端入站 Session_id header；
//  2. 客户端 body 内已有的 prompt_cache_key；
//  3. 按 (accountID, clientHeaders) 哈希派生出稳定的 UUID v5。
//
// 返回值：可能替换过 prompt_cache_key 的 body、以及确保 Session_id 就绪的 clientHeaders 克隆。
// 不会修改入参（body / clientHeaders）。
func ensureCodexPromptCacheKey(body []byte, clientHeaders http.Header, accountID string) ([]byte, http.Header) {
	key := pickPromptCacheKeyFromRequest(body, clientHeaders)
	if key == "" {
		key = deriveStableCodexCacheKey(clientHeaders, accountID)
	}
	if key == "" {
		// accountID 和 clientHeaders 都不可用（实际上不太会发生），放弃派生，保持旧行为。
		return body, clientHeaders
	}

	newBody := injectPromptCacheKey(body, key)
	newHeaders := cloneHeadersWithSessionID(clientHeaders, key)
	return newBody, newHeaders
}

// pickPromptCacheKeyFromRequest 读取客户端已经显式给出的缓存键。
func pickPromptCacheKeyFromRequest(body []byte, clientHeaders http.Header) string {
	if clientHeaders != nil {
		if v := strings.TrimSpace(clientHeaders.Get("Session_id")); v != "" {
			return v
		}
	}
	if len(body) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	if v, ok := payload["prompt_cache_key"].(string); ok {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// deriveStableCodexCacheKey 基于账号 ID 与客户端稳定身份（Authorization → X-Api-Key → User-Agent）派生一个 UUID v5。
// 没有任何可用种子时返回空串，调用方需要自行兜底。
func deriveStableCodexCacheKey(clientHeaders http.Header, accountID string) string {
	parts := make([]string, 0, 4)
	parts = append(parts, promptCacheSeedNamespace)

	if id := strings.TrimSpace(accountID); id != "" {
		parts = append(parts, "account="+id)
	}

	if clientIdentity := pickClientIdentity(clientHeaders); clientIdentity != "" {
		// 使用 SHA-1 摘要而不是原始凭证，避免把客户端凭证原文作为种子长期驻留在日志/堆中。
		sum := sha1.Sum([]byte(clientIdentity))
		parts = append(parts, "client="+hexEncode(sum[:]))
	}

	if len(parts) <= 1 {
		return ""
	}
	seed := strings.Join(parts, "|")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(seed)).String()
}

func pickClientIdentity(clientHeaders http.Header) string {
	if clientHeaders == nil {
		return ""
	}
	if v := strings.TrimSpace(clientHeaders.Get("Authorization")); v != "" {
		return v
	}
	if v := strings.TrimSpace(clientHeaders.Get("X-Api-Key")); v != "" {
		return v
	}
	if v := strings.TrimSpace(clientHeaders.Get("User-Agent")); v != "" {
		return v
	}
	return ""
}

func injectPromptCacheKey(body []byte, key string) []byte {
	if len(body) == 0 || key == "" {
		return body
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	if existing, ok := payload["prompt_cache_key"].(string); ok && existing == key {
		return body
	}
	payload["prompt_cache_key"] = key
	updated, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return updated
}

func cloneHeadersWithSessionID(clientHeaders http.Header, key string) http.Header {
	out := http.Header{}
	if clientHeaders != nil {
		out = clientHeaders.Clone()
	}
	if key == "" {
		return out
	}
	if strings.TrimSpace(out.Get("Session_id")) != "" {
		return out
	}
	out.Set("Session_id", key)
	return out
}

func hexEncode(src []byte) string {
	const hextable = "0123456789abcdef"
	dst := make([]byte, len(src)*2)
	for i, b := range src {
		dst[i*2] = hextable[b>>4]
		dst[i*2+1] = hextable[b&0x0f]
	}
	return string(dst)
}
