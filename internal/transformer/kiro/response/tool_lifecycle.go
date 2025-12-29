package response

import (
	"encoding/json"
	"strings"
	"time"
)

// ToolExecutionStatus 工具执行状态枚举
type ToolExecutionStatus int

const (
	ToolStatusPending ToolExecutionStatus = iota
	ToolStatusRunning
	ToolStatusCompleted
	ToolStatusError
)

// String 返回状态的字符串表示
func (s ToolExecutionStatus) String() string {
	switch s {
	case ToolStatusPending:
		return "pending"
	case ToolStatusRunning:
		return "running"
	case ToolStatusCompleted:
		return "completed"
	case ToolStatusError:
		return "error"
	default:
		return "unknown"
	}
}

// ToolExecution 工具执行状态
type ToolExecution struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	StartTime  time.Time           `json:"start_time"`
	EndTime    *time.Time          `json:"end_time,omitempty"`
	Status     ToolExecutionStatus `json:"status"`
	Arguments  map[string]any      `json:"arguments"`
	ArgsRaw    string              `json:"args_raw,omitempty"` // 原始 JSON 参数字符串
	Result     any                 `json:"result,omitempty"`
	Error      string              `json:"error,omitempty"`
	BlockIndex int                 `json:"block_index"`
}

// ToolLifecycleManager 工具调用生命周期管理器
// 迁移自第三方 kiro2api/parser，用于追踪工具调用状态
type ToolLifecycleManager struct {
	activeTools    map[string]*ToolExecution
	completedTools map[string]*ToolExecution
	blockIndexMap  map[string]int
	nextBlockIndex int
}

// NewToolLifecycleManager 创建工具生命周期管理器
func NewToolLifecycleManager() *ToolLifecycleManager {
	return &ToolLifecycleManager{
		activeTools:    make(map[string]*ToolExecution),
		completedTools: make(map[string]*ToolExecution),
		blockIndexMap:  make(map[string]int),
		nextBlockIndex: 1, // 索引 0 预留给文本内容
	}
}

// Reset 重置管理器状态
func (tlm *ToolLifecycleManager) Reset() {
	tlm.activeTools = make(map[string]*ToolExecution)
	tlm.completedTools = make(map[string]*ToolExecution)
	tlm.blockIndexMap = make(map[string]int)
	tlm.nextBlockIndex = 1
}

// StartToolCall 开始工具调用追踪
func (tlm *ToolLifecycleManager) StartToolCall(toolID, toolName string) *ToolExecution {
	toolID = strings.TrimSpace(toolID)
	if toolID == "" {
		return nil
	}

	// 检查工具是否已存在
	if existing, exists := tlm.activeTools[toolID]; exists {
		return existing
	}

	execution := &ToolExecution{
		ID:         toolID,
		Name:       toolName,
		StartTime:  time.Now(),
		Status:     ToolStatusPending,
		Arguments:  make(map[string]any),
		BlockIndex: tlm.getOrAssignBlockIndex(toolID),
	}

	tlm.activeTools[toolID] = execution
	execution.Status = ToolStatusRunning
	return execution
}

// AppendToolInput 追加工具输入参数
func (tlm *ToolLifecycleManager) AppendToolInput(toolID, partialJSON string) {
	toolID = strings.TrimSpace(toolID)
	if toolID == "" {
		return
	}

	execution, exists := tlm.activeTools[toolID]
	if !exists {
		return
	}

	execution.ArgsRaw += partialJSON
}

// FinalizeToolCall 完成工具调用
// 返回 true 如果成功完成
func (tlm *ToolLifecycleManager) FinalizeToolCall(toolID string) bool {
	toolID = strings.TrimSpace(toolID)
	if toolID == "" {
		return false
	}

	execution, exists := tlm.activeTools[toolID]
	if !exists {
		return false
	}

	now := time.Now()
	execution.EndTime = &now
	execution.Status = ToolStatusCompleted

	// 解析累积的参数
	if execution.ArgsRaw != "" {
		var args map[string]any
		if err := json.Unmarshal([]byte(execution.ArgsRaw), &args); err == nil {
			execution.Arguments = args
		}
	}

	// 移动到已完成列表
	tlm.completedTools[toolID] = execution
	delete(tlm.activeTools, toolID)

	return true
}

