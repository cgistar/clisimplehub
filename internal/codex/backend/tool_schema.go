package backend

import (
	"math/big"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const codexComplexUnionBranchThreshold = 8

// normalizeCodexToolSchemas 将纯 const 的大型 oneOf/anyOf 联合折叠为等价 enum，避免上游拒识。
func normalizeCodexToolSchemas(body []byte) []byte {
	tools := gjson.GetBytes(body, "tools")
	if !tools.Exists() || !tools.IsArray() || len(tools.Array()) == 0 {
		return body
	}

	changed := false
	for i, tool := range tools.Array() {
		updatedTool, toolChanged := normalizeCodexTool(tool)
		if !toolChanged {
			continue
		}
		next, errSet := sjson.SetRawBytes(body, "tools."+strconv.Itoa(i), updatedTool)
		if errSet == nil {
			body = next
			changed = true
		}
	}
	if !changed {
		return body
	}
	return body
}

func normalizeCodexTool(tool gjson.Result) ([]byte, bool) {
	toolType := tool.Get("type").String()
	if toolType == "namespace" {
		nestedTools := tool.Get("tools")
		if !nestedTools.IsArray() || len(nestedTools.Array()) == 0 {
			return nil, false
		}
		changed := false
		raw := []byte(tool.Raw)
		for j, nestedTool := range nestedTools.Array() {
			updatedNested, nestedChanged := normalizeCodexTool(nestedTool)
			if !nestedChanged {
				continue
			}
			next, errSet := sjson.SetRawBytes(raw, "tools."+strconv.Itoa(j), updatedNested)
			if errSet == nil {
				raw = next
				changed = true
			}
		}
		return raw, changed
	}
	if toolType != "function" && toolType != "custom" {
		return nil, false
	}
	params := tool.Get("parameters")
	if !params.Exists() || !params.IsObject() {
		return nil, false
	}
	updatedParams, paramsChanged := normalizeCodexParameters(params)
	if !paramsChanged {
		return nil, false
	}
	updatedTool, errSet := sjson.SetRawBytes([]byte(tool.Raw), "parameters", updatedParams)
	if errSet != nil {
		return nil, false
	}
	return updatedTool, true
}

func normalizeCodexParameters(params gjson.Result) ([]byte, bool) {
	rawParams := []byte(params.Raw)
	changed := false
	properties := params.Get("properties")
	if !properties.Exists() || !properties.IsObject() {
		return rawParams, false
	}
	for propName, propVal := range properties.Map() {
		updatedProp, propChanged := normalizeCodexPropertySchema(propVal)
		if !propChanged {
			continue
		}
		next, errSet := sjson.SetRawBytes(rawParams, "properties."+escapeCodexSjsonKey(propName), updatedProp)
		if errSet == nil {
			rawParams = next
			changed = true
		}
	}
	return rawParams, changed
}

func normalizeCodexPropertySchema(prop gjson.Result) ([]byte, bool) {
	if !prop.IsObject() {
		return nil, false
	}
	hasOneOf := prop.Get("oneOf").Exists()
	hasAnyOf := prop.Get("anyOf").Exists()
	if hasOneOf && hasAnyOf {
		return nil, false
	}
	unionName := ""
	if hasOneOf {
		unionName = "oneOf"
	} else if hasAnyOf {
		unionName = "anyOf"
	} else {
		return nil, false
	}
	union := prop.Get(unionName)
	if !union.IsArray() || len(union.Array()) < codexComplexUnionBranchThreshold {
		return nil, false
	}

	branches := union.Array()
	constRawValues := make([]string, 0, len(branches))
	constSemanticKeys := make([]string, 0, len(branches))
	seenSemanticKeys := make(map[string]struct{}, len(branches))
	for _, branch := range branches {
		canonicalKey, rawJSON, ok := isPureConstBranch(branch)
		if !ok {
			return nil, false
		}
		if _, seen := seenSemanticKeys[canonicalKey]; seen {
			return nil, false
		}
		seenSemanticKeys[canonicalKey] = struct{}{}
		constSemanticKeys = append(constSemanticKeys, canonicalKey)
		constRawValues = append(constRawValues, rawJSON)
	}
	if len(constRawValues) == 0 {
		return nil, false
	}

	rawProp := []byte(prop.Raw)
	existingEnum := prop.Get("enum")
	if existingEnum.Exists() && existingEnum.IsArray() {
		existingEnumKeys := make([]string, 0, len(existingEnum.Array()))
		for _, v := range existingEnum.Array() {
			key, ok := canonicalJSONValueKey(v)
			if !ok {
				return nil, false
			}
			existingEnumKeys = append(existingEnumKeys, key)
		}
		if equalCanonicalSets(existingEnumKeys, constSemanticKeys) {
			rawProp, _ = sjson.DeleteBytes(rawProp, unionName)
			return rawProp, true
		}
		return nil, false
	}

	rawEnumJSON := []byte("[" + strings.Join(constRawValues, ",") + "]")
	rawProp, errEnum := sjson.SetRawBytes(rawProp, "enum", rawEnumJSON)
	if errEnum != nil {
		return nil, false
	}
	rawProp, _ = sjson.DeleteBytes(rawProp, unionName)
	return rawProp, true
}

func isPureConstBranch(branch gjson.Result) (canonicalKey string, rawJSON string, ok bool) {
	if !branch.IsObject() {
		return "", "", false
	}
	constVal := branch.Get("const")
	if !constVal.Exists() {
		return "", "", false
	}
	for key := range branch.Map() {
		if key != "const" && key != "description" && key != "title" {
			return "", "", false
		}
	}
	key, ok := canonicalJSONValueKey(constVal)
	if !ok {
		return "", "", false
	}
	return key, constVal.Raw, true
}

func canonicalJSONValueKey(val gjson.Result) (string, bool) {
	switch val.Type {
	case gjson.String:
		return "s:" + val.String(), true
	case gjson.Number:
		raw := strings.TrimSpace(val.Raw)
		var r big.Rat
		if _, ok := r.SetString(raw); ok {
			return "n:" + r.RatString(), true
		}
		return "n:" + raw, true
	case gjson.True:
		return "b:true", true
	case gjson.False:
		return "b:false", true
	case gjson.Null:
		return "null", true
	default:
		return "", false
	}
}

func equalCanonicalSets(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	setA := make(map[string]struct{}, len(a))
	for _, v := range a {
		setA[v] = struct{}{}
	}
	for _, v := range b {
		if _, ok := setA[v]; !ok {
			return false
		}
	}
	return len(setA) == len(a)
}

func escapeCodexSjsonKey(key string) string {
	key = strings.ReplaceAll(key, `\`, `\\`)
	key = strings.ReplaceAll(key, `.`, `\.`)
	key = strings.ReplaceAll(key, `:`, `\:`)
	return key
}
