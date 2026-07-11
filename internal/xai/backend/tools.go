package backend

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	toolCustom          = "custom"
	toolFunction        = "function"
	toolImageGeneration = "image_generation"
	toolNamespace       = "namespace"
	toolSearch          = "tool_search"
	toolWebSearch       = "web_search"

	codexAppNamespace       = "codex_app"
	automationUpdateTool    = "automation_update"
	safeFunctionParameters  = `{"type":"object","properties":{},"additionalProperties":true}`
)

// NormalizeTools 展开 namespace、过滤/改写上游不支持或会卡死的 tool schema。
func NormalizeTools(body []byte) []byte {
	tools := gjson.GetBytes(body, "tools")
	if !tools.Exists() || !tools.IsArray() {
		return body
	}

	changed := false
	filtered := []byte(`[]`)
	for _, tool := range tools.Array() {
		toolType := tool.Get("type").String()
		if toolType == toolNamespace {
			changed = true
			namespaceName := tool.Get("name").String()
			if namespaceTools := tool.Get("tools"); namespaceTools.IsArray() {
				for _, nested := range namespaceTools.Array() {
					raw, nestedChanged, ok := normalizeTool(nested, namespaceName)
					if !ok {
						return body
					}
					changed = changed || nestedChanged
					if len(raw) == 0 {
						continue
					}
					updated, err := sjson.SetRawBytes(filtered, "-1", raw)
					if err != nil {
						return body
					}
					filtered = updated
				}
			}
			continue
		}
		raw, toolChanged, ok := normalizeTool(tool, "")
		if !ok {
			return body
		}
		changed = changed || toolChanged
		if len(raw) == 0 {
			continue
		}
		updated, err := sjson.SetRawBytes(filtered, "-1", raw)
		if err != nil {
			return body
		}
		filtered = updated
	}
	if !changed {
		// 仍可能需要 tool_choice 清理
		return NormalizeToolChoice(body)
	}
	updated, err := sjson.SetRawBytes(body, "tools", filtered)
	if err != nil {
		return body
	}
	return NormalizeToolChoice(updated)
}

func normalizeTool(tool gjson.Result, namespaceName string) ([]byte, bool, bool) {
	toolType := tool.Get("type").String()
	changed := false
	// tool_search / image_generation 由上游侧处理，Responses tools 列表中剥离
	if toolType == toolSearch || toolType == toolImageGeneration {
		return nil, true, true
	}
	raw := []byte(tool.Raw)
	if toolType == toolCustom {
		if tool.Get("name").String() == "apply_patch" {
			return nil, true, true
		}
		updated, err := sjson.SetBytes(raw, "type", toolFunction)
		if err != nil {
			return nil, false, false
		}
		raw = updated
		toolType = toolFunction
		changed = true
	}
	if toolType == toolWebSearch && tool.Get("external_web_access").Exists() {
		updated, err := sjson.DeleteBytes(raw, "external_web_access")
		if err != nil {
			return nil, false, false
		}
		raw = updated
		changed = true
	}
	if toolType == toolFunction && !gjson.GetBytes(raw, "parameters").Exists() {
		updated, err := sjson.SetRawBytes(raw, "parameters", []byte(`{"type":"object","properties":{}}`))
		if err != nil {
			return nil, false, false
		}
		raw = updated
		changed = true
	}
	// Codex Desktop codex_app.automation_update 大 schema 会导致上游卡 SSE
	if toolType == toolFunction && needsSimplifiedParameters(tool, namespaceName) {
		updated, err := sjson.SetRawBytes(raw, "parameters", []byte(safeFunctionParameters))
		if err != nil {
			return nil, false, false
		}
		raw = updated
		if strict := tool.Get("strict"); strict.Exists() && strict.Bool() {
			updated, err = sjson.SetBytes(raw, "strict", false)
			if err != nil {
				return nil, false, false
			}
			raw = updated
		}
		changed = true
	}
	return raw, changed, true
}

func needsSimplifiedParameters(tool gjson.Result, namespaceName string) bool {
	return strings.EqualFold(strings.TrimSpace(tool.Get("type").String()), toolFunction) &&
		strings.EqualFold(strings.TrimSpace(namespaceName), codexAppNamespace) &&
		strings.EqualFold(strings.TrimSpace(tool.Get("name").String()), automationUpdateTool)
}

// NormalizeToolChoice 无 tools 时删除 tool_choice / parallel_tool_calls。
func NormalizeToolChoice(body []byte) []byte {
	tools := gjson.GetBytes(body, "tools")
	hasTools := tools.Exists() && tools.IsArray() && len(tools.Array()) > 0
	if hasTools {
		return body
	}
	if tools.Exists() {
		body, _ = sjson.DeleteBytes(body, "tools")
	}
	if gjson.GetBytes(body, "tool_choice").Exists() {
		body, _ = sjson.DeleteBytes(body, "tool_choice")
	}
	if gjson.GetBytes(body, "parallel_tool_calls").Exists() {
		body, _ = sjson.DeleteBytes(body, "parallel_tool_calls")
	}
	return body
}
