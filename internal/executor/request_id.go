package executor

import "context"

type requestIDContextKey struct{}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	if ctx == nil {
		return nil
	}
	if requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDContextKey{}, requestID)
}

func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v := ctx.Value(requestIDContextKey{}); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

