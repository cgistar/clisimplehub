package response

// StopReasonManager 管理 stop_reason 的判断逻辑
// 根据 Claude API 规范，stop_reason 可以是：
//   - "end_turn": 模型正常完成回复
//   - "tool_use": 模型需要调用工具
//   - "max_tokens": 达到最大 token 限制
//   - "stop_sequence": 遇到停止序列
type StopReasonManager struct {
	hasActiveTools    bool   // 是否有活跃的工具调用
	hasCompletedTools bool   // 是否有已完成的工具调用
	hasStopSequence   bool   // 是否遇到停止序列
	maxTokensReached  bool   // 是否达到 token 限制
	upstreamReason    string // 上游返回的原始 stop_reason
	toolChoiceAuto    bool   // tool_choice 是否为 auto
}

// NewStopReasonManager 创建 stop_reason 管理器
func NewStopReasonManager() *StopReasonManager {
	return &StopReasonManager{
		toolChoiceAuto: true, // 默认为 auto
	}
}

// NewStopReasonManagerWithToolChoice 创建带 tool_choice 设置的管理器
func NewStopReasonManagerWithToolChoice(toolChoice string) *StopReasonManager {
	return &StopReasonManager{
		toolChoiceAuto: toolChoice == "" || toolChoice == "auto",
	}
}

// UpdateToolCallStatus 更新工具调用状态
func (srm *StopReasonManager) UpdateToolCallStatus(hasActiveTools, hasCompletedTools bool) {
	srm.hasActiveTools = hasActiveTools
	srm.hasCompletedTools = hasCompletedTools
}

// SetMaxTokensReached 设置是否达到 token 限制
func (srm *StopReasonManager) SetMaxTokensReached(reached bool) {
	srm.maxTokensReached = reached
}

// SetStopSequenceHit 设置是否遇到停止序列
func (srm *StopReasonManager) SetStopSequenceHit(hit bool) {
	srm.hasStopSequence = hit
}

// SetUpstreamReason 设置上游返回的原始 stop_reason
func (srm *StopReasonManager) SetUpstreamReason(reason string) {
	srm.upstreamReason = reason
}

// DetermineStopReason 根据当前状态确定最终的 stop_reason
// 优先级：max_tokens > stop_sequence > tool_use > end_turn
func (srm *StopReasonManager) DetermineStopReason() string {
	// 1. 如果上游明确返回了 stop_reason，优先使用
	if srm.upstreamReason != "" {
		switch srm.upstreamReason {
		case "max_tokens", "stop_sequence":
			return srm.upstreamReason
		case "tool_use":
			// 确认确实有工具调用
			if srm.hasActiveTools || srm.hasCompletedTools {
				return "tool_use"
			}
		case "end_turn":
			// 即使上游说 end_turn，如果有工具调用也应该返回 tool_use
			if srm.hasActiveTools || srm.hasCompletedTools {
				return "tool_use"
			}
			return "end_turn"
		}
	}

	// 2. 达到 token 限制
	if srm.maxTokensReached {
		return "max_tokens"
	}

	// 3. 遇到停止序列
	if srm.hasStopSequence {
		return "stop_sequence"
	}

	// 4. 有工具调用（活跃或已完成）
	if srm.hasActiveTools || srm.hasCompletedTools {
		return "tool_use"
	}

	// 5. 默认正常结束
	return "end_turn"
}

// HasToolCalls 检查是否有工具调用
func (srm *StopReasonManager) HasToolCalls() bool {
	return srm.hasActiveTools || srm.hasCompletedTools
}

// GetStopReasonDescription 获取 stop_reason 的描述（用于日志）
func GetStopReasonDescription(stopReason string) string {
	switch stopReason {
	case "end_turn":
		return "模型正常完成回复"
	case "tool_use":
		return "模型需要调用工具"
	case "max_tokens":
		return "达到最大 token 限制"
	case "stop_sequence":
		return "遇到停止序列"
	default:
		return "未知原因"
	}
}
