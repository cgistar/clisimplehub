package backend

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 支持 reasoning.effort 的模型
var reasoningEffortModels = map[string]struct{}{
	"grok-build-0.1":             {},
	"grok-4.5":                   {},
	"grok-4.3":                   {},
	"grok-4.20-0309-reasoning":   {},
	"grok-4.20-multi-agent-0309": {},
}

type ModelSuffix struct {
	ModelName string
	HasSuffix bool
	RawSuffix string
}

func ParseModelSuffix(model string) ModelSuffix {
	model = strings.TrimSpace(model)
	if model == "" {
		return ModelSuffix{}
	}
	lastOpen := strings.LastIndex(model, "(")
	if lastOpen < 0 || !strings.HasSuffix(model, ")") || lastOpen >= len(model)-2 {
		return ModelSuffix{ModelName: model}
	}
	return ModelSuffix{
		ModelName: strings.TrimSpace(model[:lastOpen]),
		HasSuffix: true,
		RawSuffix: strings.ToLower(strings.TrimSpace(model[lastOpen+1 : len(model)-1])),
	}
}

func BaseModelName(model string) string {
	name := ParseModelSuffix(model).ModelName
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	return strings.TrimSpace(name)
}

func SupportsReasoningEffort(model string) bool {
	name := strings.ToLower(BaseModelName(model))
	if name == "" {
		return false
	}
	if strings.Contains(name, "non-reasoning") {
		return false
	}
	if strings.HasPrefix(name, "grok-composer-") {
		return false
	}
	if strings.HasPrefix(name, "grok-imagine-") {
		return false
	}
	_, ok := reasoningEffortModels[name]
	return ok
}

func RequiresIsolatedConversation(model string) bool {
	return strings.HasPrefix(strings.ToLower(BaseModelName(model)), "grok-composer-")
}

// xAI 上游支持的 reasoning.effort 离散值（不含 none/auto/minimal）。
// cli-chat-proxy 对 none 返回 invalid-argument。
var xaiAllowedReasoningEfforts = map[string]struct{}{
	"low": {}, "medium": {}, "high": {}, "xhigh": {}, "max": {},
}

// NormalizeXAIReasoningEffort 将 Claude/通用 effort 映射为 xAI 可接受值。
// "none" / 空 → 删除 effort（关闭 thinking）；"auto"/"minimal" → 合理默认。
func NormalizeXAIReasoningEffort(effort string) (normalized string, drop bool) {
	effort = strings.ToLower(strings.TrimSpace(effort))
	switch effort {
	case "", "none", "disabled", "off":
		return "", true
	case "auto", "-1":
		return "high", false
	case "minimal", "min":
		return "low", false
	case "low", "medium", "high", "xhigh", "max":
		return effort, false
	default:
		// 未知值不传，避免上游 400
		return "", true
	}
}

// ApplySuffixThinking 将 model 后缀（如 grok-4.3(high)）写入 reasoning.effort。
func ApplySuffixThinking(body []byte, model string) []byte {
	suffix := ParseModelSuffix(model)
	if !suffix.HasSuffix {
		// 也尝试 body.model
		if m := gjson.GetBytes(body, "model").String(); m != "" {
			suffix = ParseModelSuffix(m)
		}
	}
	if !suffix.HasSuffix {
		return body
	}
	effort, drop := NormalizeXAIReasoningEffort(suffix.RawSuffix)
	if drop {
		if suffix.RawSuffix == "none" || suffix.RawSuffix == "disabled" || suffix.RawSuffix == "off" {
			return stripReasoningEffort(body)
		}
		return body
	}
	if !SupportsReasoningEffort(suffix.ModelName) {
		return body
	}
	out, err := sjson.SetBytes(body, "reasoning.effort", effort)
	if err != nil {
		return body
	}
	return out
}

// ApplyThinking 综合 suffix 与既有 reasoning 字段，并规范化 xAI effort。
func ApplyThinking(body []byte, model string) []byte {
	body = ApplySuffixThinking(body, model)
	base := BaseModelName(model)
	if base == "" {
		base = BaseModelName(gjson.GetBytes(body, "model").String())
	}
	if !SupportsReasoningEffort(base) {
		return stripReasoningEffort(body)
	}
	return normalizeExistingReasoningEffort(body)
}

// normalizeExistingReasoningEffort 修正 body 中已有的 effort（含 Claude disabled→none）。
func normalizeExistingReasoningEffort(body []byte) []byte {
	if !gjson.GetBytes(body, "reasoning.effort").Exists() {
		return body
	}
	raw := gjson.GetBytes(body, "reasoning.effort").String()
	effort, drop := NormalizeXAIReasoningEffort(raw)
	if drop {
		return stripReasoningEffort(body)
	}
	if effort == raw {
		// 仍校验白名单
		if _, ok := xaiAllowedReasoningEfforts[effort]; !ok {
			return stripReasoningEffort(body)
		}
		return body
	}
	out, err := sjson.SetBytes(body, "reasoning.effort", effort)
	if err != nil {
		return body
	}
	return out
}

func stripReasoningEffort(body []byte) []byte {
	if !gjson.GetBytes(body, "reasoning.effort").Exists() {
		// 若整块 reasoning 为空对象则清理
		if reasoning := gjson.GetBytes(body, "reasoning"); reasoning.Exists() && reasoning.IsObject() && len(reasoning.Map()) == 0 {
			body, _ = sjson.DeleteBytes(body, "reasoning")
		}
		return body
	}
	body, _ = sjson.DeleteBytes(body, "reasoning.effort")
	// 仅 summary 无 effort 时保留 summary；若 summary 也不需要可整删
	if reasoning := gjson.GetBytes(body, "reasoning"); reasoning.Exists() && reasoning.IsObject() {
		// 无 effort 时上游可能仍拒 summary-only；稳妥起见整段删除
		if !gjson.GetBytes(body, "reasoning.effort").Exists() {
			body, _ = sjson.DeleteBytes(body, "reasoning")
		}
	}
	return body
}

// rewriteModelInBody 将 body.model 设为 base name。
func rewriteModelInBody(body []byte, baseModel string) []byte {
	baseModel = strings.TrimSpace(baseModel)
	if baseModel == "" || len(body) == 0 {
		return body
	}
	out, err := sjson.SetBytes(body, "model", baseModel)
	if err != nil {
		return body
	}
	return out
}

// setStreamFlag 强制 stream 字段与调用一致。
func setStreamFlag(body []byte, stream bool) []byte {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body
	}
	out, err := sjson.SetBytes(body, "stream", stream)
	if err != nil {
		return body
	}
	return out
}

func isValidJSONObject(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var v any
	return json.Unmarshal(body, &v) == nil && gjson.ValidBytes(body)
}
