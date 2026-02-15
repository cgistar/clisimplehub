package client

// ConfigGetter 定义配置读取接口
type ConfigGetter interface {
	// GetProxyURL 返回代理 URL 配置
	GetProxyURL() string
}

// StaticConfigGetter 静态配置读取器，直接返回预设的代理 URL
type StaticConfigGetter struct {
	ProxyURL string
}

// GetProxyURL 实现 ConfigGetter 接口
func (s *StaticConfigGetter) GetProxyURL() string {
	return s.ProxyURL
}
