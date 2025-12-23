// Package retry 提供重试和断路器功能
package retry

import "time"

// Config 定义重试配置
type Config struct {
	MaxRetriesPerEndpoint   int           // 单个端点最大重试次数
	MaxTotalRetries         int           // 总最大重试次数
	CircuitBreakerThreshold int           // 断路器触发阈值（连续失败次数）
	TempDisableDuration     time.Duration // 端点临时禁用时长
}

// DefaultConfig 返回默认重试配置
func DefaultConfig() Config {
	return Config{
		MaxRetriesPerEndpoint:   2,
		MaxTotalRetries:         10,
		CircuitBreakerThreshold: 2,
		TempDisableDuration:     5 * time.Minute,
	}
}

// ShouldRetry 判断是否应该重试（基于状态码）
func ShouldRetry(statusCode int) bool {
	// 5xx 服务器错误重试
	return statusCode >= 500 && statusCode <= 599
}
