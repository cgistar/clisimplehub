package codexplugin

import (
	"net/http"
	"strings"

	codexShared "clisimplehub/internal/codex/shared"
	appmiddleware "clisimplehub/internal/middleware"
	"clisimplehub/internal/proxy"

	"github.com/google/uuid"
)

func applyCodexHeaders(req *http.Request, accessToken, accountID string, isStreaming bool, config *codexShared.CodexMultiConfig, clientHeaders http.Header) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	// 配置项只控制稳定指纹，不从客户端透传敏感鉴权头。
	userAgent := codexShared.DefaultCodexUserAgent
	originator := codexShared.DefaultCodexOriginator
	if config != nil {
		userAgent = config.GetUserAgent()
		originator = config.GetOriginator()
	}
	if config == nil || strings.TrimSpace(config.Config.UserAgent) == "" {
		if clientUserAgent := strings.TrimSpace(clientHeaders.Get("User-Agent")); appmiddleware.IsCodexCLI(clientUserAgent) {
			userAgent = clientUserAgent
		}
	}

	// 只允许安全的 Codex 协议头使用客户端原值。
	filtered := filterClientHeaders(clientHeaders)

	// Version 不再无条件填默认值，避免与真实客户端版本漂移。
	if val := filtered.Get("Version"); val != "" {
		req.Header.Set("Version", val)
	} else if config != nil && strings.TrimSpace(config.Config.ClientVersion) != "" {
		req.Header.Set("Version", config.GetClientVersion())
	}

	if val := filtered.Get("Session_id"); val != "" {
		req.Header.Set("Session_id", val)
	} else {
		req.Header.Set("Session_id", uuid.NewString())
	}

	if val := filtered.Get("Openai-Beta"); val != "" {
		req.Header.Set("Openai-Beta", val)
	}
	if val := filtered.Get("X-Codex-Beta-Features"); val != "" {
		req.Header.Set("X-Codex-Beta-Features", val)
	}
	if val := filtered.Get("X-Codex-Turn-Metadata"); val != "" {
		req.Header.Set("X-Codex-Turn-Metadata", val)
	}
	if val := filtered.Get("X-Client-Request-Id"); val != "" {
		req.Header.Set("X-Client-Request-Id", val)
	}

	req.Header.Set("User-Agent", userAgent)

	if isStreaming {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("Connection", "Keep-Alive")

	// 账号池当前都是 OAuth 模式，需要携带账号上下文头。
	isAPIKeyMode := false
	if !isAPIKeyMode {
		if val := filtered.Get("Originator"); val != "" {
			req.Header.Set("Originator", val)
		} else {
			req.Header.Set("Originator", originator)
		}
		if accountID != "" {
			req.Header.Set("Chatgpt-Account-Id", accountID)
		}
	}
}

// isCompactResponsesPath 判断请求路径是否是 compact endpoint。
func isCompactResponsesPath(requestPath string) bool {
	return proxy.IsCodexCompactResponsesPath(requestPath)
}

// getCodexUpstreamURL 根据配置和请求路径构造 Codex 上游地址。
func getCodexUpstreamURL(config *codexShared.CodexMultiConfig, requestPath string) string {
	baseURL := codexShared.DefaultCodexBaseURL
	if config != nil {
		if cfgBaseURL := strings.TrimSpace(config.GetBaseURL()); cfgBaseURL != "" {
			baseURL = cfgBaseURL
		}
	}

	// 归一化 baseURL，避免用户把 endpoint 路径重复写入配置。
	baseURL = strings.TrimSuffix(baseURL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/responses/compact")
	baseURL = strings.TrimSuffix(baseURL, "/responses")

	// 根据下游请求路径选择普通或 compact endpoint。
	if isCompactResponsesPath(requestPath) {
		return baseURL + "/responses/compact"
	}
	return baseURL + "/responses"
}
