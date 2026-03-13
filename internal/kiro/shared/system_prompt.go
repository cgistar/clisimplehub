package shared

import "strings"

const (
	MiddlewareBillingPrefix = "x-anthropic-billing-header:"
	ClaudeAgentIdentifier   = "You are a Claude agent, built on Anthropic's Claude Agent SDK."
)

func StripInjectedMiddlewareSystemText(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}

	lines := strings.Split(text, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if shouldStripInjectedSystemLine(trimmed) {
			continue
		}
		filtered = append(filtered, line)
	}

	return strings.TrimSpace(strings.Join(filtered, "\n"))
}

func shouldStripInjectedSystemLine(line string) bool {
	if line == "" {
		return false
	}
	if strings.HasPrefix(line, MiddlewareBillingPrefix) {
		return true
	}
	return line == ClaudeAgentIdentifier
}
