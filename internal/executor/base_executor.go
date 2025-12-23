package executor

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"clisimplehub/internal/logger"
	"clisimplehub/internal/usage"
)

// BaseExecutor 提供执行器的通用实现
type BaseExecutor struct {
	id string
}

// NewBaseExecutor 创建基础执行器
func NewBaseExecutor(id string) *BaseExecutor {
	return &BaseExecutor{id: id}
}

func (e *BaseExecutor) Identifier() string {
	return e.id
}

// Forward 实现通用的请求转发逻辑
func (e *BaseExecutor) Forward(ctx context.Context, endpoint *EndpointConfig, req *ForwardRequest, w http.ResponseWriter) *ForwardResult {
	result := &ForwardResult{}

	// 构建目标 URL
	targetURL, err := buildTargetURL(endpoint.APIURL, req.Path, req.RawQuery)
	if err != nil {
		result.Error = err
		return result
	}
	result.TargetURL = targetURL

	// 应用模型映射
	requestBody := applyModelMapping(req.Body, endpoint)

	// 创建 HTTP 请求
	proxyReq, err := http.NewRequestWithContext(ctx, req.Method, targetURL, bytes.NewReader(requestBody))
	if err != nil {
		result.Error = fmt.Errorf("failed to create request: %w", err)
		return result
	}

	// 复制请求头
	copyRequestHeaders(proxyReq, req.Headers)

	// 应用端点认证
	applyAuth(proxyReq, endpoint, req.IsStreaming)

	// 应用自定义 headers
	applyCustomHeaders(proxyReq, endpoint)

	result.TargetHeaders = sanitizeHeaders(proxyReq.Header)

	// 创建 HTTP 客户端（支持代理）
	client := NewHTTPClient(endpoint, 0)

	// 发送请求
	resp, err := client.Do(proxyReq)
	if err != nil {
		result.Error = fmt.Errorf("request failed: %w", err)
		return result
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	result.Headers = resp.Header.Clone()

	// 处理流式响应
	contentType := resp.Header.Get("Content-Type")
	if req.IsStreaming && strings.Contains(contentType, "text/event-stream") {
		return e.handleStreamingResponse(w, resp, result)
	}

	// 处理非流式响应
	return e.handleNonStreamingResponse(resp, result)
}

func (e *BaseExecutor) handleStreamingResponse(w http.ResponseWriter, resp *http.Response, result *ForwardResult) *ForwardResult {
	// 复制响应头
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
		line := scanner.Bytes()

		// 捕获流数据用于日志
		if capture.Len() < maxCaptureSize {
			capture.Write(line)
			capture.WriteByte('\n')
		}

		// 提取 token 使用量
		if tokens := e.extractStreamTokens(line); tokens != nil {
			result.Tokens = tokens
		}

		// 写入响应
		w.Write(line)
		w.Write([]byte("\n"))
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

	// 解压后移除编码头
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

func (e *BaseExecutor) extractStreamTokens(line []byte) *TokenUsage {
	if !bytes.HasPrefix(line, []byte("data:")) {
		return nil
	}

	jsonData := bytes.TrimSpace(line[5:])
	if len(jsonData) == 0 || bytes.Equal(jsonData, []byte("[DONE]")) {
		return nil
	}

	stats := usage.ExtractFromResponse(jsonData)
	if stats == nil || stats.IsEmpty() {
		return nil
	}
	return &TokenUsage{
		InputTokens:  stats.InputTokens,
		OutputTokens: stats.OutputTokens,
		CachedCreate: stats.CachedCreate,
		CachedRead:   stats.CachedRead,
		Reasoning:    stats.Reasoning,
	}
}

// ExtractTokens 从响应体提取 token 使用量
func (e *BaseExecutor) ExtractTokens(body []byte) *TokenUsage {
	stats := usage.ExtractFromResponse(body)
	if stats == nil || stats.IsEmpty() {
		return nil
	}
	return &TokenUsage{
		InputTokens:  stats.InputTokens,
		OutputTokens: stats.OutputTokens,
		CachedCreate: stats.CachedCreate,
		CachedRead:   stats.CachedRead,
		Reasoning:    stats.Reasoning,
	}
}

// 辅助函数

func buildTargetURL(apiURL, path, rawQuery string) (string, error) {
	base := strings.TrimSpace(apiURL)
	if base == "" {
		return "", fmt.Errorf("empty api_url")
	}

	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "https://" + base
	}
	base = strings.TrimRight(base, "/")

	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid api_url: %w", err)
	}

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = rawQuery

	return u.String(), nil
}

