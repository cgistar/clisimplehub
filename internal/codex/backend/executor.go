package backend

import (
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
	for attempt := 1; attempt <= attempts; attempt++ {
		httpReq, body, imageMeta, identityState, err := Prepare(ctx, req)
		if err != nil {
			result := &Result{Error: err}
			if statusErr, ok := err.(StatusError); ok {
				result.StatusCode = statusErr.Code
				result.Body = statusErr.Body
			}
			return result, err
		}
		preparedBody = body
		targetURL = httpReq.URL.String()
		targetHeaders = SanitizeHeaders(httpReq.Header)

		resp, err := client.Do(httpReq)
		if err == nil {
			return resultFromHTTPResponse(ctx, resp, req, targetURL, targetHeaders, preparedBody, imageMeta, identityState)
		}
		lastErr = err
		if attempt < attempts {
			if waitErr := waitForRetry(ctx, req.RetryDelay); waitErr != nil {
				return &Result{
					TargetURL:     targetURL,
					TargetHeaders: targetHeaders,
					RequestBody:   preparedBody,
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
		Error:         err,
	}, err
}

func resultFromHTTPResponse(ctx context.Context, resp *http.Response, req Request, targetURL string, targetHeaders map[string]string, requestBody []byte, imageMeta *imagePreparedRequest, identityState IdentityState) (*Result, error) {
	if resp == nil {
		err := fmt.Errorf("nil upstream response")
		return &Result{TargetURL: targetURL, TargetHeaders: targetHeaders, RequestBody: requestBody, Error: err}, err
	}
	result := &Result{
		StatusCode:    resp.StatusCode,
		Headers:       cloneHeader(resp.Header),
		TargetURL:     targetURL,
		TargetHeaders: targetHeaders,
		RequestBody:   requestBody,
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 && req.IsStreaming && !IsCompactPath(req.Path) && !IsImagesPath(req.Path) {
		result.Stream = NewIdentityExposeReadCloser(resp.Body, identityState)
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
	}
	return result, nil
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
