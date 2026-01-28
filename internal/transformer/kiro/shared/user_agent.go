package shared

import (
	"regexp"
	"strings"
)

const (
	// DefaultKiroUserAgentBase is the default AWS SDK style User-Agent prefix used by Kiro clients.
	DefaultKiroUserAgentBase = "aws-sdk-js/1.0.27 ua/2.1 os/darwin#24.6.0 lang/js md/nodejs#22.21.1 api/codewhispererstreaming#1.0.27 m/E"
	// DefaultKiroVersion is the default Kiro client version token appended to user agent headers.
	DefaultKiroVersion = "KiroIDE-0.8.0"
)

func KiroUserAgentBaseOrDefault(userAgentBase string) string {
	if v := strings.TrimSpace(userAgentBase); v != "" {
		return v
	}
	return DefaultKiroUserAgentBase
}

// KiroXAmzUserAgentBase 从完整的 User-Agent 中提取 x-amz-user-agent 基础部分
// 提取第一个空格之前的部分（例如 "aws-sdk-js/1.0.27"）
func KiroXAmzUserAgentBase(userAgentBase string) string {
	fullUA := KiroUserAgentBaseOrDefault(userAgentBase)
	// 提取第一个空格之前的部分
	if idx := strings.Index(fullUA, " "); idx > 0 {
		return fullUA[:idx]
	}
	return fullUA
}

func KiroVersionOrDefault(version string) string {
	if v := strings.TrimSpace(version); v != "" {
		// Normalize to the value shape Kiro clients use in headers: `KiroIDE-<version>`.
		// This keeps config.json simple (users can set `0.8.0`) while staying compatible
		// with callers that already pass the full `KiroIDE-...` token.
		if !strings.HasPrefix(v, "KiroIDE-") {
			return "KiroIDE-" + v
		}
		return v
	}
	return DefaultKiroVersion
}

func TruncateFingerprint(fp string, maxLen int) string {
	fp = strings.TrimSpace(fp)
	if fp == "" {
		return "unknown"
	}
	// Keep compatibility with 32-hex "UUID-without-dashes" style fingerprints by normalizing
	// to the 64-hex machineId format used by Kiro clients.
	// (mirrors kiro.rs behavior: 32 hex -> repeat once)
	if len(fp) == 32 && isHexString(fp) {
		fp = fp + fp
	}
	if maxLen > 0 && len(fp) > maxLen {
		return fp[:maxLen]
	}
	return fp
}

var hexStringRe = regexp.MustCompile(`^[0-9a-fA-F]+$`)

func isHexString(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && hexStringRe.MatchString(s)
}
