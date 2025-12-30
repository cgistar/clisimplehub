package proxy

import (
	"strings"

	"clisimplehub/internal/logger"
)

func (p *ProxyServer) isDebugModeAll() bool {
	p.mu.RLock()
	store := p.store
	p.mu.RUnlock()

	if store == nil {
		return false
	}
	v, err := store.GetConfig("debugMode")
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(v), "db")
}

func (p *ProxyServer) isDebugModeFile() bool {
	p.mu.RLock()
	store := p.store
	p.mu.RUnlock()

	if store == nil {
		return false
	}
	v, err := store.GetConfig("debugMode")
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(v), "file")
}

func (p *ProxyServer) getDebugMode() string {
	p.mu.RLock()
	store := p.store
	p.mu.RUnlock()

	if store == nil {
		return ""
	}
	v, err := store.GetConfig("debugMode")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// InitDebugFileLogger 初始化文件调试日志（在服务启动时调用）
func (p *ProxyServer) InitDebugFileLogger() {
	debugMode := p.getDebugMode()
	logger.InitDebugFileLogger(debugMode)
}

// UpdateDebugFileLogger 更新文件调试日志配置（在配置变更时调用）
func (p *ProxyServer) UpdateDebugFileLogger() {
	debugMode := p.getDebugMode()
	logger.UpdateDebugMode(debugMode)
}
