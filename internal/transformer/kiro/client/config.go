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

// StorageConfigGetter 从 storage 读取配置
type StorageConfigGetter struct {
	getConfig func(key string) (string, error)
}

// NewStorageConfigGetter 创建基于 storage 的配置读取器
func NewStorageConfigGetter(getConfig func(key string) (string, error)) *StorageConfigGetter {
	return &StorageConfigGetter{
		getConfig: getConfig,
	}
}

// GetProxyURL 从 storage 读取 kiro.proxyUrl 配置
func (s *StorageConfigGetter) GetProxyURL() string {
	if s.getConfig == nil {
		return ""
	}
	proxyURL, err := s.getConfig("kiro.proxyUrl")
	if err != nil {
		return ""
	}
	return proxyURL
}
