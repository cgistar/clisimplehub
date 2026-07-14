// Package thinking 提供 xAI Responses 的完整 thinking 配置管线
package thinking

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ---------------------------------------------------------------------------
// types
// ---------------------------------------------------------------------------

// ThinkingMode 配置模式。
type ThinkingMode int

const (
	ModeBudget ThinkingMode = iota
	ModeLevel
	ModeNone
	ModeAuto
)

func (m ThinkingMode) String() string {
	switch m {
	case ModeBudget:
		return "budget"
	case ModeLevel:
		return "level"
	case ModeNone:
		return "none"
	case ModeAuto:
		return "auto"
	default:
		return "unknown"
	}
}

// ThinkingLevel 离散 effort。
type ThinkingLevel string

const (
	LevelNone    ThinkingLevel = "none"
	LevelAuto    ThinkingLevel = "auto"
	LevelMinimal ThinkingLevel = "minimal"
	LevelLow     ThinkingLevel = "low"
	LevelMedium  ThinkingLevel = "medium"
	LevelHigh    ThinkingLevel = "high"
	LevelXHigh   ThinkingLevel = "xhigh"
	LevelMax     ThinkingLevel = "max"
)

// ThinkingConfig 统一配置。
type ThinkingConfig struct {
	Mode   ThinkingMode
	Budget int
	Level  ThinkingLevel
}

// SuffixResult 模型名后缀解析结果。
type SuffixResult struct {
	ModelName string
	HasSuffix bool
	RawSuffix string
}

// ThinkingSupport 模型 thinking 能力。
type ThinkingSupport struct {
	Levels         []string
	ZeroAllowed    bool
	DynamicAllowed bool
	Min            int
	Max            int
}

// ModelInfo 模型元数据；nil Thinking 表示无 thinking 能力。
// ModelInfo 为 nil 时按 user-defined 透传。
type ModelInfo struct {
	ID          string
	Type        string
	Thinking    *ThinkingSupport
	UserDefined bool
}

// ModelCapability 能力分类。
type ModelCapability int

const (
	CapabilityUnknown ModelCapability = iota - 1
	CapabilityNone
	CapabilityBudgetOnly
	CapabilityLevelOnly
	CapabilityHybrid
)

// ---------------------------------------------------------------------------
// errors
// ---------------------------------------------------------------------------

// ErrorCode thinking 错误码。
type ErrorCode string

const (
	ErrInvalidSuffix        ErrorCode = "INVALID_SUFFIX"
	ErrUnknownLevel         ErrorCode = "UNKNOWN_LEVEL"
	ErrThinkingNotSupported ErrorCode = "THINKING_NOT_SUPPORTED"
	ErrLevelNotSupported    ErrorCode = "LEVEL_NOT_SUPPORTED"
	ErrBudgetOutOfRange     ErrorCode = "BUDGET_OUT_OF_RANGE"
)

// ThinkingError 结构化 validation 错误（HTTP 400）。
type ThinkingError struct {
	Code    ErrorCode
	Message string
	Model   string
}

func (e *ThinkingError) Error() string { return e.Message }

func (e *ThinkingError) StatusCode() int { return http.StatusBadRequest }

func NewThinkingError(code ErrorCode, message string) *ThinkingError {
	return &ThinkingError{Code: code, Message: message}
}

func NewThinkingErrorWithModel(code ErrorCode, message, model string) *ThinkingError {
	return &ThinkingError{Code: code, Message: message, Model: model}
}

// IsThinkingError 判断是否为 ThinkingError。
func IsThinkingError(err error) bool {
	_, ok := err.(*ThinkingError)
	return ok
}

// ---------------------------------------------------------------------------
// suffix
// ---------------------------------------------------------------------------