// MarkToolError 标记工具调用错误
func (tlm *ToolLifecycleManager) MarkToolError(toolID, errorMsg string) {
	toolID = strings.TrimSpace(toolID)
	if toolID == "" {
		return
	}

	execution, exists := tlm.activeTools[toolID]
	if !exists {
		return
	}

	now := time.Now()
	execution.EndTime = &now
	execution.Error = errorMsg
	execution.Status = ToolStatusError

	// 移动到已完成列表
	tlm.completedTools[toolID] = execution
	delete(tlm.activeTools, toolID)
}

// GetToolExecution 获取工具执行信息
func (tlm *ToolLifecycleManager) GetToolExecution(toolID string) *ToolExecution {
	if tool, exists := tlm.activeTools[toolID]; exists {
		return tool
	}
	if tool, exists := tlm.completedTools[toolID]; exists {
		return tool
	}
	return nil
}

// GetActiveTools 获取所有活跃的工具
func (tlm *ToolLifecycleManager) GetActiveTools() map[string]*ToolExecution {
	result := make(map[string]*ToolExecution)
	for id, tool := range tlm.activeTools {
		result[id] = tool
	}
	return result
}

// GetCompletedTools 获取所有已完成的工具
func (tlm *ToolLifecycleManager) GetCompletedTools() map[string]*ToolExecution {
	result := make(map[string]*ToolExecution)
	for id, tool := range tlm.completedTools {
		result[id] = tool
	}
	return result
}

// GetBlockIndex 获取工具的块索引
func (tlm *ToolLifecycleManager) GetBlockIndex(toolID string) int {
	if index, exists := tlm.blockIndexMap[toolID]; exists {
		return index
	}
	return -1
}

// GetNextBlockIndex 获取下一个块索引
func (tlm *ToolLifecycleManager) GetNextBlockIndex() int {
	return tlm.nextBlockIndex
}

// HasActiveTools 检查是否有活跃工具
func (tlm *ToolLifecycleManager) HasActiveTools() bool {
	return len(tlm.activeTools) > 0
}

// HasCompletedTools 检查是否有已完成工具
func (tlm *ToolLifecycleManager) HasCompletedTools() bool {
	return len(tlm.completedTools) > 0
}

// GetActiveToolIDs 获取所有活跃工具 ID
func (tlm *ToolLifecycleManager) GetActiveToolIDs() []string {
	ids := make([]string, 0, len(tlm.activeTools))
	for id := range tlm.activeTools {
		ids = append(ids, id)
	}
	return ids
}

// GetCompletedToolIDs 获取所有已完成工具 ID
func (tlm *ToolLifecycleManager) GetCompletedToolIDs() []string {
	ids := make([]string, 0, len(tlm.completedTools))
	for id := range tlm.completedTools {
		ids = append(ids, id)
	}
	return ids
}

// getOrAssignBlockIndex 获取或分配块索引
func (tlm *ToolLifecycleManager) getOrAssignBlockIndex(toolID string) int {
	if index, exists := tlm.blockIndexMap[toolID]; exists {
		return index
	}

	index := tlm.nextBlockIndex
	tlm.blockIndexMap[toolID] = index
	tlm.nextBlockIndex++
	return index
}

// GenerateToolSummary 生成工具执行摘要
func (tlm *ToolLifecycleManager) GenerateToolSummary() map[string]any {
	activeCount := len(tlm.activeTools)
	completedCount := len(tlm.completedTools)
	errorCount := 0
	totalExecutionTime := int64(0)

	for _, tool := range tlm.completedTools {
		if tool.Status == ToolStatusError {
			errorCount++
		}
		if tool.EndTime != nil {
			totalExecutionTime += tool.EndTime.Sub(tool.StartTime).Milliseconds()
		}
	}

	successRate := 0.0
	totalCount := completedCount + activeCount
	if totalCount > 0 {
		successRate = float64(completedCount-errorCount) / float64(totalCount)
	}

	return map[string]any{
		"active_tools":         activeCount,
		"completed_tools":      completedCount,
		"error_tools":          errorCount,
		"total_execution_time": totalExecutionTime,
		"success_rate":         successRate,
	}
}
