package retry

// Tracker 跟踪重试状态
type Tracker struct {
	config             Config
	triedEndpoints     map[string]int  // 每个端点的尝试次数
	exhaustedEndpoints map[string]bool // 已耗尽的端点
	totalRetries       int
}

// NewTracker 创建重试跟踪器
func NewTracker(config Config) *Tracker {
	return &Tracker{
		config:             config,
		triedEndpoints:     make(map[string]int),
		exhaustedEndpoints: make(map[string]bool),
	}
}

// RecordAttempt 记录一次尝试
func (t *Tracker) RecordAttempt(endpointKey string) {
	t.triedEndpoints[endpointKey]++
	t.totalRetries++
}

// IsEndpointExhausted 检查端点是否已耗尽重试次数
func (t *Tracker) IsEndpointExhausted(endpointKey string) bool {
	return t.exhaustedEndpoints[endpointKey]
}

// MarkEndpointExhausted 标记端点已耗尽
func (t *Tracker) MarkEndpointExhausted(endpointKey string) {
	t.exhaustedEndpoints[endpointKey] = true
}

// ShouldExhaustEndpoint 检查是否应该耗尽当前端点
func (t *Tracker) ShouldExhaustEndpoint(endpointKey string) bool {
	return t.triedEndpoints[endpointKey] >= t.config.MaxRetriesPerEndpoint
}

// CanRetry 检查是否还能重试
func (t *Tracker) CanRetry() bool {
	return t.totalRetries < t.config.MaxTotalRetries
}

// GetAttemptCount 获取指定端点的尝试次数
func (t *Tracker) GetAttemptCount(endpointKey string) int {
	return t.triedEndpoints[endpointKey]
}

// ExhaustedEndpoints 返回已耗尽端点集合（用于查找下一个可用端点）。
// 注意：返回的是内部 map 引用，调用方应视为只读。
func (t *Tracker) ExhaustedEndpoints() map[string]bool {
	if t == nil {
		return nil
	}
	return t.exhaustedEndpoints
}
