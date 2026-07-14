package backend

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func Execute(ctx context.Context, req Request) (*Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	client := req.Client
	if client == nil {
		client = http.DefaultClient
	}
	attempts := req.Attempts
	if attempts <= 0 {
		attempts = 1
	}

	var lastErr error
	var preparedBody []byte
	var targetURL string
	var targetHeaders map[string]string
	var replayScope ReplayScope
	for attempt := 1; attempt <= attempts; attempt++ {
		httpReq, body, imageMeta, identityState, scope, err := Prepare(ctx, req)
		if err != nil {
			result := &Result{Error: err, ReplayScope: scope}
			if statusErr, ok := err.(StatusError); ok {
				result.StatusCode = statusErr.Code
				result.Body = statusErr.Body
			}
			return result, err
		}
		preparedBody = body
		targetURL = httpReq.URL.String()
		targetHeaders = SanitizeHeaders(httpReq.Header)
		replayScope = scope

		resp, err := client.Do(httpReq)
		if err == nil {
			return resultFromHTTPResponse(ctx, resp, req, targetURL, targetHeaders, preparedBody, imageMeta, identityState, replayScope)
		}
		lastErr = err
		if attempt < attempts {
			if waitErr := waitForRetry(ctx, req.RetryDelay); waitErr != nil {
				return &Result{
					TargetURL:     targetURL,
					TargetHeaders: targetHeaders,
					RequestBody:   preparedBody,
					ReplayScope:   replayScope,
					Error:         waitErr,
				}, waitErr
			}
			continue
		}
	}

	err := fmt.Errorf("request failed: %w", lastErr)
	return &Result{
		TargetURL:     targetURL,
		TargetHeaders: targetHeaders,
		RequestBody:   preparedBody,
		ReplayScope:   replayScope,
		Error:         err,
	}, err
}

func resultFromHTTPResponse(ctx context.Context, resp *http.Response, req Request, targetURL string, targetHeaders map[string]string, requestBody []byte, imageMeta *imagePreparedRequest, identityState IdentityState, replayScope ReplayScope) (*Result, error) {
	if resp == nil {
		err := fmt.Errorf("nil upstream response")
		return &Result{TargetURL: targetURL, TargetHeaders: targetHeaders, RequestBody: requestBody, ReplayScope: replayScope, Error: err}, err
	}
	result := &Result{
		StatusCode:    resp.StatusCode,
		Headers:       cloneHeader(resp.Header),
		TargetURL:     targetURL,
		TargetHeaders: targetHeaders,
		RequestBody:   requestBody,
		ReplayScope:   replayScope,
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 && req.IsStreaming && !IsCompactPath(req.Path) && !IsImagesPath(req.Path) {
		stream := NewIdentityExposeReadCloser(resp.Body, identityState)
		if replayScope.Valid() {
			stream = NewReasoningReplayCaptureReadCloser(stream, replayScope)
		}
		result.Stream = stream
		// 流式结果的协议由本层确定，不能继承上游可能错误的 JSON 响应头。
		result.Headers.Set("Content-Type", "text/event-stream")
		result.Headers.Del("Content-Length")
		return result, nil
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && req.IsStreaming && IsImagesPath(req.Path) {
		result.Stream = BuildOpenAIImageStream(ctx, NewIdentityExposeReadCloser(resp.Body, identityState), req, imageMeta)
		result.Headers.Set("Content-Type", "text/event-stream")
		result.Headers.Del("Content-Length")
		return result, nil
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 52_428_800))
	closeErr := resp.Body.Close()
	if err != nil {
		result.Error = fmt.Errorf("read response: %w", err)
		return result, result.Error
	}
	if closeErr != nil {
		result.Error = fmt.Errorf("close response: %w", closeErr)
		return result, result.Error
	}
	data = ExposeIdentityPayload(data, identityState)
	result.Body = data
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		ClearReasoningReplayOnInvalidSignature(replayScope, resp.StatusCode, data)
		statusErr := NewStatusError(resp.StatusCode, data, time.Now())
		result.StatusCode = statusErr.Code
		result.Body = statusErr.Body
		result.Error = statusErr
		return result, statusErr
	}
	if IsImagesPath(req.Path) {
		var body []byte
		if req.IsStreaming {
			body, err = BuildOpenAIImageStreamResponse(data, req, imageMeta)
			result.Headers.Set("Content-Type", "text/event-stream")
		} else {
			body, err = BuildOpenAIImageResponse(data, req, imageMeta)
			result.Headers.Set("Content-Type", "application/json")
		}
		if err != nil {
			result.Error = err
			return result, err
		}
		result.Body = body
		result.Headers.Del("Content-Length")
		return result, nil
	}
	// 非流式 /responses：上游强制 SSE，聚合为 completed JSON 并回填空 output。
	if !req.IsStreaming && !IsCompactPath(req.Path) {
		if completed, ok := AggregateResponsesSSEToCompleted(data); ok {
			result.Body = completed
			result.Headers.Set("Content-Type", "application/json")
			result.Headers.Del("Content-Length")
			if replayScope.Valid() {
				CacheReasoningReplayFromCompleted(replayScope, completed)
			}
			return result, nil
		}
	}
	if replayScope.Valid() {
		cacheReplayFromPayload(replayScope, data)
	}
	return result, nil
}