// ParseSuffix 解析 model(value) 后缀。
func ParseSuffix(model string) SuffixResult {
	model = strings.TrimSpace(model)
	if model == "" {
		return SuffixResult{}
	}
	lastOpen := strings.LastIndex(model, "(")
	if lastOpen < 0 || !strings.HasSuffix(model, ")") || lastOpen >= len(model)-2 {
		return SuffixResult{ModelName: model}
	}
	return SuffixResult{
		ModelName: strings.TrimSpace(model[:lastOpen]),
		HasSuffix: true,
		RawSuffix: strings.TrimSpace(model[lastOpen+1 : len(model)-1]),
	}
}

// ParseNumericSuffix 非负整数预算。
func ParseNumericSuffix(rawSuffix string) (budget int, ok bool) {
	if rawSuffix == "" {
		return 0, false
	}
	value, err := strconv.Atoi(rawSuffix)
	if err != nil || value < 0 {
		return 0, false
	}
	return value, true
}

// ParseSpecialSuffix none/auto/-1。
func ParseSpecialSuffix(rawSuffix string) (mode ThinkingMode, ok bool) {
	switch strings.ToLower(strings.TrimSpace(rawSuffix)) {
	case "none":
		return ModeNone, true
	case "auto", "-1":
		return ModeAuto, true
	default:
		return ModeBudget, false
	}
}

// ParseLevelSuffix 离散 level（不含 none/auto）。
func ParseLevelSuffix(rawSuffix string) (level ThinkingLevel, ok bool) {
	switch strings.ToLower(strings.TrimSpace(rawSuffix)) {
	case "minimal":
		return LevelMinimal, true
	case "low":
		return LevelLow, true
	case "medium":
		return LevelMedium, true
	case "high":
		return LevelHigh, true
	case "xhigh":
		return LevelXHigh, true
	case "max":
		return LevelMax, true
	default:
		return "", false
	}
}

func parseSuffixToConfig(rawSuffix string) ThinkingConfig {
	if mode, ok := ParseSpecialSuffix(rawSuffix); ok {
		switch mode {
		case ModeNone:
			return ThinkingConfig{Mode: ModeNone, Budget: 0}
		case ModeAuto:
			return ThinkingConfig{Mode: ModeAuto, Budget: -1}
		}
	}
	if level, ok := ParseLevelSuffix(rawSuffix); ok {
		return ThinkingConfig{Mode: ModeLevel, Level: level}
	}
	if budget, ok := ParseNumericSuffix(rawSuffix); ok {
		if budget == 0 {
			return ThinkingConfig{Mode: ModeNone, Budget: 0}
		}
		return ThinkingConfig{Mode: ModeBudget, Budget: budget}
	}
	return ThinkingConfig{}
}

// ---------------------------------------------------------------------------
// convert
// ---------------------------------------------------------------------------

var levelToBudgetMap = map[string]int{
	"none":    0,
	"auto":    -1,
	"minimal": 512,
	"low":     1024,
	"medium":  8192,
	"high":    24576,
	"xhigh":   32768,
	"max":     128000,
}

const (
	ThresholdMinimal = 512
	ThresholdLow     = 1024
	ThresholdMedium  = 8192
	ThresholdHigh    = 24576
)

// ConvertLevelToBudget level → budget。
func ConvertLevelToBudget(level string) (int, bool) {
	budget, ok := levelToBudgetMap[strings.ToLower(strings.TrimSpace(level))]
	return budget, ok
}

// ConvertBudgetToLevel budget → 最近 level。
func ConvertBudgetToLevel(budget int) (string, bool) {
	switch {
	case budget < -1:
		return "", false
	case budget == -1:
		return string(LevelAuto), true
	case budget == 0:
		return string(LevelNone), true
	case budget <= ThresholdMinimal:
		return string(LevelMinimal), true
	case budget <= ThresholdLow:
		return string(LevelLow), true
	case budget <= ThresholdMedium:
		return string(LevelMedium), true
	case budget <= ThresholdHigh:
		return string(LevelHigh), true
	default:
		return string(LevelXHigh), true
	}
}

