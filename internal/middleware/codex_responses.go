package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"

	codexShared "clisimplehub/internal/codex/shared"
)

const DefaultCodexUserAgent = codexShared.DefaultCodexUserAgent

var codexCliPattern = regexp.MustCompile(`(?i)^codex(?:-cli|-tui)?/[\d.]+`)

type originalHeaderContextKey struct{}

var ErrCompactStreamingNotSupported = errors.New("streaming not supported for compact responses")

var compactStreamingErrorJSON = []byte(`{"error":{"type":"invalid_request_error","message":"Streaming not supported for compact responses"}}`)

// fieldsToRemoveForCodexUpstream 仅删除 Codex 上游硬伤字段。
// store / context_management 透传客户端值
var fieldsToRemoveForCodexUpstream = []string{
	"previous_response_id",
	"prompt_cache_retention",
	"safety_identifier",
	"stream_options",
}

// CodexResponsesAdaptMiddleware 在网关入口统一规范化 /responses 请求。
// cli-align：不再强制改写 UA、不再注入 Codex CLI instructions、不再剥离采样字段。
func CodexResponsesAdaptMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsResponsesPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		r = r.WithContext(context.WithValue(r.Context(), originalHeaderContextKey{}, r.Header.Clone()))

		body, err := readRequestBody(r)
		if err != nil {
			log.Printf("[responses-middleware] read body error: %v", err)
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		adaptedBody, _, err := NormalizeCodexResponsesRequest(body, r.URL.Path, r.Header.Get("User-Agent"))
		if err != nil {
			if errors.Is(err, ErrCompactStreamingNotSupported) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write(CompactStreamingErrorPayload())
				return
			}
			log.Printf("[responses-middleware] normalize body error: %v", err)
			adaptedBody = body
		}

		restoreRequestBody(r, adaptedBody)
		next.ServeHTTP(w, r)
	})
}

func OriginalHeadersFromContext(ctx context.Context) (http.Header, bool) {
	if ctx == nil {
		return nil, false
	}
	headers, ok := ctx.Value(originalHeaderContextKey{}).(http.Header)
	if !ok || headers == nil {
		return nil, false
	}
	return headers.Clone(), true
}

func readRequestBody(r *http.Request) ([]byte, error) {
	if r == nil || r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	return body, err
}

func restoreRequestBody(r *http.Request, body []byte) {
	if r == nil {
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
}

// NormalizeCodexResponsesRequest 统一处理 /responses 请求体（cli-align）。
// 返回的 userAgent 保持入参原样，不再强制伪装。
func NormalizeCodexResponsesRequest(body []byte, requestPath string, userAgent string) ([]byte, string, error) {
	if len(body) == 0 {
		return body, userAgent, nil
	}

	var reqBody map[string]any
	if err := json.Unmarshal(body, &reqBody); err != nil {
		return body, userAgent, nil
	}

	if IsCompactResponsesPath(requestPath) {
		if streamValue, ok := reqBody["stream"]; ok {
			if streamEnabled, ok := streamValue.(bool); ok && streamEnabled {
				return nil, userAgent, ErrCompactStreamingNotSupported
			}
			delete(reqBody, "stream")
		}
	}
	RemoveFieldsForCodexUpstream(reqBody)

	// instructions 仅做空值归一，不注入 Codex CLI 系统提示。
	if v, ok := reqBody["instructions"]; !ok || v == nil {
		reqBody["instructions"] = ""
	}

	adapted, err := json.Marshal(reqBody)
	if err != nil {
		return body, userAgent, err
	}
	return adapted, userAgent, nil
}

// RemoveFieldsForCodexUpstream 删除客户端可能携带但会降低 Codex OAuth 上游稳定性的字段。
func RemoveFieldsForCodexUpstream(reqBody map[string]any) {
	for _, field := range fieldsToRemoveForCodexUpstream {
		delete(reqBody, field)
	}
}

// AdaptResponsesPayloadForNonCLI 保留 API 兼容；cli-align 下仅归一 instructions。
// Deprecated: 默认路径已不再调用。
func AdaptResponsesPayloadForNonCLI(reqBody map[string]any) {
	if v, ok := reqBody["instructions"]; !ok || v == nil {
		reqBody["instructions"] = ""
	}
}

// AdaptCompactResponsesPayloadForNonCLI 保留 API 兼容；cli-align 下仅归一 instructions。
// Deprecated: 默认路径已不再调用。
func AdaptCompactResponsesPayloadForNonCLI(reqBody map[string]any) {
	if v, ok := reqBody["instructions"]; !ok || v == nil {
		reqBody["instructions"] = ""
	}
}

func IsCodexCLI(userAgent string) bool {
	return codexCliPattern.MatchString(userAgent)
}

func IsResponsesPath(path string) bool {
	p := strings.ToLower(strings.TrimRight(strings.TrimSpace(path), "/"))
	return strings.HasSuffix(p, "/responses") || strings.HasSuffix(p, "/responses/compact")
}

func IsCompactResponsesPath(path string) bool {
	p := strings.ToLower(strings.TrimRight(strings.TrimSpace(path), "/"))
	return strings.HasSuffix(p, "/responses/compact")
}

func CompactStreamingErrorPayload() []byte {
	return append([]byte(nil), compactStreamingErrorJSON...)
}
