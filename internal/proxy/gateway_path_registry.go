package proxy

import "strings"

type pathMatchKind uint8

const (
	pathMatchExact pathMatchKind = iota + 1
	pathMatchPrefix
	pathMatchSuffix
	pathMatchContains
)

type gatewayPathRule struct {
	name          string
	match         pathMatchKind
	pattern       string
	interfaceType InterfaceType
	knownForward  bool
	retryable     bool
	anthropic     bool
	unifiedModels bool
	claudeCount   bool
	codexCompact  bool
}

type GatewayPathRegistry struct {
	rules []gatewayPathRule
}

var defaultGatewayPathRegistry = newDefaultGatewayPathRegistry()

func newDefaultGatewayPathRegistry() *GatewayPathRegistry {
	r := NewGatewayPathRegistry()
	r.RegisterRule(gatewayPathRule{
		name:          "claude_count_tokens",
		match:         pathMatchExact,
		pattern:       "/v1/messages/count_tokens",
		interfaceType: InterfaceTypeClaude,
		knownForward:  true,
		retryable:     true,
		anthropic:     true,
		claudeCount:   true,
	})
	r.RegisterRule(gatewayPathRule{
		name:          "unified_models",
		match:         pathMatchExact,
		pattern:       "/v1/models",
		knownForward:  true,
		anthropic:     true,
		unifiedModels: true,
	})
	r.RegisterRule(gatewayPathRule{
		name:          "codex_unified_models",
		match:         pathMatchSuffix,
		pattern:       "/codex/v1/models",
		interfaceType: InterfaceTypeCodex,
		knownForward:  true,
		unifiedModels: true,
	})
	r.RegisterRule(gatewayPathRule{
		name:          "claude_messages",
		match:         pathMatchPrefix,
		pattern:       "/v1/messages",
		interfaceType: InterfaceTypeClaude,
		knownForward:  true,
		retryable:     true,
		anthropic:     true,
	})
	r.RegisterRule(gatewayPathRule{
		name:          "chat_completions_prefix",
		match:         pathMatchPrefix,
		pattern:       "/v1/chat/completions",
		interfaceType: InterfaceTypeChat,
		knownForward:  true,
	})
	r.RegisterRule(gatewayPathRule{
		name:          "chat_completions_suffix",
		match:         pathMatchSuffix,
		pattern:       "/chat/completions",
		interfaceType: InterfaceTypeChat,
		knownForward:  true,
	})
	r.RegisterRule(gatewayPathRule{
		name:          "codex_responses",
		match:         pathMatchSuffix,
		pattern:       "/responses",
		interfaceType: InterfaceTypeCodex,
		knownForward:  true,
		retryable:     true,
	})
	r.RegisterRule(gatewayPathRule{
		name:          "codex_responses_compact",
		match:         pathMatchSuffix,
		pattern:       "/responses/compact",
		interfaceType: InterfaceTypeCodex,
		knownForward:  true,
		retryable:     true,
		codexCompact:  true,
	})
	r.RegisterRule(gatewayPathRule{
		name:          "codex_images_generations",
		match:         pathMatchSuffix,
		pattern:       "/images/generations",
		interfaceType: InterfaceTypeCodex,
		knownForward:  true,
		retryable:     true,
	})
	r.RegisterRule(gatewayPathRule{
		name:          "codex_images_edits",
		match:         pathMatchSuffix,
		pattern:       "/images/edits",
		interfaceType: InterfaceTypeCodex,
		knownForward:  true,
		retryable:     true,
	})
	r.RegisterRule(gatewayPathRule{
		name:          "gemini_contains",
		match:         pathMatchContains,
		pattern:       "/gemini",
		interfaceType: InterfaceTypeGemini,
		knownForward:  true,
	})
	r.RegisterRule(gatewayPathRule{
		name:          "chat_prefix",
		match:         pathMatchPrefix,
		pattern:       "/chat",
		interfaceType: InterfaceTypeChat,
		knownForward:  true,
	})
	return r
}

func NewGatewayPathRegistry() *GatewayPathRegistry {
	return &GatewayPathRegistry{
		rules: make([]gatewayPathRule, 0, 16),
	}
}