// HasLevel 检查 levels 是否包含 target。
func HasLevel(levels []string, target string) bool {
	for _, level := range levels {
		if strings.EqualFold(strings.TrimSpace(level), target) {
			return true
		}
	}
	return false
}

func detectModelCapability(modelInfo *ModelInfo) ModelCapability {
	if modelInfo == nil {
		return CapabilityUnknown
	}
	if modelInfo.Thinking == nil {
		return CapabilityNone
	}
	support := modelInfo.Thinking
	hasBudget := support.Min > 0 || support.Max > 0
	hasLevels := len(support.Levels) > 0
	switch {
	case hasBudget && hasLevels:
		return CapabilityHybrid
	case hasBudget:
		return CapabilityBudgetOnly
	case hasLevels:
		return CapabilityLevelOnly
	default:
		return CapabilityNone
	}
}

// IsUserDefinedModel nil 或显式 UserDefined。
func IsUserDefinedModel(modelInfo *ModelInfo) bool {
	if modelInfo == nil {
		return true
	}
	return modelInfo.UserDefined
}

// ---------------------------------------------------------------------------
// validate
// ---------------------------------------------------------------------------

var standardLevelOrder = []ThinkingLevel{
	LevelMinimal, LevelLow, LevelMedium, LevelHigh, LevelXHigh, LevelMax,
}

