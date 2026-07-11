package backend

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
)

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