func (r *GatewayPathRegistry) RegisterRule(rule gatewayPathRule) {
	if r == nil {
		return
	}

	pattern := normalizeRequestPath(rule.pattern)
	if pattern == "" {
		return
	}
	if rule.match == pathMatchSuffix {
		pattern = strings.TrimRight(pattern, "/")
	}
	r.rules = append(r.rules, gatewayPathRule{
		name:          strings.TrimSpace(rule.name),
		match:         rule.match,
		pattern:       pattern,
		interfaceType: rule.interfaceType,
		knownForward:  rule.knownForward,
		retryable:     rule.retryable,
		anthropic:     rule.anthropic,
		unifiedModels: rule.unifiedModels,
		claudeCount:   rule.claudeCount,
		codexCompact:  rule.codexCompact,
	})
}

func normalizeRequestPath(path string) string {
	return strings.ToLower(strings.TrimSpace(path))
}

func (r *GatewayPathRegistry) matchedRules(path string) []gatewayPathRule {
	if r == nil {
		return nil
	}
	normalized := normalizeRequestPath(path)
	normalizedTrimmed := strings.TrimRight(normalized, "/")
	out := make([]gatewayPathRule, 0, 4)
	for _, rule := range r.rules {
		if matchesGatewayPathRule(rule, normalized, normalizedTrimmed) {
			out = append(out, rule)
		}
	}
	return out
}

func matchesGatewayPathRule(rule gatewayPathRule, normalizedPath string, normalizedTrimmed string) bool {
	switch rule.match {
	case pathMatchExact:
		return normalizedPath == rule.pattern
	case pathMatchPrefix:
		return strings.HasPrefix(normalizedPath, rule.pattern)
	case pathMatchSuffix:
		return strings.HasSuffix(normalizedTrimmed, rule.pattern)
	case pathMatchContains:
		return strings.Contains(normalizedPath, rule.pattern)
	default:
		return false
	}
}

func (r *GatewayPathRegistry) DetectInterfaceType(path string) InterfaceType {
	for _, rule := range r.matchedRules(path) {
		if rule.interfaceType != "" {
			return rule.interfaceType
		}
	}
	return InterfaceTypeClaude
}

func (r *GatewayPathRegistry) IsUnifiedModelsPath(path string) bool {
	for _, rule := range r.matchedRules(path) {
		if rule.unifiedModels {
			return true
		}
	}
	return false
}

func (r *GatewayPathRegistry) IsClaudeCountTokensPath(path string) bool {
	for _, rule := range r.matchedRules(path) {
		if rule.claudeCount {
			return true
		}
	}
	return false
}

func (r *GatewayPathRegistry) IsCodexCompactResponsesPath(path string) bool {
	for _, rule := range r.matchedRules(path) {
		if rule.codexCompact {
			return true
		}
	}
	return false
}

func (r *GatewayPathRegistry) IsAnthropicCompatiblePath(path string) bool {
	for _, rule := range r.matchedRules(path) {
		if rule.anthropic {
			return true
		}
	}
	return false
}

func (r *GatewayPathRegistry) IsKnownProxyForwardPath(path string) bool {
	for _, rule := range r.matchedRules(path) {
		if rule.knownForward {
			return true
		}
	}
	return false
}

func (r *GatewayPathRegistry) IsRetryablePath(path string) bool {
	for _, rule := range r.matchedRules(path) {
		if rule.retryable {
			return true
		}
	}
	return false
}

func (r *GatewayPathRegistry) ShouldRecordUsageStats(interfaceType InterfaceType, path string) bool {
	if interfaceType == InterfaceTypeClaude || interfaceType == InterfaceTypeCodex {
		return r.IsRetryablePath(path)
	}
	return true
}

func DetectInterfaceTypeByPath(path string) InterfaceType {
	return defaultGatewayPathRegistry.DetectInterfaceType(path)
}

func IsUnifiedModelsPath(path string) bool {
	return defaultGatewayPathRegistry.IsUnifiedModelsPath(path)
}

func IsClaudeCountTokensPath(path string) bool {
	return defaultGatewayPathRegistry.IsClaudeCountTokensPath(path)
}

func IsCodexCompactResponsesPath(path string) bool {
	return defaultGatewayPathRegistry.IsCodexCompactResponsesPath(path)
}

func IsAnthropicCompatiblePath(path string) bool {
	return defaultGatewayPathRegistry.IsAnthropicCompatiblePath(path)
}

func IsKnownProxyForwardPath(path string) bool {
	return defaultGatewayPathRegistry.IsKnownProxyForwardPath(path)
}

func IsRetryablePath(path string) bool {
	return defaultGatewayPathRegistry.IsRetryablePath(path)
}

func ShouldRecordUsageStats(interfaceType InterfaceType, path string) bool {
	return defaultGatewayPathRegistry.ShouldRecordUsageStats(interfaceType, path)
}
