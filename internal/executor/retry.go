// Package executor 提供重试执行器
package executor

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"clisimplehub/internal/logger"
	"clisimplehub/internal/retry"
)

// RetryConfig 重试配置（复用 internal/retry 的定义，避免重复）
type RetryConfig = retry.Config

// DefaultRetryConfig 默认重试配置
func DefaultRetryConfig() RetryConfig {
	return retry.DefaultConfig()
}

// RetryExecutor 带重试功能的执行器
type RetryExecutor struct {
	execCtx        *ExecutionContext
	config         RetryConfig
	circuitBreaker *retry.CircuitBreaker
}

// NewRetryExecutor 创建重试执行器
func NewRetryExecutor(execCtx *ExecutionContext, config RetryConfig) *RetryExecutor {
	return &RetryExecutor{
		execCtx:        execCtx,
		config:         config,
		circuitBreaker: retry.NewCircuitBreaker(config.CircuitBreakerThreshold),
	}
}

// ExecuteResult 执行结果
type ExecuteResult struct {
	Result        *ForwardResult
	Endpoint      *EndpointConfig
	InterfaceType string
	Attempts      int
	LastError     error
}

// Execute 执行请求（带重试）
// 流程：
// 1. 检测接口类型
// 2. 查找端点
// 3. 选择执行器执行
// 4. 失败时重试或切换端点
func (r *RetryExecutor) Execute(ctx context.Context, req *ForwardRequest, w http.ResponseWriter, enableRetry bool) *ExecuteResult {
	// 1. 检测接口类型
	interfaceType := r.execCtx.DetectInterfaceType(req.Path)

	// 2. 查找初始端点
	endpoint := r.execCtx.GetProvider().GetActiveEndpoint(interfaceType)
	if endpoint == nil {
		return &ExecuteResult{
			Result: &ForwardResult{
				StatusCode: http.StatusServiceUnavailable,
				Error:      &StatusError{Code: http.StatusServiceUnavailable, Message: "No enabled endpoints available"},
			},
			InterfaceType: interfaceType,
		}
	}

	// 不启用重试时，直接执行一次，不更新断路器（避免隐式故障转移）
	if !enableRetry {
		result := r.execCtx.ExecuteWithEndpoint(ctx, endpoint, req, w)
		return &ExecuteResult{
			Result:        result,
			Endpoint:      endpoint,
			InterfaceType: interfaceType,
			Attempts:      1,
		}
	}

	// 3. 重试循环
	return r.executeWithRetry(ctx, req, w, interfaceType, endpoint)
}

