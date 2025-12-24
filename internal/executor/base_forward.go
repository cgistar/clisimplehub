package executor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Forward 实现通用的请求转发逻辑
func (e *BaseExecutor) Forward(ctx context.Context, endpoint *EndpointConfig, req *ForwardRequest, w http.ResponseWriter) *ForwardResult {
	result := &ForwardResult{}

	targetURL, err := BuildTargetURL(endpoint.APIURL, req.Path, req.RawQuery)
	if err != nil {
		result.Error = err
		return result
	}
	result.TargetURL = targetURL

	requestBody := applyModelMapping(req.Body, endpoint)
	proxyReq, err := http.NewRequestWithContext(ctx, req.Method, targetURL, bytes.NewReader(requestBody))
	if err != nil {
		result.Error = fmt.Errorf("failed to create request: %w", err)
		return result
	}

	copyRequestHeaders(proxyReq, req.Headers)
	e.getAuthApplier().Apply(proxyReq, endpoint, req.IsStreaming)
	ApplyEndpointHeaders(proxyReq, endpoint)

	result.TargetHeaders = sanitizeHeaders(proxyReq.Header)

	client := NewHTTPClient(endpoint, 0)
	resp, err := client.Do(proxyReq)
	if err != nil {
		result.Error = fmt.Errorf("request failed: %w", err)
		return result
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	result.Headers = resp.Header.Clone()

	contentType := resp.Header.Get("Content-Type")
	if req.IsStreaming && strings.Contains(contentType, "text/event-stream") {
		return e.handleStreamingResponse(ctx, w, resp, result)
	}

	return e.handleNonStreamingResponse(resp, result)
}

func (e *BaseExecutor) handleStreamingResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response, result *ForwardResult) *ForwardResult {
	for key, values := range resp.Header {
		if key == "Content-Length" || key == "Content-Encoding" {
			continue
		}
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	flusher, ok := w.(http.Flusher)
	if !ok {
		result.Error = fmt.Errorf("response writer does not support flushing")
		return result
	}

	reader := getResponseReader(resp)
	if closer, ok := reader.(io.Closer); ok && reader != resp.Body {
		defer closer.Close()
	}

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var capture strings.Builder
	const maxCaptureSize = 50 * 1024

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			result.Error = ctx.Err()
			result.Streamed = true
			result.ResponseStream = capture.String()
			return result
		default:
		}

		line := scanner.Bytes()
		if capture.Len() < maxCaptureSize {
			capture.Write(line)
			capture.WriteByte('\n')
		}

		if tokens := e.extractStreamTokens(line); tokens != nil {
			result.Tokens = tokens
		}

		if _, err := w.Write(line); err != nil {
			result.Error = context.Canceled
			break
		}
		if _, err := w.Write([]byte("\n")); err != nil {
			result.Error = context.Canceled
			break
		}
		flusher.Flush()
	}

	if err := scanner.Err(); err != nil {
		result.Error = err
	}

	result.ResponseStream = capture.String()
	result.Streamed = true
	return result
}

func (e *BaseExecutor) handleNonStreamingResponse(resp *http.Response, result *ForwardResult) *ForwardResult {
	reader := getResponseReader(resp)
	if closer, ok := reader.(io.Closer); ok && reader != resp.Body {
		defer closer.Close()
	}

	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		result.Headers.Del("Content-Encoding")
		result.Headers.Del("Content-Length")
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		result.Error = fmt.Errorf("failed to read response: %w", err)
		return result
	}

	result.Body = body
	result.Tokens = e.ExtractTokens(body)
	return result
}
