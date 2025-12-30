package executor

import (
	"context"
	"encoding/base64"
	"unicode/utf8"
)

type captureUpstreamRequestBodyContextKey struct{}
type captureUpstreamResponseBodyContextKey struct{}

func WithCaptureUpstreamRequestBody(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, captureUpstreamRequestBodyContextKey{}, true)
}

func WithCaptureUpstreamResponseBody(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, captureUpstreamResponseBodyContextKey{}, true)
}

func shouldCaptureUpstreamRequestBody(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v := ctx.Value(captureUpstreamRequestBodyContextKey{})
	enabled, _ := v.(bool)
	return enabled
}

func shouldCaptureUpstreamResponseBody(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v := ctx.Value(captureUpstreamResponseBodyContextKey{})
	enabled, _ := v.(bool)
	return enabled
}

func capturedUpstreamRequestBody(body []byte) string {
	return string(body)
}

func capturedUpstreamResponseBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	// 检查是否是有效的 UTF-8 文本
	isText := utf8.Valid(body)

	var result string

	if isText {
		result = string(body)
	} else {
		// 二进制内容，使用 base64 编码
		result = base64.StdEncoding.EncodeToString(body)
	}

	return result
}

type limitedByteBuffer struct {
	buf []byte
	max int
}

func (b *limitedByteBuffer) Append(p []byte) {
	if b == nil || len(p) == 0 {
		return
	}
	if b.max > 0 {
		remaining := b.max - len(b.buf)
		if remaining <= 0 {
			return
		}
		if len(p) > remaining {
			p = p[:remaining]
		}
	}
	b.buf = append(b.buf, p...)
}

func (b *limitedByteBuffer) AppendByte(ch byte) {
	if b == nil {
		return
	}
	if b.max > 0 && len(b.buf) >= b.max {
		return
	}
	b.buf = append(b.buf, ch)
}

func (b *limitedByteBuffer) Bytes() []byte {
	if b == nil {
		return nil
	}
	return b.buf
}
