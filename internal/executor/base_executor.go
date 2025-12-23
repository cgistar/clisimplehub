package executor

// BaseExecutor 提供执行器的通用实现
type BaseExecutor struct {
	id          string
	authApplier AuthApplier
}

// NewBaseExecutor 创建基础执行器
func NewBaseExecutor(id string) *BaseExecutor {
	return &BaseExecutor{id: id}
}

func (e *BaseExecutor) Identifier() string {
	return e.id
}

func (e *BaseExecutor) getAuthApplier() AuthApplier {
	if e != nil && e.authApplier != nil {
		return e.authApplier
	}
	return defaultAuthApplier{}
}

// SetAuthApplier 设置鉴权应用器（扩展点：不同 provider 可注入不同策略）
func (e *BaseExecutor) SetAuthApplier(auth AuthApplier) {
	if e == nil {
		return
	}
	e.authApplier = auth
}
