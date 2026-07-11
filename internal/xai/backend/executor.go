package backend

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
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

	// HTTP stream 路径的 compaction_trigger：走 compact + 合成 SSE
	if req.IsStreaming && !IsCompactPath(req.Path) && IsResponsesPath(req.Path) &&
		InputHasCompactionTrigger(req.Body) {
		return executeCompactionTriggerStream(ctx, req)
	}

	var lastErr error
	var preparedBody []byte
	var targetURL string
	var targetHeaders map[string]string
	var replayScope ReplayScope

	for attempt := 1; attempt <= attempts; attempt++ {
		httpReq, body, scope, err := PrepareHTTPRequest(ctx, req)
		if err != nil {
			return &Result{Error: err}, err
		}
		preparedBody = body
		replayScope = scope
		targetURL = httpReq.URL.String()
		targetHeaders = SanitizeHeaders(httpReq.Header)

		resp, err := client.Do(httpReq)
		if err == nil {
			return resultFromHTTPResponse(resp, req, targetURL, targetHeaders, preparedBody, replayScope)
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

func executeCompactionTriggerStream(ctx context.Context, req Request) (*Result, error) {
	// 非流式 compact 请求
	compactReq := req
	compactReq.IsStreaming = false
	compactReq.Path = "/xai/v1/responses/compact"
	// 去掉 compaction_trigger 等
	body, err := PrepareCompactBody(req.Body, req.Model)
	if err != nil {
		return &Result{Error: err}, err
	}
	compactReq.Body = body

	result, execErr := Execute(ctx, compactReq)
	if result == nil {
		result = &Result{Error: execErr}
	}
	if execErr != nil && result.StatusCode == 0 {
		return result, execErr
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return result, result.Error
	}

	// 合成 SSE
	sseBody := SyntheticCompactionStream(body, BaseModelName(req.Model), result.Body)
	headers := http.Header{}
	headers.Set("Content-Type", "text/event-stream")
	return &Result{
		StatusCode:    http.StatusOK,
		Headers:       headers,
		Body:          sseBody,
		TargetURL:     result.TargetURL,
		TargetHeaders: result.TargetHeaders,
		RequestBody:   body,
		ReplayScope:   result.ReplayScope,
	}, nil
}

func resultFromHTTPResponse(resp *http.Response, req Request, targetURL string, targetHeaders map[string]string, requestBody []byte, replayScope ReplayScope) (*Result, error) {
	if resp == nil {
		err := fmt.Errorf("nil upstream response")
		return &Result{TargetURL: targetURL, TargetHeaders: targetHeaders, RequestBody: requestBody, ReplayScope: replayScope, Error: err}, err
	}
	result := &Result{
		StatusCode:    resp.StatusCode,
		Headers:       resp.Header.Clone(),
		TargetURL:     targetURL,
		TargetHeaders: targetHeaders,
		RequestBody:   requestBody,
		ReplayScope:   replayScope,
	}

	// 流式成功：透传并做 reasoning 形态归一；旁路 sniff completed 写 replay
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && req.IsStreaming && !IsCompactPath(req.Path) {
		if IsResponsesPath(req.Path) {
			stream := WrapReasoningStream(resp.Body)
			if replayScope.Valid() {
				stream = WrapReplayCacheStream(stream, replayScope)
			}
			result.Stream = stream
		} else {
			result.Stream = resp.Body
		}
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
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && IsResponsesPath(req.Path) {
		// 上游可能是 SSE，非流客户端需聚合
		if !req.IsStreaming && LooksLikeSSE(data) {
			// 聚合前先缓存 completed 事件
			if replayScope.Valid() {
				cacheReplayFromSSEBytes(replayScope, data)
			}
			data = AggregateResponsesSSE(data)
		} else {
			data = NormalizeNonStreamReasoning(data)
			if replayScope.Valid() {
				// 非 SSE 的 completed 对象
				CacheReasoningReplayFromCompleted(replayScope, data)
				// 也可能是 {type:response.completed, response:{...}}
				if typ := gjson.GetBytes(data, "type").String(); typ == "response.completed" {
					CacheReasoningReplayFromCompleted(replayScope, data)
				}
			}
		}
	}
	result.Body = data
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Error = fmt.Errorf("upstream returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
		return result, result.Error
	}
	return result, nil
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// WrapReplayCacheStream 旁路扫描 SSE：收集 output_item.done，completed 时 patch output 再写 cache。
func WrapReplayCacheStream(inner io.ReadCloser, scope ReplayScope) io.ReadCloser {
	if inner == nil || !scope.Valid() {
		return inner
	}
	return &replayCacheStream{
		inner:               inner,
		scope:               scope,
		buf:                 make([]byte, 0, 4096),
		outputItemsByIndex:  make(map[int64][]byte),
		outputItemsFallback: make([][]byte, 0, 4),
	}
}

type replayCacheStream struct {
	inner               io.ReadCloser
	scope               ReplayScope
	buf                 []byte
	done                bool
	outputItemsByIndex  map[int64][]byte
	outputItemsFallback [][]byte
}

func (s *replayCacheStream) Read(p []byte) (int, error) {
	n, err := s.inner.Read(p)
	if n > 0 && !s.done {
		s.buf = append(s.buf, p[:n]...)
		if len(s.buf) > 8*1024*1024 {
			s.buf = s.buf[len(s.buf)-4*1024*1024:]
		}
		// 增量解析完整行
		for {
			idx := bytes.IndexByte(s.buf, '\n')
			if idx < 0 {
				break
			}
			line := bytes.TrimSpace(s.buf[:idx])
			s.buf = s.buf[idx+1:]
			s.consumeSSELine(line)
		}
	}
	if err != nil && !s.done && len(s.buf) > 0 {
		s.consumeSSELine(bytes.TrimSpace(s.buf))
		s.buf = nil
	}
	return n, err
}

func (s *replayCacheStream) consumeSSELine(line []byte) {
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	payload := bytes.TrimSpace(line[len("data:"):])
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return
	}
	payload = NormalizeReasoningSummaryData(payload)
	typ := gjson.GetBytes(payload, "type").String()
	switch typ {
	case "response.output_item.done":
		collectOutputItemDone(payload, s.outputItemsByIndex, &s.outputItemsFallback)
	case "response.completed":
		patched := patchCompletedOutput(payload, s.outputItemsByIndex, s.outputItemsFallback)
		CacheReasoningReplayFromCompleted(s.scope, patched)
		s.done = true
	}
}

func (s *replayCacheStream) Close() error {
	if s.inner != nil {
		return s.inner.Close()
	}
	return nil
}

func cacheReplayFromSSEBytes(scope ReplayScope, data []byte) {
	if !scope.Valid() || len(data) == 0 {
		return
	}
	byIndex := make(map[int64][]byte)
	var fallback [][]byte
	var completed []byte
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(line[len("data:"):])
		if len(payload) == 0 {
			continue
		}
		payload = NormalizeReasoningSummaryData(payload)
		switch gjson.GetBytes(payload, "type").String() {
		case "response.output_item.done":
			collectOutputItemDone(payload, byIndex, &fallback)
		case "response.completed":
			completed = payload
		}
	}
	if len(completed) == 0 {
		return
	}
	CacheReasoningReplayFromCompleted(scope, patchCompletedOutput(completed, byIndex, fallback))
}

func collectOutputItemDone(eventData []byte, byIndex map[int64][]byte, fallback *[][]byte) {
	itemResult := gjson.GetBytes(eventData, "item")
	if !itemResult.Exists() || itemResult.Type != gjson.JSON {
		return
	}
	if outputIndex := gjson.GetBytes(eventData, "output_index"); outputIndex.Exists() {
		byIndex[outputIndex.Int()] = []byte(itemResult.Raw)
		return
	}
	*fallback = append(*fallback, []byte(itemResult.Raw))
}

func patchCompletedOutput(eventData []byte, byIndex map[int64][]byte, fallback [][]byte) []byte {
	outputResult := gjson.GetBytes(eventData, "response.output")
	shouldPatch := (!outputResult.Exists() || !outputResult.IsArray() || len(outputResult.Array()) == 0) &&
		(len(byIndex) > 0 || len(fallback) > 0)
	if !shouldPatch {
		return eventData
	}
	// 按 index 排序
	indexes := make([]int64, 0, len(byIndex))
	for idx := range byIndex {
		indexes = append(indexes, idx)
	}
	for i := 0; i < len(indexes); i++ {
		for j := i + 1; j < len(indexes); j++ {
			if indexes[j] < indexes[i] {
				indexes[i], indexes[j] = indexes[j], indexes[i]
			}
		}
	}
	var buf bytes.Buffer
	buf.WriteByte('[')
	wrote := false
	for _, idx := range indexes {
		if wrote {
			buf.WriteByte(',')
		}
		buf.Write(byIndex[idx])
		wrote = true
	}
	for _, item := range fallback {
		if wrote {
			buf.WriteByte(',')
		}
		buf.Write(item)
		wrote = true
	}
	buf.WriteByte(']')
	if !wrote {
		return eventData
	}
	patched, _ := sjson.SetRawBytes(eventData, "response.output", buf.Bytes())
	return patched
}
