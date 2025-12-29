package executor

import (
	"context"
)

type captureUpstreamRequestBodyContextKey struct{}

const maxCapturedUpstreamRequestBodyBytes = 8000 * 1024

func WithCaptureUpstreamRequestBody(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, captureUpstreamRequestBodyContextKey{}, true)
}

func shouldCaptureUpstreamRequestBody(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v := ctx.Value(captureUpstreamRequestBodyContextKey{})
	enabled, _ := v.(bool)
	return enabled
}

func truncateCapturedUpstreamRequestBody(body []byte) (string, bool) {
	if len(body) <= maxCapturedUpstreamRequestBodyBytes {
		return string(body), false
	}
	kept := body[:maxCapturedUpstreamRequestBodyBytes]
	return string(kept) + "...(truncated)", true
}

