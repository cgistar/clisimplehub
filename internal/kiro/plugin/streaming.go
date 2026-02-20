package kiroplugin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	kiro_claude "clisimplehub/internal/kiro/claude"
	"clisimplehub/internal/executor"
	"clisimplehub/internal/transformer"
)

// handleKiroStreamingResponse handles Kiro streaming (EventStream) responses.
// Extracted from executor/transformer_forward.go.
func handleKiroStreamingResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response, result *executor.ForwardResult, tr transformer.Transformer, modelName string, originalRequestRawJSON, requestRawJSON []byte) *executor.ForwardResult {
	debugLogger := executor.DebugLoggerFromContext(ctx)
	if debugLogger != nil {
		debugLogger.Log("开始流式响应处理")
	}
	for key, values := range resp.Header {
		switch strings.ToLower(key) {
		case "content-length", "content-encoding", "content-type":
			continue
		}
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.Header().Set("Content-Type", tr.OutputContentType(true))
	w.WriteHeader(resp.StatusCode)

	flusher, ok := w.(http.Flusher)
	if !ok {
		result.Error = fmt.Errorf("response writer does not support flushing")
		return result
	}

	reader := responseReader(resp)
	if closer, ok := reader.(io.Closer); ok && reader != resp.Body {
		defer closer.Close()
	}

	var state any
	var capture strings.Builder
	var rawCapture []byte // 捕获原始上游数据

	const (
		readTimeout            = 30 * time.Second
		maxConsecutiveTimeouts = 3
	)
	consecutiveTimeouts := 0

	readCtx, cancelRead := context.WithCancel(ctx)
	defer cancelRead()

	type readResult struct {
		n    int
		err  error
		data []byte
	}
	readChan := make(chan readResult, 1)
	go func() {
		for {
			select {
			case <-readCtx.Done():
				return
			default:
			}
			buf := make([]byte, 32*1024)
			n, err := reader.Read(buf)
			var data []byte
			if n > 0 {
				data = make([]byte, n)
				copy(data, buf[:n])
			}
			select {
			case readChan <- readResult{n: n, err: err, data: data}:
				if err != nil {
					return
				}
			case <-readCtx.Done():
				return
			}
		}
	}()

	stopAndDrain := func(t *time.Timer) {
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
	}

	timer := time.NewTimer(readTimeout)
	defer timer.Stop()

readLoop:
	for {
		stopAndDrain(timer)
		timer.Reset(readTimeout)

		select {
		case <-ctx.Done():
			stopAndDrain(timer)
			result.Error = ctx.Err()
			result.Streamed = true
			result.ResponseStream = capture.String()
			return result

		case <-timer.C:
			consecutiveTimeouts++
			if consecutiveTimeouts <= maxConsecutiveTimeouts {
				continue
			}
			result.Error = fmt.Errorf("kiro stream: consecutive read timeout (%d times)", maxConsecutiveTimeouts)
			result.Streamed = true
			result.ResponseStream = capture.String()
			return result

		case res := <-readChan:
			stopAndDrain(timer)
			n, err := res.n, res.err
			consecutiveTimeouts = 0

			if n > 0 {
				chunk := res.data
				rawCapture = append(rawCapture, chunk...) // 捕获原始数据
				if err := ctx.Err(); err != nil {
					result.Error = err
					break readLoop
				}
				outs, trErr := tr.TransformResponseStream(ctx, modelName, originalRequestRawJSON, requestRawJSON, chunk, &state)
				if trErr != nil {
					result.Error = trErr
					break readLoop
				}
				for _, out := range outs {
					if out == "" {
						continue
					}
					if _, err := w.Write([]byte(out)); err != nil {
						result.Error = context.Canceled
						break readLoop
					}
					capture.WriteString(out)
					flusher.Flush()
				}
				if tok, ok := state.(interface{ TokenUsage() (int, int) }); ok && tok != nil {
					in, out := tok.TokenUsage()
					if in != 0 || out != 0 {
						result.Tokens = &executor.TokenUsage{InputTokens: int64(in), OutputTokens: int64(out)}
					}
				}
			}

			if err == io.EOF {
				break readLoop
			}
			if err != nil {
				result.Error = err
				break readLoop
			}
		}
	}

	// Final end marker
	if state != nil {
		if outs, trErr := tr.TransformResponseStream(ctx, modelName, originalRequestRawJSON, requestRawJSON, nil, &state); trErr == nil {
			for _, out := range outs {
				if out == "" {
					continue
				}
				if _, err := w.Write([]byte(out)); err != nil {
					result.Error = context.Canceled
					break
				}
				capture.WriteString(out)
				flusher.Flush()
			}
		}
	}

	// If upstream ended without explicit end marker, finish the Claude SSE stream
	if s, ok := state.(*kiro_claude.StreamState); ok && s != nil && !s.Finished {
		for _, out := range kiro_claude.FinishStream(s) {
			if out == "" {
				continue
			}
			if _, err := w.Write([]byte(out)); err != nil {
				result.Error = context.Canceled
				break
			}
			capture.WriteString(out)
			flusher.Flush()
		}
	}

	if debugLogger != nil {
		debugLogger.Log("流式响应完成，捕获长度: %d bytes", len(capture.String()))
		debugLogger.SetSection("TransformedResponse", capture.String())
		if len(rawCapture) > 0 {
			debugLogger.SetRawSection("UpstreamResponseRaw", rawCapture)
		}
	}

	result.Streamed = true
	result.ResponseStream = capture.String()
	return result
}
