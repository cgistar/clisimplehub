package middleware

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// SuffixResult 保存 model 名称后缀解析结果。
// 格式: model-name(value) → ModelName="model-name", RawSuffix="value"
type SuffixResult struct {
	ModelName string
	HasSuffix bool
	RawSuffix string
}

// ParseModelSuffix 解析 model 名中的括号后缀。
func ParseModelSuffix(model string) SuffixResult {
	lastOpen := strings.LastIndex(model, "(")
	if lastOpen == -1 || !strings.HasSuffix(model, ")") {
		return SuffixResult{ModelName: model}
	}
	return SuffixResult{
		ModelName: model[:lastOpen],
		HasSuffix: true,
		RawSuffix: model[lastOpen+1 : len(model)-1],
	}
}

// normalizeModel 解析 model suffix 并将 base model 写回 body。
func normalizeModel(body []byte) ([]byte, SuffixResult) {
	model := gjson.GetBytes(body, "model").String()
	if model == "" {
		return body, SuffixResult{}
	}
	sr := ParseModelSuffix(model)
	if !sr.HasSuffix {
		return body, sr
	}
	body, _ = sjson.SetBytes(body, "model", sr.ModelName)
	return body, sr
}

// disableThinkingIfToolChoiceForced 当 tool_choice.type 为 "any" 或 "tool" 时
// 禁用 thinking（Anthropic API 硬约束）。
func disableThinkingIfToolChoiceForced(body []byte) []byte {
	tcType := gjson.GetBytes(body, "tool_choice.type").String()
	if tcType != "any" && tcType != "tool" {
		return body
	}
	body, _ = sjson.DeleteBytes(body, "thinking")
	body, _ = sjson.DeleteBytes(body, "output_config.effort")
	// 清理空的 output_config
	oc := gjson.GetBytes(body, "output_config")
	if oc.Exists() && oc.IsObject() && len(oc.Map()) == 0 {
		body, _ = sjson.DeleteBytes(body, "output_config")
	}
	return body
}

// extractAndRemoveBetas 从 body 中提取 "betas" 字段并删除，返回 betas 列表。
func extractAndRemoveBetas(body []byte) ([]string, []byte) {
	betasResult := gjson.GetBytes(body, "betas")
	if !betasResult.Exists() {
		return nil, body
	}
	var betas []string
	if betasResult.IsArray() {
		for _, item := range betasResult.Array() {
			if s := strings.TrimSpace(item.String()); s != "" {
				betas = append(betas, s)
			}
		}
	} else if s := strings.TrimSpace(betasResult.String()); s != "" {
		betas = append(betas, s)
	}
	body, _ = sjson.DeleteBytes(body, "betas")
	return betas, body
}
