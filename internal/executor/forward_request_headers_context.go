package executor

import (
	"context"
	"net/http"
)

type forwardRequestHeadersContextKey struct{}

// WithForwardRequestHeaders stores incoming request headers in context for transformer forwarders.
func WithForwardRequestHeaders(ctx context.Context, headers http.Header) context.Context {
	if ctx == nil {
		return nil
	}
	if headers == nil {
		return ctx
	}
	return context.WithValue(ctx, forwardRequestHeadersContextKey{}, headers.Clone())
}

// ForwardRequestHeadersFromContext gets incoming request headers from context.
func ForwardRequestHeadersFromContext(ctx context.Context) http.Header {
	if ctx == nil {
		return nil
	}
	if v := ctx.Value(forwardRequestHeadersContextKey{}); v != nil {
		if h, ok := v.(http.Header); ok {
			return h.Clone()
		}
	}
	return nil
}
