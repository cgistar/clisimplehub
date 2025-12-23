// Package executor 提供 AI API 请求执行器框架
package executor

import (
	"context"
	"net/http"
)

// ForwardRequest 表示转发请求的输入
type ForwardRequest struct {
	Method      string
	Path        string
	RawQuery    string
	Headers     http.Header
	Body        []byte
	IsStreaming bool
}

// ForwardResult 表示转发请求的结果
type ForwardResult struct {
	StatusCode     int
	Headers        http.Header
	Body           []byte
	TargetURL      string
	TargetHeaders  map[string]string
	ResponseStream string
	Tokens         *TokenUsage
	Streamed       bool
	Error          error
}

// StreamWriter 用于写入流式响应
type StreamWriter interface {
	http.ResponseWriter
	http.Flusher
}

// TokenUsage 表示 token 使用统计
type TokenUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	CachedCreate int64 `json:"cached_create,omitempty"`
	CachedRead   int64 `json:"cached_read,omitempty"`
	Reasoning    int64 `json:"reasoning,omitempty"`
}

// EndpointConfig 端点配置
type EndpointConfig struct {
	ID            int64             `json:"id"`
	Name          string            `json:"name"`
	APIURL        string            `json:"api_url"`
	APIKey        string            `json:"api_key"`
	InterfaceType string            `json:"interface_type"`
	Transformer   string            `json:"transformer,omitempty"`
	VendorID      int64             `json:"vendor_id,omitempty"`
	Model         string            `json:"model,omitempty"`
	ProxyURL      string            `json:"proxy_url,omitempty"`
	Models        []ModelMapping    `json:"models,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
}

// ModelMapping 模型映射配置
type ModelMapping struct {
	Name  string `json:"name"`  // 实际模型名（上游模型名）
	Alias string `json:"alias"` // API 使用的别名（客户端传入的 model）
}

// Executor 定义执行器接口
type Executor interface {
	// Identifier 返回执行器标识符
	Identifier() string

	// Forward 转发请求到上游，处理模型映射、代理、转换等
	Forward(ctx context.Context, endpoint *EndpointConfig, req *ForwardRequest, w http.ResponseWriter) *ForwardResult

	// ExtractTokens 从响应体提取 token 使用量
	ExtractTokens(body []byte) *TokenUsage
}

// StatusError 表示带状态码的错误
type StatusError struct {
	Code    int
	Message string
}

func (e StatusError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return http.StatusText(e.Code)
}

func (e StatusError) StatusCode() int {
	return e.Code
}
