package executor

import (
	"context"

	"clisimplehub/internal/logger"
)

type debugLoggerContextKey struct{}

// WithDebugLogger 将调试日志记录器添加到上下文
func WithDebugLogger(ctx context.Context, debugLogger *logger.RequestDebugLogger) context.Context {
	if ctx == nil || debugLogger == nil {
		return ctx
	}
	return context.WithValue(ctx, debugLoggerContextKey{}, debugLogger)
}

// DebugLoggerFromContext 从上下文获取调试日志记录器
func DebugLoggerFromContext(ctx context.Context) *logger.RequestDebugLogger {
	if ctx == nil {
		return nil
	}
	if v := ctx.Value(debugLoggerContextKey{}); v != nil {
		if dl, ok := v.(*logger.RequestDebugLogger); ok {
			return dl
		}
	}
	return nil
}
