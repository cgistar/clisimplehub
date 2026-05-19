// Package executor 提供 AI API 请求执行器框架
package executor

import (
	"context"
	"io"
	"net/http"

	"clisimplehub/internal/storage"
	"clisimplehub/internal/transformer"
)

// ForwardRequest 表示转发请求的输入
type ForwardRequest struct {
	Method       string
	Path         string
	RawQuery     string
	Headers      http.Header
	Body         []byte
	IsStreaming  bool
	RequestModel string
}

// ForwardResult 表示转发请求的结果
type ForwardResult struct {
	StatusCode    int
	Headers       http.Header
	Body          []byte
	TargetURL     string
	TargetHeaders map[string]string
	// UpstreamRequestBody records the effective request body sent to upstream when enabled via context.
	UpstreamRequestBody string
	// UpstreamResponseBody records the response body from upstream when enabled via context (base64 encoded if binary).
	UpstreamResponseBody string
	ResponseStream       string
	Tokens               *TokenUsage
	Streamed             bool
	Error                error
}

type StreamInputMode string

const (
	StreamInputModeLine  StreamInputMode = "line"
	StreamInputModeChunk StreamInputMode = "chunk"
)

type TransformContext struct {
	OriginalRequestBody    []byte
	TransformedRequestBody []byte
	StreamState            any
	Metadata               map[string]any
}

type TransformationPlan struct {
	Transformer         transformer.Transformer
	TargetInterfaceType string
	TargetPath          string
	RawQuery            string
	RequestBody         []byte
	IsStreaming         bool
	OutputContentType   string
	StreamInputMode     StreamInputMode
	Context             *TransformContext
}

type UpstreamRequest struct {
	Method              string
	TargetPath          string
	RawQuery            string
	Headers             http.Header
	Body                []byte
	IsStreaming         bool
	RequestModel        string
	OriginalPath        string
	TargetInterfaceType string
	Endpoint            *EndpointConfig
	Transformer         transformer.Transformer
	TransformContext    *TransformContext
}

type UpstreamRoundTripResult struct {
	StatusCode    int
	Headers       http.Header
	Body          []byte
	Stream        io.ReadCloser
	TargetURL     string
	TargetHeaders map[string]string
	Tokens        *TokenUsage
	Error         error
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
	TotalTokens  int64 `json:"total_tokens,omitempty"`
	CachedCreate int64 `json:"cached_create,omitempty"`
	CachedRead   int64 `json:"cached_read,omitempty"`
	Reasoning    int64 `json:"reasoning,omitempty"`
}

// EndpointConfig is an alias for storage.Endpoint
// executor 使用完整的端点配置（包括 Active, Enabled, Priority 等字段）
type EndpointConfig = storage.Endpoint

// ModelMapping is an alias for storage.ModelMapping
type ModelMapping = storage.ModelMapping

// UpstreamRoundTripper handles upstream transport for a specific transformer spec.
// Plugins implement this to own auth, HTTP, retry, and failover without writing the downstream response.
type UpstreamRoundTripper interface {
	RoundTrip(ctx context.Context, req *UpstreamRequest) *UpstreamRoundTripResult
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