func (r *RetryExecutor) executeWithRetry(ctx context.Context, req *ForwardRequest, w http.ResponseWriter, interfaceType string, endpoint *EndpointConfig) *ExecuteResult {
	var lastErr error
	tracker := retry.NewTracker(r.config)
	attempts := 0

	for tracker.CanRetry() {
		if endpoint == nil {
			break
		}

		currentKey := EndpointKey(endpoint)

		// 跳过已耗尽的端点
		if tracker.IsEndpointExhausted(currentKey) {
			nextEndpoint := r.execCtx.FindNextEndpoint(interfaceType, endpoint, tracker.ExhaustedEndpoints())
			if nextEndpoint == nil {
				break
			}
			// 端点耗尽：通知端点切换（故障转移）
			r.execCtx.NotifySwitch(endpoint, nextEndpoint, req.Path, 0, "endpoint exhausted")
			endpoint = nextEndpoint
			currentKey = EndpointKey(endpoint)
		}

		tracker.RecordAttempt(currentKey)
		attempts++

		// 执行请求
		result := r.execCtx.ExecuteWithEndpoint(ctx, endpoint, req, w)

		// 流式响应已写入，无法重试
		if result.Streamed {
			_ = r.updateCircuitBreaker(endpoint, req.Path, result)
			return &ExecuteResult{
				Result:        result,
				Endpoint:      endpoint,
				InterfaceType: interfaceType,
				Attempts:      attempts,
			}
		}

		// 成功
		if result.Error == nil && result.StatusCode == http.StatusOK {
			r.circuitBreaker.RecordSuccess(currentKey)
			return &ExecuteResult{
				Result:        result,
				Endpoint:      endpoint,
				InterfaceType: interfaceType,
				Attempts:      attempts,
			}
		}

		lastErr = result.Error
		disabledUntil := r.updateCircuitBreaker(endpoint, req.Path, result)
		if !disabledUntil.IsZero() {
			// 端点被断路器临时禁用：将其标记为耗尽并静默切换到下一个端点（保持与旧 proxy 行为一致）
			tracker.MarkEndpointExhausted(currentKey)
			endpoint = r.execCtx.FindNextEndpoint(interfaceType, endpoint, tracker.ExhaustedEndpoints())
			continue
		}

		// 检查是否应该重试
		if result.Error == nil && !retry.ShouldRetry(result.StatusCode) {
			// 非重试状态码，直接返回
			return &ExecuteResult{
				Result:        result,
				Endpoint:      endpoint,
				InterfaceType: interfaceType,
				Attempts:      attempts,
				LastError:     lastErr,
			}
		}

		// 检查端点是否已耗尽
		if tracker.ShouldExhaustEndpoint(currentKey) {
			tracker.MarkEndpointExhausted(currentKey)

			// 查找下一个端点
			nextEndpoint := r.execCtx.FindNextEndpoint(interfaceType, endpoint, tracker.ExhaustedEndpoints())
			if nextEndpoint == nil {
				break
			}

			// 通知端点切换
			errMsg := "max retries exceeded"
			if result.Error != nil {
				errMsg = result.Error.Error()
			} else if result.StatusCode > 0 {
				errMsg = fmt.Sprintf("HTTP %d", result.StatusCode)
			}
			r.execCtx.NotifySwitch(endpoint, nextEndpoint, req.Path, result.StatusCode, errMsg)
			endpoint = nextEndpoint
		}
	}

	// 所有重试耗尽
	return &ExecuteResult{
		Result: &ForwardResult{
			StatusCode: http.StatusServiceUnavailable,
			Error:      fmt.Errorf("all endpoints failed: %v", lastErr),
		},
		Endpoint:      endpoint,
		InterfaceType: interfaceType,
		Attempts:      attempts,
		LastError:     lastErr,
	}
}

func (r *RetryExecutor) updateCircuitBreaker(endpoint *EndpointConfig, path string, result *ForwardResult) time.Time {
	if endpoint == nil {
		return time.Time{}
	}

	key := EndpointKey(endpoint)
	if key == "" {
		return time.Time{}
	}

	// 成功时重置
	if result.Error == nil && result.StatusCode == http.StatusOK {
		r.circuitBreaker.RecordSuccess(key)
		return time.Time{}
	}

	// 只对可重试的失败计数
	if !retry.IsRetryableFailure(result.StatusCode, result.Error) {
		return time.Time{}
	}

	failures := r.circuitBreaker.RecordFailure(key)
	if r.circuitBreaker.ShouldTrip(key) {
		r.circuitBreaker.Reset(key)
		interfaceType := endpoint.InterfaceType
		until := r.execCtx.DisableEndpoint(interfaceType, endpoint)

		threshold := r.config.CircuitBreakerThreshold
		if threshold <= 0 {
			threshold = 2
		}

		reason := ""
		if result.Error != nil {
			reason = result.Error.Error()
		} else if result.StatusCode > 0 {
			reason = fmt.Sprintf("HTTP %d", result.StatusCode)
		} else {
			reason = "unknown"
		}

		if !until.IsZero() {
			logger.Warn(
				"[Executor] circuit breaker tripped: interface=%s endpoint=%s key=%s path=%s failures=%d threshold=%d reason=%s disabled_until=%s",
				interfaceType,
				endpoint.Name,
				key,
				path,
				failures,
				threshold,
				reason,
				until.Format(time.RFC3339),
			)
		} else {
			logger.Warn(
				"[Executor] circuit breaker tripped (disable no-op): interface=%s endpoint=%s key=%s path=%s failures=%d threshold=%d reason=%s",
				interfaceType,
				endpoint.Name,
				key,
				path,
				failures,
				threshold,
				reason,
			)
		}

		return until
	}
	return time.Time{}
}
