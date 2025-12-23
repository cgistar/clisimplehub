// Package executor 提供执行上下文
package executor

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// ExecutionContext 执行上下文
// 封装执行器和端点提供者，简化请求处理
type ExecutionContext struct {
	provider EndpointProvider
	observer ExecutionObserver
}

// ExecutionObserver 执行观察者接口
// 用于通知请求处理状态（如 WebSocket 推送）
type ExecutionObserver interface {
	// OnRequestStart 请求开始
	OnRequestStart(requestID string, interfaceType string, endpoint *EndpointConfig, path string)
	// OnRequestComplete 请求完成
	OnRequestComplete(requestID string, interfaceType string, endpoint *EndpointConfig, result *ForwardResult, duration time.Duration)
	// OnEndpointSwitch 端点切换（故障转移）
	OnEndpointSwitch(from, to *EndpointConfig, path string, statusCode int, errorMsg string)
	// OnEndpointDisabled 端点被临时禁用
	OnEndpointDisabled(interfaceType string, endpoint *EndpointConfig, until time.Time)
}

// NewExecutionContext 创建执行上下文
func NewExecutionContext(provider EndpointProvider) *ExecutionContext {
	return &ExecutionContext{provider: provider}
}

// SetObserver 设置执行观察者
func (c *ExecutionContext) SetObserver(observer ExecutionObserver) {
	c.observer = observer
}

// GetProvider 获取端点提供者
func (c *ExecutionContext) GetProvider() EndpointProvider {
	return c.provider
}

// DetectInterfaceType 检测接口类型
func (c *ExecutionContext) DetectInterfaceType(path string) string {
	if c.provider == nil {
		return ""
	}
	return c.provider.DetectInterfaceType(path)
}

// ResolveEndpoint 根据路径解析端点
func (c *ExecutionContext) ResolveEndpoint(path string) (*EndpointConfig, string) {
	if c.provider == nil {
		return nil, ""
	}
	interfaceType := c.provider.DetectInterfaceType(path)
	endpoint := c.provider.GetActiveEndpoint(interfaceType)
	return endpoint, interfaceType
}

// GetExecutor 根据接口类型获取执行器
func (c *ExecutionContext) GetExecutor(interfaceType string) Executor {
	return SelectExecutor(interfaceType)
}

// Execute 执行单次请求
// 流程：检测接口类型 → 选择执行器 → 查找端点 → 执行转发
func (c *ExecutionContext) Execute(ctx context.Context, req *ForwardRequest, w http.ResponseWriter) (*ForwardResult, *EndpointConfig, string) {
	// 1. 检测接口类型
	interfaceType := c.DetectInterfaceType(req.Path)

	// 2. 查找端点
	endpoint := c.provider.GetActiveEndpoint(interfaceType)
	if endpoint == nil {
		return &ForwardResult{
			Error:      &StatusError{Code: http.StatusServiceUnavailable, Message: "No enabled endpoints available"},
			StatusCode: http.StatusServiceUnavailable,
		}, nil, interfaceType
	}

	// 3. 选择执行器并执行
	var result *ForwardResult
	if endpoint != nil && strings.TrimSpace(endpoint.Transformer) != "" {
		result = c.executeWithTransformer(ctx, interfaceType, endpoint, req, w)
	} else {
		exec := c.GetExecutor(interfaceType)
		result = exec.Forward(ctx, endpoint, req, w)
	}

	return result, endpoint, interfaceType
}

// ExecuteWithEndpoint 使用指定端点执行请求
func (c *ExecutionContext) ExecuteWithEndpoint(ctx context.Context, endpoint *EndpointConfig, req *ForwardRequest, w http.ResponseWriter) *ForwardResult {
	interfaceType := c.DetectInterfaceType(req.Path)
	if endpoint != nil && strings.TrimSpace(endpoint.Transformer) != "" {
		return c.executeWithTransformer(ctx, interfaceType, endpoint, req, w)
	}
	exec := c.GetExecutor(interfaceType)
	return exec.Forward(ctx, endpoint, req, w)
}

// FindNextEndpoint 查找下一个可用端点
func (c *ExecutionContext) FindNextEndpoint(interfaceType string, current *EndpointConfig, exhausted map[string]bool) *EndpointConfig {
	if c.provider == nil {
		return nil
	}
	return c.provider.FindNextUntried(interfaceType, current, exhausted)
}

// DisableEndpoint 临时禁用端点
func (c *ExecutionContext) DisableEndpoint(interfaceType string, endpoint *EndpointConfig) time.Time {
	if c.provider == nil {
		return time.Time{}
	}
	until := c.provider.DisableEndpoint(interfaceType, endpoint)
	if c.observer != nil && !until.IsZero() {
		c.observer.OnEndpointDisabled(interfaceType, endpoint, until)
	}
	return until
}

// NotifySwitch 通知端点切换
func (c *ExecutionContext) NotifySwitch(from, to *EndpointConfig, path string, statusCode int, errorMsg string) {
	if c.observer != nil {
		c.observer.OnEndpointSwitch(from, to, path, statusCode, errorMsg)
	}
}

// NotifyStart 通知请求开始
func (c *ExecutionContext) NotifyStart(requestID, interfaceType string, endpoint *EndpointConfig, path string) {
	if c.observer != nil {
		c.observer.OnRequestStart(requestID, interfaceType, endpoint, path)
	}
}

// NotifyComplete 通知请求完成
func (c *ExecutionContext) NotifyComplete(requestID, interfaceType string, endpoint *EndpointConfig, result *ForwardResult, duration time.Duration) {
	if c.observer != nil {
		c.observer.OnRequestComplete(requestID, interfaceType, endpoint, result, duration)
	}
}