// ValidateConfig 按模型能力校验并规范化
func ValidateConfig(config ThinkingConfig, modelInfo *ModelInfo, fromFormat, toFormat string, fromSuffix bool) (*ThinkingConfig, error) {
	fromFormat = strings.ToLower(strings.TrimSpace(fromFormat))
	toFormat = strings.ToLower(strings.TrimSpace(toFormat))
	model := "unknown"
	var support *ThinkingSupport
	if modelInfo != nil {
		if modelInfo.ID != "" {
			model = modelInfo.ID
		}
		support = modelInfo.Thinking
	}

	if support == nil {
		if config.Mode != ModeNone {
			return nil, NewThinkingErrorWithModel(ErrThinkingNotSupported, "thinking not supported for this model", model)
		}
		return &config, nil
	}

	toCapability := detectModelCapability(modelInfo)
	toHasLevelSupport := toCapability == CapabilityLevelOnly || toCapability == CapabilityHybrid
	modelFamilyMismatch := false
	if modelInfo != nil {
		modelType := strings.ToLower(strings.TrimSpace(modelInfo.Type))
		if modelType != "" {
			if (fromFormat != "" && !isSameProviderFamily(fromFormat, modelType)) ||
				(toFormat != "" && !isSameProviderFamily(toFormat, modelType)) {
				modelFamilyMismatch = true
			}
		}
	}
	// 跨 provider family（如 claude/chat → xai）对不支持的 level 做 nearest clamp。
	allowClampUnsupported := toHasLevelSupport && (!isSameProviderFamily(fromFormat, toFormat) || modelFamilyMismatch)
	strictBudget := !fromSuffix && fromFormat != "" && isSameProviderFamily(fromFormat, toFormat) && !modelFamilyMismatch
	budgetDerivedFromLevel := false

	capability := detectModelCapability(modelInfo)
	switch capability {
	case CapabilityBudgetOnly:
		if config.Mode == ModeLevel {
			if config.Level == LevelAuto {
				break
			}
			budget, ok := ConvertLevelToBudget(string(config.Level))
			if !ok {
				return nil, NewThinkingError(ErrUnknownLevel, fmt.Sprintf("unknown level: %s", config.Level))
			}
			config.Mode = ModeBudget
			config.Budget = budget
			config.Level = ""
			budgetDerivedFromLevel = true
		}
	case CapabilityLevelOnly:
		if config.Mode == ModeBudget {
			level, ok := ConvertBudgetToLevel(config.Budget)
			if !ok {
				return nil, NewThinkingError(ErrUnknownLevel, fmt.Sprintf("budget %d cannot be converted to a valid level", config.Budget))
			}
			config.Mode = ModeLevel
			config.Level = clampLevel(ThinkingLevel(level), modelInfo)
			config.Budget = 0
		}
	case CapabilityHybrid:
	}

	if config.Mode == ModeLevel && config.Level == LevelNone {
		config.Mode = ModeNone
		config.Budget = 0
		config.Level = ""
	}
	if config.Mode == ModeLevel && config.Level == LevelAuto {
		config.Mode = ModeAuto
		config.Budget = -1
		config.Level = ""
	}
	if config.Mode == ModeBudget && config.Budget == 0 {
		config.Mode = ModeNone
		config.Level = ""
	}

	if len(support.Levels) > 0 && config.Mode == ModeLevel {
		if !isLevelSupported(string(config.Level), support.Levels) {
			if allowClampUnsupported {
				config.Level = clampLevel(config.Level, modelInfo)
			}
			if !isLevelSupported(string(config.Level), support.Levels) {
				validLevels := normalizeLevels(support.Levels)
				message := fmt.Sprintf("level %q not supported, valid levels: %s", strings.ToLower(string(config.Level)), strings.Join(validLevels, ", "))
				return nil, NewThinkingError(ErrLevelNotSupported, message)
			}
		}
	}

	if strictBudget && config.Mode == ModeBudget && !budgetDerivedFromLevel {
		min, max := support.Min, support.Max
		if min != 0 || max != 0 {
			if config.Budget < min || config.Budget > max || (config.Budget == 0 && !support.ZeroAllowed) {
				message := fmt.Sprintf("budget %d out of range [%d,%d]", config.Budget, min, max)
				return nil, NewThinkingError(ErrBudgetOutOfRange, message)
			}
		}
	}

	if config.Mode == ModeAuto && !support.DynamicAllowed {
		config = convertAutoToMidRange(config, support)
	}

	if config.Mode == ModeNone && toFormat == "claude" {
		config.Budget = 0
		config.Level = ""
	} else {
		switch config.Mode {
		case ModeBudget, ModeAuto, ModeNone:
			config.Budget = clampBudget(config.Budget, modelInfo)
		}
		if config.Mode == ModeNone && config.Budget > 0 && len(support.Levels) > 0 {
			config.Level = ThinkingLevel(support.Levels[0])
		}
	}

	return &config, nil
}

func convertAutoToMidRange(config ThinkingConfig, support *ThinkingSupport) ThinkingConfig {
	if len(support.Levels) > 0 && support.Min == 0 && support.Max == 0 {
		config.Mode = ModeLevel
		config.Level = LevelMedium
		config.Budget = 0
		if !isLevelSupported(string(LevelMedium), support.Levels) {
			config.Level = clampLevel(LevelMedium, &ModelInfo{Thinking: support})
		}
		return config
	}
	mid := (support.Min + support.Max) / 2
	if mid <= 0 && support.ZeroAllowed {
		config.Mode = ModeNone
		config.Budget = 0
	} else if mid <= 0 {
		config.Mode = ModeBudget
		config.Budget = support.Min
	} else {
		config.Mode = ModeBudget
		config.Budget = mid
	}
	return config
}

func clampLevel(level ThinkingLevel, modelInfo *ModelInfo) ThinkingLevel {
	var supported []string
	if modelInfo != nil && modelInfo.Thinking != nil {
		supported = modelInfo.Thinking.Levels
	}
	if len(supported) == 0 || isLevelSupported(string(level), supported) {
		return level
	}
	pos := levelIndex(string(level))
	if pos == -1 {
		return level
	}
	bestIdx, bestDist := -1, len(standardLevelOrder)+1
	for _, s := range supported {
		if idx := levelIndex(strings.TrimSpace(s)); idx != -1 {
			if dist := abs(pos - idx); dist < bestDist || (dist == bestDist && idx < bestIdx) {
				bestIdx, bestDist = idx, dist
			}
		}
	}
	if bestIdx >= 0 {
		return standardLevelOrder[bestIdx]
	}
	return level
}