func applyModelMapping(body []byte, endpoint *EndpointConfig) []byte {
	if endpoint == nil || len(endpoint.Models) == 0 && endpoint.Model == "" {
		return body
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}

	modelVal, ok := req["model"]
	requestModel, ok := modelVal.(string)
	if !ok || requestModel == "" {
		// 请求体没有 model 时，尽量注入默认 model（保持与 proxy 旧逻辑一致）
		if endpoint.Model != "" {
			req["model"] = endpoint.Model
			if result, err := json.Marshal(req); err == nil {
				return result
			}
		}
		return body
	}

	// 查找模型映射
	for _, mapping := range endpoint.Models {
		alias := strings.TrimSpace(mapping.Alias)
		name := strings.TrimSpace(mapping.Name)

		if alias != "" && strings.EqualFold(alias, requestModel) {
			if name != "" {
				req["model"] = name
				if result, err := json.Marshal(req); err == nil {
					return result
				}
			}
			return body
		}

		if name != "" && strings.EqualFold(name, requestModel) {
			return body
		}
	}

	// 使用默认模型
	if endpoint.Model != "" {
		req["model"] = endpoint.Model
		if result, err := json.Marshal(req); err == nil {
			return result
		}
	}

	return body
}

func copyRequestHeaders(dst *http.Request, src http.Header) {
	skipHeaders := map[string]bool{
		"host":            true,
		"accept-encoding": true,
		"content-length":  true,
		"authorization":   true,
		"x-api-key":       true,
	}

	for key, values := range src {
		if skipHeaders[strings.ToLower(key)] {
			continue
		}
		for _, v := range values {
			dst.Header.Add(key, v)
		}
	}
}

func applyAuth(req *http.Request, endpoint *EndpointConfig, isStreaming bool) {
	if endpoint == nil {
		return
	}

	switch endpoint.InterfaceType {
	case "gemini":
		q := req.URL.Query()
		q.Set("key", endpoint.APIKey)
		if isStreaming {
			q.Set("alt", "sse")
		}
		req.URL.RawQuery = q.Encode()
	case "codex", "chat":
		req.Header.Set("Authorization", "Bearer "+endpoint.APIKey)
	default:
		req.Header.Set("Authorization", "Bearer "+endpoint.APIKey)
		req.Header.Set("x-api-key", endpoint.APIKey)
	}
}

func applyCustomHeaders(req *http.Request, endpoint *EndpointConfig) {
	if endpoint == nil || len(endpoint.Headers) == 0 {
		return
	}

	for key, value := range endpoint.Headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			req.Header.Set(key, value)
		}
	}
}

func sanitizeHeaders(h http.Header) map[string]string {
	result := make(map[string]string)
	sensitiveKeys := map[string]bool{
		"authorization": true,
		"x-api-key":     true,
		"api-key":       true,
	}

	for key, values := range h {
		if sensitiveKeys[strings.ToLower(key)] {
			if len(values) > 0 && len(values[0]) > 8 {
				result[key] = values[0][:4] + "****" + values[0][len(values[0])-4:]
			} else {
				result[key] = "****"
			}
		} else if len(values) > 0 {
			result[key] = values[0]
		}
	}
	return result
}

func getResponseReader(resp *http.Response) io.Reader {
	encoding := resp.Header.Get("Content-Encoding")
	if strings.EqualFold(encoding, "gzip") {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			logger.Warn("[Executor] gzip reader failed: %v", err)
			return resp.Body
		}
		return gzReader
	}
	return resp.Body
}
