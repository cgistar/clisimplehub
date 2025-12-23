// Package executor 提供端点查找接口
package executor

import "time"

// EndpointProvider 端点提供者接口
// 用于查找和管理可用端点
type EndpointProvider interface {
	// DetectInterfaceType 根据请求路径检测接口类型
	DetectInterfaceType(path string) string

	// GetActiveEndpoint 获取指定接口类型的当前活动端点
	GetActiveEndpoint(interfaceType string) *EndpointConfig

	// GetEndpointsByType 获取指定接口类型的所有端点
	GetEndpointsByType(interfaceType string) []*EndpointConfig

	// GetNextEndpoint 获取下一个可用端点（用于故障转移）
	GetNextEndpoint(interfaceType string, current *EndpointConfig) *EndpointConfig

	// FindNextUntried 查找下一个未尝试的端点
	FindNextUntried(interfaceType string, current *EndpointConfig, exhausted map[string]bool) *EndpointConfig

	// DisableEndpoint 临时禁用端点
	DisableEndpoint(interfaceType string, endpoint *EndpointConfig) time.Time

	// SetActiveEndpoint 设置活动端点
	SetActiveEndpoint(interfaceType string, endpoint *EndpointConfig) error
}

// EndpointKey 生成端点的唯一标识
func EndpointKey(ep *EndpointConfig) string {
	if ep == nil {
		return ""
	}
	if ep.ID != 0 {
		return "id:" + formatInt64(ep.ID)
	}
	if ep.Name == "" {
		return ""
	}
	return "name:" + ep.Name
}

func formatInt64(n int64) string {
	if n == 0 {
		return "0"
	}

	negative := n < 0
	if negative {
		n = -n
	}

	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte(n%10) + '0'
		n /= 10
	}

	if negative {
		i--
		buf[i] = '-'
	}

	return string(buf[i:])
}

// FindNextUntriedEndpoint 查找下一个未尝试的端点
// 通用实现，可被 EndpointProvider 使用
func FindNextUntriedEndpoint(endpoints []*EndpointConfig, current *EndpointConfig, exhausted map[string]bool) *EndpointConfig {
	if len(endpoints) == 0 {
		return nil
	}

	// 查找当前端点位置
	currentIdx := -1
	for i, ep := range endpoints {
		if current == nil || ep == nil {
			continue
		}
		if current.ID != 0 {
			if ep.ID == current.ID {
				currentIdx = i
				break
			}
			continue
		}
		if ep.Name == current.Name {
			currentIdx = i
			break
		}
	}

	// 先从当前位置之后查找
	for i := currentIdx + 1; i < len(endpoints); i++ {
		ep := endpoints[i]
		if ep == nil || !isEndpointEnabled(ep) {
			continue
		}
		if exhausted[EndpointKey(ep)] {
			continue
		}
		return ep
	}

	// 再从开头查找
	for i := 0; i < currentIdx; i++ {
		ep := endpoints[i]
		if ep == nil || !isEndpointEnabled(ep) {
			continue
		}
		if exhausted[EndpointKey(ep)] {
			continue
		}
		return ep
	}

	return nil
}

func isEndpointEnabled(ep *EndpointConfig) bool {
	// EndpointConfig 没有 Enabled 字段，默认认为是启用的
	// 实际的启用状态由 EndpointProvider 实现管理
	return ep != nil
}