func clampBudget(value int, modelInfo *ModelInfo) int {
	var support *ThinkingSupport
	if modelInfo != nil {
		support = modelInfo.Thinking
	}
	if support == nil {
		return value
	}
	if value == -1 {
		return value
	}
	min, max := support.Min, support.Max
	if value == 0 && !support.ZeroAllowed {
		return min
	}
	if min == 0 && max == 0 {
		return value
	}
	if value < min {
		if value == 0 && support.ZeroAllowed {
			return 0
		}
		return min
	}
	if value > max {
		return max
	}
	return value
}

func isLevelSupported(level string, supported []string) bool {
	for _, s := range supported {
		if strings.EqualFold(level, strings.TrimSpace(s)) {
			return true
		}
	}
	return false
}

func levelIndex(level string) int {
	for i, l := range standardLevelOrder {
		if strings.EqualFold(level, string(l)) {
			return i
		}
	}
	return -1
}

func normalizeLevels(levels []string) []string {
	out := make([]string, len(levels))
	for i, l := range levels {
		out[i] = strings.ToLower(strings.TrimSpace(l))
	}
	return out
}

func isGeminiFamily(provider string) bool {
	switch provider {
	case "gemini", "antigravity":
		return true
	default:
		return false
	}
}

func isOpenAIFamily(provider string) bool {
	switch provider {
	case "openai", "openai-response", "codex", "chat":
		return true
	default:
		return false
	}
}

