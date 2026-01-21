package shared

import (
	"regexp"
	"strings"
)

const (
	// DefaultKiroUserAgentBase is the default AWS SDK style User-Agent prefix used by Kiro clients.
	DefaultKiroUserAgentBase = "aws-sdk-js/1.0.27 ua/2.1 os/win32#10.0.22631 lang/js md/nodejs#22.21.1 api/codewhispererstreaming#1.0.27 m/E"
	// DefaultKiroVersion is the default Kiro client version token appended to user agent headers.
	DefaultKiroVersion = "KiroIDE-0.8.0"
	// DefaultKiroXAmzUserAgentBase is the default `x-amz-user-agent` base prefix.
	DefaultKiroXAmzUserAgentBase = "aws-sdk-js/1.0.27"
)

func KiroUserAgentBaseOrDefault(userAgentBase string) string {
	if v := strings.TrimSpace(userAgentBase); v != "" {
		return v
	}
	return DefaultKiroUserAgentBase
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
