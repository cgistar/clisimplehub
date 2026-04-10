package middleware

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
)

const DefaultCodexUserAgent = "codex_cli_rs/0.101.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464"

var codexCliPattern = regexp.MustCompile(`(?i)^(codex_vscode|codex_cli_rs|codex_exec)/[\d.]+`)

var ErrCompactStreamingNotSupported = errors.New("streaming not supported for compact responses")

var compactStreamingErrorJSON = []byte(`{"error":{"type":"invalid_request_error","message":"Streaming not supported for compact responses"}}`)

const codexCLIInstructions = `You are Codex, a pragmatic coding agent working in the user's shared workspace.

- Understand the relevant code before changing it.
- Prefer targeted search tools such as rg when exploring the codebase.
- If the user asks for a code change, implement it directly unless they only asked for analysis or a plan.
- Keep edits minimal, preserve existing behavior, and avoid unnecessary complexity.
- Do not revert or overwrite user changes you did not make.
- Prefer non-destructive, non-interactive commands. Do not use git reset --hard, git checkout --, or amend commits unless the user explicitly asks for it.
- Give brief progress updates while working when the task is non-trivial.
- Keep the final response concise and include verification results or limits.
- For code reviews, focus on bugs, regressions, risks, and missing tests before summaries.
- For simple terminal requests, run the command directly when possible.`

var fieldsToRemoveForNonCLI = []string{
	"temperature",
	"top_p",
	"max_output_tokens",
	"user",
	"text_formatting",
	"truncation",
	"text",
	"service_tier",
	"prompt_cache_retention",
	"safety_identifier",
}

// CodexResponsesAdaptMiddleware 在网关入口统一规范化 /responses 请求。
func CodexResponsesAdaptMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsResponsesPath(r.URL.Path) || IsCodexCLI(r.Header.Get("User-Agent")) {
			next.ServeHTTP(w, r)
			return
		}

		body, err := readRequestBody(r)
		if err != nil {
			log.Printf("[responses-middleware] read body error: %v", err)
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		adaptedBody, adaptedUserAgent, err := NormalizeCodexResponsesRequest(body, r.URL.Path, r.Header.Get("User-Agent"))
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
		r.Header.Set("User-Agent", adaptedUserAgent)
		next.ServeHTTP(w, r)
	})
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

// NormalizeCodexResponsesRequest 统一处理 /responses 请求体和 User-Agent。
func NormalizeCodexResponsesRequest(body []byte, requestPath string, userAgent string) ([]byte, string, error) {
	normalizedUserAgent := userAgent
	isCodexClient := IsCodexCLI(userAgent)
	if !isCodexClient {
		normalizedUserAgent = DefaultCodexUserAgent
	}
	if len(body) == 0 {
		return body, normalizedUserAgent, nil
	}

	var reqBody map[string]any
	if err := json.Unmarshal(body, &reqBody); err != nil {
		return body, normalizedUserAgent, nil
	}

	if IsCompactResponsesPath(requestPath) {
		if streamValue, ok := reqBody["stream"]; ok {
			if streamEnabled, ok := streamValue.(bool); ok && streamEnabled {
				return nil, normalizedUserAgent, ErrCompactStreamingNotSupported
			}
			delete(reqBody, "stream")
		}
		delete(reqBody, "store")
	} else {
		reqBody["store"] = false
	}

	if !isCodexClient {
		AdaptResponsesPayloadForNonCLI(reqBody)
	}

	adapted, err := json.Marshal(reqBody)
	if err != nil {
		return body, normalizedUserAgent, err
	}
	return adapted, normalizedUserAgent, nil
}

// AdaptResponsesPayloadForNonCLI 删除非 Codex CLI 请求中不兼容的字段并补充固定 instructions。
func AdaptResponsesPayloadForNonCLI(reqBody map[string]any) {
	for _, field := range fieldsToRemoveForNonCLI {
		delete(reqBody, field)
	}
	reqBody["instructions"] = codexCLIInstructions
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