func isSameProviderFamily(from, to string) bool {
	if from == to {
		return true
	}
	return (isGeminiFamily(from) && isGeminiFamily(to)) ||
		(isOpenAIFamily(from) && isOpenAIFamily(to))
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ---------------------------------------------------------------------------
// apply
// ---------------------------------------------------------------------------

// LookupModelFunc 由调用方注入模型能力查询（catalog 在 backend）。
type LookupModelFunc func(model string) *ModelInfo

// ApplyThinking 完整 thinking 入口（suffix 优先 → 校验 → 写入 reasoning.effort）。
//
// fromFormat 用于跨家族 clamp：claude / openai / chat / codex / openai-response。
// toFormat 固定为 xai 语义（level-only codex applier）。
// modelInfo 为 nil 时按 user-defined 透传；Thinking 为 nil 时 strip effort。
func ApplyThinking(body []byte, model string, fromFormat string, lookup LookupModelFunc) ([]byte, error) {
	fromFormat = strings.ToLower(strings.TrimSpace(fromFormat))
	if fromFormat == "" {
		fromFormat = "codex"
	}
	const toFormat = "xai"

	suffixResult := ParseSuffix(model)
	baseModel := strings.TrimSpace(suffixResult.ModelName)
	if baseModel == "" {
		baseModel = strings.TrimSpace(model)
	}

	var modelInfo *ModelInfo
	if lookup != nil {
		modelInfo = lookup(baseModel)
	}

	// 未知模型：user-defined，透传配置给上游
	if IsUserDefinedModel(modelInfo) {
		return applyUserDefined(body, fromFormat, suffixResult)
	}

	if modelInfo.Thinking == nil {
		if hasThinkingConfig(extractConfig(body, fromFormat, toFormat)) {
			return StripEffort(body), nil
		}
		return body, nil
	}

	var config ThinkingConfig
	fromSuffix := suffixResult.HasSuffix
	if fromSuffix {
		config = parseSuffixToConfig(suffixResult.RawSuffix)
	} else {
		config = extractConfig(body, fromFormat, toFormat)
	}
	if !hasThinkingConfig(config) {
		return body, nil
	}

	validated, err := ValidateConfig(config, modelInfo, fromFormat, toFormat, fromSuffix)
	if err != nil {
		return body, err
	}
	if validated == nil {
		return body, nil
	}
	return applyCodex(body, *validated, modelInfo)
}

func applyUserDefined(body []byte, fromFormat string, suffixResult SuffixResult) ([]byte, error) {
	var config ThinkingConfig
	if suffixResult.HasSuffix {
		config = parseSuffixToConfig(suffixResult.RawSuffix)
	} else {
		config = extractConfig(body, fromFormat, "xai")
		if !hasThinkingConfig(config) && fromFormat != "xai" && fromFormat != "codex" {
			config = extractCodexConfig(body)
		}
	}
	if !hasThinkingConfig(config) {
		return body, nil
	}
	return applyCompatibleCodex(body, config)
}

func hasThinkingConfig(config ThinkingConfig) bool {
	return config.Mode != ModeBudget || config.Budget != 0 || config.Level != ""
}

// extractConfig 按来源格式提取；xAI wire 上统一读 reasoning.effort。
func extractConfig(body []byte, fromFormat, toFormat string) ThinkingConfig {
	// 进入 Prepare 时 body 已是 Responses wire，优先 codex 字段。
	if cfg := extractCodexConfig(body); hasThinkingConfig(cfg) {
		return cfg
	}
	switch strings.ToLower(strings.TrimSpace(fromFormat)) {
	case "openai", "chat":
		return extractOpenAIConfig(body)
	case "claude":
		return extractClaudeConfig(body)
	default:
		_ = toFormat
		return ThinkingConfig{}
	}
}

// extractCodexConfig 读 reasoning.effort；兼容数字字符串与 special 值。
func extractCodexConfig(body []byte) ThinkingConfig {
	effort := gjson.GetBytes(body, "reasoning.effort")
	if !effort.Exists() {
		return ThinkingConfig{}
	}
	return parseEffortValue(effort.String())
}

func extractOpenAIConfig(body []byte) ThinkingConfig {
	effort := gjson.GetBytes(body, "reasoning_effort")
	if !effort.Exists() {
		return ThinkingConfig{}
	}
	return parseEffortValue(effort.String())
}

func extractClaudeConfig(body []byte) ThinkingConfig {
	thinkingType := gjson.GetBytes(body, "thinking.type").String()
	if thinkingType == "disabled" {
		return ThinkingConfig{Mode: ModeNone, Budget: 0}
	}
	if thinkingType == "adaptive" || thinkingType == "auto" {
		if effort := gjson.GetBytes(body, "output_config.effort"); effort.Exists() && effort.Type == gjson.String {
			value := strings.ToLower(strings.TrimSpace(effort.String()))
			if value == "" {
				return ThinkingConfig{}
			}
			return parseEffortValue(value)
		}
		// adaptive 无 effort
		return ThinkingConfig{}
	}
	if budget := gjson.GetBytes(body, "thinking.budget_tokens"); budget.Exists() {
		value := int(budget.Int())
		switch value {
		case 0:
			return ThinkingConfig{Mode: ModeNone, Budget: 0}
		case -1:
			return ThinkingConfig{Mode: ModeAuto, Budget: -1}
		default:
			return ThinkingConfig{Mode: ModeBudget, Budget: value}
		}
	}
	if thinkingType == "enabled" {
		return ThinkingConfig{Mode: ModeAuto, Budget: -1}
	}
	return ThinkingConfig{}
}

// parseEffortValue 将 effort 字符串规范为 ThinkingConfig。
func parseEffortValue(raw string) ThinkingConfig {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ThinkingConfig{}
	}
	if mode, ok := ParseSpecialSuffix(raw); ok {
		switch mode {
		case ModeNone:
			return ThinkingConfig{Mode: ModeNone, Budget: 0}
		case ModeAuto:
			return ThinkingConfig{Mode: ModeAuto, Budget: -1}
		}
	}
	if level, ok := ParseLevelSuffix(raw); ok {
		return ThinkingConfig{Mode: ModeLevel, Level: level}
	}
	if budget, ok := ParseNumericSuffix(raw); ok {
		if budget == 0 {
			return ThinkingConfig{Mode: ModeNone, Budget: 0}
		}
		return ThinkingConfig{Mode: ModeBudget, Budget: budget}
	}
	// 未知字符串：当作 level，交给 ValidateConfig 拒绝或 clamp
	return ThinkingConfig{Mode: ModeLevel, Level: ThinkingLevel(strings.ToLower(raw))}
}

