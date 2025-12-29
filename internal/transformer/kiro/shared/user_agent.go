package shared

import "strings"

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
		return v
	}
	return DefaultKiroVersion
}

func TruncateFingerprint(fp string, maxLen int) string {
	fp = strings.TrimSpace(fp)
	if fp == "" {
		return "unknown"
	}
	if maxLen > 0 && len(fp) > maxLen {
		return fp[:maxLen]
	}
	return fp
}
