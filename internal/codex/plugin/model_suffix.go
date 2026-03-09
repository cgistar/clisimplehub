package codexplugin

import (
	"encoding/json"
	"strings"
)

type modelSuffixResult struct {
	modelName string
	hasSuffix bool
	rawSuffix string
}

func parseModelSuffix(model string) modelSuffixResult {
	model = strings.TrimSpace(model)
	if model == "" {
		return modelSuffixResult{}
	}

	lastOpen := strings.LastIndex(model, "(")
	if lastOpen == -1 || !strings.HasSuffix(model, ")") {
		return modelSuffixResult{
			modelName: model,
			hasSuffix: false,
		}
	}

	return modelSuffixResult{
		modelName: strings.TrimSpace(model[:lastOpen]),
		hasSuffix: true,
		rawSuffix: strings.ToLower(strings.TrimSpace(model[lastOpen+1 : len(model)-1])),
	}
}

func baseModelName(model string) string {
	return parseModelSuffix(model).modelName
}

func parseEffortSuffix(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "minimal", "low", "medium", "high", "xhigh", "max", "none", "auto":
		return strings.ToLower(strings.TrimSpace(raw)), true
	default:
		return "", false
	}
}

func applySuffixThinkingToCodexBody(body []byte, model string) ([]byte, bool) {
	suffix := parseModelSuffix(model)
	if !suffix.hasSuffix {
		return body, false
	}

	effort, ok := parseEffortSuffix(suffix.rawSuffix)
	if !ok {
		return body, false
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, false
	}

	reasoning, _ := payload["reasoning"].(map[string]any)
	if reasoning == nil {
		reasoning = make(map[string]any)
	}
	reasoning["effort"] = effort
	payload["reasoning"] = reasoning

	updated, err := json.Marshal(payload)
	if err != nil {
		return body, false
	}
	return updated, true
}