// applyCodex 写入 reasoning.effort
func applyCodex(body []byte, config ThinkingConfig, modelInfo *ModelInfo) ([]byte, error) {
	if IsUserDefinedModel(modelInfo) {
		return applyCompatibleCodex(body, config)
	}
	if modelInfo == nil || modelInfo.Thinking == nil {
		return body, nil
	}
	if config.Mode != ModeLevel && config.Mode != ModeNone {
		return body, nil
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		body = []byte(`{}`)
	}
	if config.Mode == ModeLevel {
		result, _ := sjson.SetBytes(body, "reasoning.effort", string(config.Level))
		return result, nil
	}

	effort := ""
	support := modelInfo.Thinking
	if config.Budget == 0 {
		if support.ZeroAllowed || HasLevel(support.Levels, string(LevelNone)) {
			effort = string(LevelNone)
		}
	}
	if effort == "" && config.Level != "" {
		effort = string(config.Level)
	}
	if effort == "" && len(support.Levels) > 0 {
		effort = support.Levels[0]
	}
	if effort == "" {
		return body, nil
	}
	result, _ := sjson.SetBytes(body, "reasoning.effort", effort)
	return result, nil
}

func applyCompatibleCodex(body []byte, config ThinkingConfig) ([]byte, error) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		body = []byte(`{}`)
	}
	var effort string
	switch config.Mode {
	case ModeLevel:
		if config.Level == "" {
			return body, nil
		}
		effort = string(config.Level)
	case ModeNone:
		effort = string(LevelNone)
		if config.Level != "" {
			effort = string(config.Level)
		}
	case ModeAuto:
		effort = string(LevelAuto)
	case ModeBudget:
		level, ok := ConvertBudgetToLevel(config.Budget)
		if !ok {
			return body, nil
		}
		effort = level
	default:
		return body, nil
	}
	result, _ := sjson.SetBytes(body, "reasoning.effort", effort)
	return result, nil
}

// StripEffort 删除 reasoning.effort；空 reasoning 对象一并清理，summary 保留。
func StripEffort(body []byte) []byte {
	if !gjson.GetBytes(body, "reasoning.effort").Exists() {
		if reasoning := gjson.GetBytes(body, "reasoning"); reasoning.Exists() && reasoning.IsObject() && len(reasoning.Map()) == 0 {
			body, _ = sjson.DeleteBytes(body, "reasoning")
		}
		return body
	}
	body, _ = sjson.DeleteBytes(body, "reasoning.effort")
	if reasoning := gjson.GetBytes(body, "reasoning"); reasoning.Exists() && reasoning.IsObject() && len(reasoning.Map()) == 0 {
		body, _ = sjson.DeleteBytes(body, "reasoning")
	}
	return body
}

// SourceTypeToFromFormat 将内部 source_type 映射为 thinking fromFormat。
func SourceTypeToFromFormat(sourceType string) string {
	switch strings.ToLower(strings.TrimSpace(sourceType)) {
	case "claude":
		return "claude"
	case "chat":
		return "openai"
	case "codex", "responses", "openai-response", "":
		return "codex"
	default:
		return strings.ToLower(strings.TrimSpace(sourceType))
	}
}
