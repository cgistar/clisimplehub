package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"clisimplehub/internal/storage"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var (
	claudeMessagesUserIDCache    sync.Map
	claudeMessagesSessionIDCache sync.Map
)

func injectClaudeMessagesUserID(body []byte, endpoint *storage.Endpoint, cfg resolvedClaudeMessagesConfig) []byte {
	existing := gjson.GetBytes(body, "metadata.user_id").String()
	if existing != "" && isValidClaudeMessagesUserID(existing) {
		return body
	}

	sessionID := resolveClaudeMessagesSessionID(endpoint, cfg)
	userID := resolveClaudeMessagesUserID(endpoint, cfg, sessionID)
	body, _ = sjson.SetBytes(body, "metadata.user_id", userID)
	return body
}

func resolveClaudeMessagesSessionID(endpoint *storage.Endpoint, cfg resolvedClaudeMessagesConfig) string {
	if !cfg.CacheSessionID {
		return randomUUID()
	}
	key := claudeMessagesIdentityKey(endpoint)
	if value, ok := claudeMessagesSessionIDCache.Load(key); ok {
		return value.(string)
	}
	sessionID := randomUUID()
	actual, _ := claudeMessagesSessionIDCache.LoadOrStore(key, sessionID)
	return actual.(string)
}

func resolveClaudeMessagesUserID(endpoint *storage.Endpoint, cfg resolvedClaudeMessagesConfig, sessionID string) string {
	if !cfg.CacheUserID {
		return generateClaudeMessagesUserID(sessionID)
	}
	key := claudeMessagesIdentityKey(endpoint)
	if value, ok := claudeMessagesUserIDCache.Load(key); ok {
		return value.(string)
	}
	userID := generateClaudeMessagesUserID(sessionID)
	actual, _ := claudeMessagesUserIDCache.LoadOrStore(key, userID)
	return actual.(string)
}

func claudeMessagesIdentityKey(endpoint *storage.Endpoint) string {
	if endpoint == nil {
		return "default"
	}
	return strings.TrimSpace(endpoint.APIURL) + "\n" + strings.TrimSpace(endpoint.APIKey)
}

func generateClaudeMessagesUserID(sessionID string) string {
	return fmt.Sprintf("user_%s_account__session_%s", randomHex(32), sessionID)
}

func isValidClaudeMessagesUserID(uid string) bool {
	if !strings.HasPrefix(uid, "user_") {
		return false
	}
	parts := strings.SplitN(uid, "_account_", 2)
	if len(parts) != 2 {
		return false
	}
	return strings.Contains(parts[1], "_session_")
}

func randomHex(nBytes int) string {
	buf := make([]byte, nBytes)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func randomUUID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

func jsonUnmarshalResult(result gjson.Result, dst any) error {
	if result.Raw == "" {
		return fmt.Errorf("empty result")
	}
	return json.Unmarshal([]byte(result.Raw), dst)
}
