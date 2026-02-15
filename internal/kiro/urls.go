package kiro

import "strings"

// ResolveRegion returns a trimmed region, defaulting to "us-east-1" if empty.
func ResolveRegion(region string) string {
	if r := strings.TrimSpace(region); r != "" {
		return r
	}
	return "us-east-1"
}

// KiroOidcHost returns the OIDC hostname for the given region.
func KiroOidcHost(region string) string {
	return "oidc." + ResolveRegion(region) + ".amazonaws.com"
}

// KiroOidcBaseURL returns the OIDC base URL for the given region.
func KiroOidcBaseURL(region string) string {
	return "https://" + KiroOidcHost(region)
}

// KiroAPIHost 返回指定 region 的 Kiro/CodeWhisperer API 域名。
func KiroAPIHost(region string) string {
	return "codewhisperer." + ResolveRegion(region) + ".amazonaws.com"
}

// KiroQHost 返回指定 region 的 Q API 域名（用于用量接口）。
func KiroQHost(region string) string {
	return "q." + ResolveRegion(region) + ".amazonaws.com"
}

// KiroRefreshURL 返回指定 region 的 token refresh URL (Social 认证)。
func KiroRefreshURL(region string) string {
	return "https://prod." + ResolveRegion(region) + ".auth.desktop.kiro.dev/refreshToken"
}

// KiroIdcRefreshURL 返回指定 region 的 IdC token refresh URL (AWS SSO OIDC)。
func KiroIdcRefreshURL(region string) string {
	return KiroOidcBaseURL(region) + "/token"
}

// KiroGenerateURL 返回指定 region 的 generateAssistantResponse URL。
func KiroGenerateURL(region string) string {
	return "https://" + KiroQHost(region) + "/generateAssistantResponse"
}

// KiroMCPURL returns the region-specific MCP endpoint URL (used by web_search).
func KiroMCPURL(region string) string {
	return "https://" + KiroQHost(region) + "/mcp"
}

// KiroUsageURL 返回指定 region 的用量查询 URL。
func KiroUsageURL(region string) string {
	return "https://" + KiroQHost(region) + "/getUsageLimits"
}
