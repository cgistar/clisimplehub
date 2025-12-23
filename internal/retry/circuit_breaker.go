package retry

import (
	"context"
	"errors"
	"sync"
	"time"
)

// CircuitBreaker 实现断路器模式
type CircuitBreaker struct {
	mu            sync.RWMutex
	failureCounts map[string]int
	threshold     int
}

// NewCircuitBreaker 创建断路器
func NewCircuitBreaker(threshold int) *CircuitBreaker {
	if threshold <= 0 {
		threshold = 2
	}
	return &CircuitBreaker{
		failureCounts: make(map[string]int),
		threshold:     threshold,
	}
}

// RecordSuccess 记录成功（重置失败计数）
func (cb *CircuitBreaker) RecordSuccess(key string) {
	if key == "" {
		return
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failureCounts[key] = 0
}

// RecordFailure 记录失败，返回当前失败次数
func (cb *CircuitBreaker) RecordFailure(key string) int {
	if key == "" {
		return 0
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failureCounts[key]++
	return cb.failureCounts[key]
}

// ShouldTrip 判断是否应该触发断路器
func (cb *CircuitBreaker) ShouldTrip(key string) bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.failureCounts[key] >= cb.threshold
}

// Reset 重置指定 key 的计数
func (cb *CircuitBreaker) Reset(key string) {
	if key == "" {
		return
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failureCounts[key] = 0
}

// IsIgnorableError 判断是否为可忽略的错误（不应计入断路器）
func IsIgnorableError(err error) bool {
	if err == nil {
		return false
	}
	// 客户端取消或超时：不视为端点失败
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// IsRetryableFailure 判断是否为应触发断路器的失败
func IsRetryableFailure(statusCode int, err error) bool {
	if err != nil && !IsIgnorableError(err) {
		return true
	}
	if err == nil && statusCode >= 500 && statusCode <= 599 {
		return true
	}
	return false
}

// TempDisableEntry 临时禁用条目
type TempDisableEntry struct {
	Until           time.Time
	PreviousEnabled bool
}
