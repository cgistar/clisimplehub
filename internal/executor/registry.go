package executor

import (
	"strings"
	"sync"
)

// Registry 执行器注册表
type Registry struct {
	mu        sync.RWMutex
	executors map[string]Executor
}

// NewRegistry 创建执行器注册表
func NewRegistry() *Registry {
	return &Registry{
		executors: make(map[string]Executor),
	}
}

// Register 注册执行器
func (r *Registry) Register(executor Executor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.executors[executor.Identifier()] = executor
}

// Get 获取执行器
func (r *Registry) Get(identifier string) Executor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.executors[identifier]
}

// GetByInterfaceType 根据接口类型获取执行器
func (r *Registry) GetByInterfaceType(interfaceType string) Executor {
	r.mu.RLock()
	defer r.mu.RUnlock()

	normalized := strings.ToLower(strings.TrimSpace(interfaceType))
	if exec, ok := r.executors[normalized]; ok {
		return exec
	}
	return nil
}

// 全局默认注册表
var defaultRegistry = NewRegistry()

// Default 返回默认注册表
func Default() *Registry {
	return defaultRegistry
}

// RegisterDefault 在默认注册表中注册执行器
func RegisterDefault(executor Executor) {
	defaultRegistry.Register(executor)
}

// GetDefault 从默认注册表获取执行器
func GetDefault(identifier string) Executor {
	return defaultRegistry.Get(identifier)
}

// GetByInterfaceTypeDefault 从默认注册表根据接口类型获取执行器
func GetByInterfaceTypeDefault(interfaceType string) Executor {
	return defaultRegistry.GetByInterfaceType(interfaceType)
}

// init 初始化默认执行器
func init() {
	RegisterDefault(NewBaseExecutor("claude"))
	RegisterDefault(NewBaseExecutor("codex"))
	RegisterDefault(NewBaseExecutor("gemini"))
	RegisterDefault(NewBaseExecutor("chat"))
}
