package middleware

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	agentIdentifier = "You are a Claude agent, built on Anthropic's Claude Agent SDK."
	billingPrefix   = "x-anthropic-billing-header:"
	claudeCliUA     = "claude-cli"
)

// applyCloaking 对非 Claude Code 客户端的请求进行伪装处理：
// 1. 注入 billing header + agent identifier 到 system prompt
// 2. 注入 fake user_id 到 metadata
func applyCloaking(body []byte, userAgent string) []byte {
	if isClaudeCodeClient(userAgent) {
		return body
	}
	model := gjson.GetBytes(body, "model").String()
	if strings.HasPrefix(model, "claude-3-5-haiku") {
		body = injectFakeUserID(body)
		return body
	}
	body = injectSystemInstructions(body)
	body = injectFakeUserID(body)
	return body
}

func isClaudeCodeClient(ua string) bool {
	return strings.HasPrefix(ua, claudeCliUA)
}

// injectSystemInstructions 在 system prompt 前插入 billing header 和 agent identifier（非严格模式）。
func injectSystemInstructions(body []byte) []byte {
	billingText := generateBillingHeader(body)
	billingBlock := map[string]string{"type": "text", "text": billingText}
	agentBlock := map[string]string{"type": "text", "text": agentIdentifier}

	sysResult := gjson.GetBytes(body, "system")

	// 无 system → 直接注入
	if !sysResult.Exists() {
		body, _ = sjson.SetBytes(body, "system", []any{billingBlock, agentBlock})
		return body
	}

	// string system → 转为 array 并前置
	if sysResult.Type == gjson.String {
		userBlock := map[string]any{
			"type":          "text",
			"text":          sysResult.String(),
			"cache_control": map[string]string{"type": "ephemeral"},
		}
		body, _ = sjson.SetBytes(body, "system", []any{billingBlock, agentBlock, userBlock})
		return body
	}

	// array system → 检查幂等性，前置注入
	if sysResult.IsArray() {
		arr := sysResult.Array()
		if len(arr) > 0 {
			firstText := arr[0].Get("text").String()
			if strings.HasPrefix(firstText, billingPrefix) {
				return body // 已注入
			}
		}
		// 构建新数组：billing + agent + 原有 blocks（给无 cache_control 的补上）
		var newSystem []any
		newSystem = append(newSystem, billingBlock, agentBlock)
		for _, item := range arr {
			var block map[string]any
			if err := unmarshalGjsonResult(item, &block); err != nil || block == nil {
				continue
			}
			if _, has := block["cache_control"]; !has {
				block["cache_control"] = map[string]string{"type": "ephemeral"}
			}
			newSystem = append(newSystem, block)
		}
		body, _ = sjson.SetBytes(body, "system", newSystem)
	}
	return body
}

func generateBillingHeader(body []byte) string {
	build := randomHex(2)[:3] // 3 hex chars
	hash := sha256Hex(body)[:5]
	return fmt.Sprintf("%s cc_version=2.1.63.%s; cc_entrypoint=cli; cch=%s;", billingPrefix, build, hash)
}

// --- Fake User ID ---

// injectFakeUserID 注入 fake user_id 到 metadata.user_id（如不存在或无效）。
func injectFakeUserID(body []byte) []byte {
	existing := gjson.GetBytes(body, "metadata.user_id").String()
	if existing != "" && isValidUserID(existing) {
		return body
	}
	body, _ = sjson.SetBytes(body, "metadata.user_id", generateFakeUserID())
	return body
}

func isValidUserID(uid string) bool {
	// user_[64hex]_account_[UUID]_session_[UUID]
	if !strings.HasPrefix(uid, "user_") {
		return false
	}
	parts := strings.SplitN(uid, "_account_", 2)
	if len(parts) != 2 {
		return false
	}
	hexPart := strings.TrimPrefix(parts[0], "user_")
	if len(hexPart) != 64 {
		return false
	}
	return strings.Contains(parts[1], "_session_")
}

func generateFakeUserID() string {
	userHex := randomHex(32) // 64 hex chars
	sessionUUID := randomUUID()
	return fmt.Sprintf("user_%s_account__session_%s", userHex, sessionUUID)
}

// --- 工具函数 ---

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randomUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func unmarshalGjsonResult(r gjson.Result, dst *map[string]any) error {
	raw := r.Raw
	if raw == "" {
		return fmt.Errorf("empty result")
	}
	return json.Unmarshal([]byte(raw), dst)
}
