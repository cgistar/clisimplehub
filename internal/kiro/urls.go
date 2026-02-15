package kiro

import "strings"

// KiroAPIHost 返回指定 region 的 Kiro/CodeWhisperer API 域名。
func KiroAPIHost(region string) string {
	region = strings.TrimSpace(region)
	if region == "" {
		region = "us-east-1"
	}
	return "codewhisperer." + region + ".amazonaws.com"
}

// KiroQHost 返回指定 region 的 Q API 域名（用于用量接口）。
func KiroQHost(region string) string {
	region = strings.TrimSpace(region)
	if region == "" {
		region = "us-east-1"
	}
	return "q." + region + ".amazonaws.com"
}

// KiroRefreshURL 返回指定 region 的 token refresh URL (Social 认证)。
func KiroRefreshURL(region string) string {
	region = strings.TrimSpace(region)
	if region == "" {
		region = "us-east-1"
	}
	return "https://prod." + region + ".auth.desktop.kiro.dev/refreshToken"
}

// KiroIdcRefreshURL 返回指定 region 的 IdC token refresh URL (AWS SSO OIDC)。
func KiroIdcRefreshURL(region string) string {
	region = strings.TrimSpace(region)
	if region == "" {
		region = "us-east-1"
	}
	return "https://oidc." + region + ".amazonaws.com/token"
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
