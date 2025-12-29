package executor

import "context"

type kiroDebugLogsContextKey struct{}

func WithKiroDebugLogs(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, kiroDebugLogsContextKey{}, true)
}

func shouldWriteKiroDebugLogs(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v := ctx.Value(kiroDebugLogsContextKey{})
	enabled, _ := v.(bool)
	return enabled
}

