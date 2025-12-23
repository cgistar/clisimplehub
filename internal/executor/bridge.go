// Package executor 提供执行器与 proxy 的桥接功能
package executor

import (
	"net/http"
)

// EndpointAdapter 端点适配器接口
type EndpointAdapter interface {
	GetID() int64
	GetName() string
	GetAPIURL() string
	GetAPIKey() string
	GetInterfaceType() string
	GetTransformer() string
	GetModel() string
	GetVendorID() int64
	GetProxyURL() string
	GetModels() []ModelMapping
	GetHeaders() map[string]string
}

// EndpointFromAdapter 从适配器创建执行器端点配置
func EndpointFromAdapter(ep EndpointAdapter) *EndpointConfig {
	if ep == nil {
		return nil
	}

	return &EndpointConfig{
		ID:            ep.GetID(),
		Name:          ep.GetName(),
		APIURL:        ep.GetAPIURL(),
		APIKey:        ep.GetAPIKey(),
		InterfaceType: ep.GetInterfaceType(),
		Transformer:   ep.GetTransformer(),
		VendorID:      ep.GetVendorID(),
		Model:         ep.GetModel(),
		ProxyURL:      ep.GetProxyURL(),
		Models:        ep.GetModels(),
		Headers:       ep.GetHeaders(),
	}
}

// ForwardRequestFromHTTP 从 HTTP 请求创建转发请求
func ForwardRequestFromHTTP(r *http.Request, body []byte, isStreaming bool) *ForwardRequest {
	return &ForwardRequest{
		Method:      r.Method,
		Path:        r.URL.Path,
		RawQuery:    r.URL.RawQuery,
		Headers:     r.Header.Clone(),
		Body:        body,
		IsStreaming: isStreaming,
	}
}

// SelectExecutor 根据接口类型选择合适的执行器
func SelectExecutor(interfaceType string) Executor {
	if exec := GetByInterfaceTypeDefault(interfaceType); exec != nil {
		return exec
	}
	// 默认返回 Claude 执行器
	return GetDefault("claude")
}