func cacheReplayFromPayload(scope ReplayScope, data []byte) {
	if !scope.Valid() || len(data) == 0 {
		return
	}
	if len(data) > 0 && data[0] == '{' && (bytes.Contains(data, []byte(`"output"`)) || bytes.Contains(data, []byte(`"id"`))) {
		CacheReasoningReplayFromCompleted(scope, data)
		return
	}
	byIndex := make(map[int64][]byte)
	var fallback [][]byte
	var completed []byte
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(line[5:])
		if len(payload) == 0 || !bytes.HasPrefix(payload, []byte("{")) {
			continue
		}
		switch extractJSONType(payload) {
		case "response.output_item.done":
			CollectOutputItemDone(payload, byIndex, &fallback)
		case "response.completed":
			completed = PatchCompletedOutput(payload, byIndex, fallback)
		}
	}
	if len(completed) > 0 {
		CacheReasoningReplayFromCompleted(scope, completed)
	}
}

func extractJSONType(payload []byte) string {
	const key = `"type"`
	idx := bytes.Index(payload, []byte(key))
	if idx < 0 {
		return ""
	}
	rest := payload[idx+len(key):]
	colon := bytes.IndexByte(rest, ':')
	if colon < 0 {
		return ""
	}
	rest = bytes.TrimSpace(rest[colon+1:])
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:]
	end := bytes.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return string(rest[:end])
}

// ReasoningReplayCaptureReadCloser 在流式透传时旁路收集 completed 并写 replay cache。
type ReasoningReplayCaptureReadCloser struct {
	inner     io.ReadCloser
	scope     ReplayScope
	buf       []byte
	byIndex   map[int64][]byte
	fallback  [][]byte
	completed []byte
	closed    bool
}

func NewReasoningReplayCaptureReadCloser(inner io.ReadCloser, scope ReplayScope) io.ReadCloser {
	if inner == nil || !scope.Valid() {
		return inner
	}
	return &ReasoningReplayCaptureReadCloser{
		inner:   inner,
		scope:   scope,
		byIndex: make(map[int64][]byte),
	}
}

func (r *ReasoningReplayCaptureReadCloser) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	if n > 0 {
		r.feed(p[:n])
	}
	if err != nil {
		r.finish()
	}
	return n, err
}

func (r *ReasoningReplayCaptureReadCloser) Close() error {
	r.finish()
	if r.inner != nil {
		return r.inner.Close()
	}
	return nil
}

func (r *ReasoningReplayCaptureReadCloser) feed(chunk []byte) {
	r.buf = append(r.buf, chunk...)
	for {
		idx := bytes.IndexByte(r.buf, '\n')
		if idx < 0 {
			return
		}
		line := bytes.TrimSpace(r.buf[:idx])
		r.buf = r.buf[idx+1:]
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(line[5:])
		if len(payload) == 0 || !bytes.HasPrefix(payload, []byte("{")) {
			continue
		}
		switch extractJSONType(payload) {
		case "response.output_item.done":
			CollectOutputItemDone(payload, r.byIndex, &r.fallback)
		case "response.completed":
			r.completed = PatchCompletedOutput(append([]byte(nil), payload...), r.byIndex, r.fallback)
		}
	}
}

func (r *ReasoningReplayCaptureReadCloser) finish() {
	if r.closed {
		return
	}
	r.closed = true
	if len(r.completed) > 0 {
		CacheReasoningReplayFromCompleted(r.scope, r.completed)
	}
}

func SanitizeHeaders(headers http.Header) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		if strings.EqualFold(key, "Authorization") {
			out[key] = "Bearer ***"
			continue
		}
		out[key] = values[0]
	}
	return out
}
