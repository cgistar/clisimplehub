package codexplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	codexBackend "clisimplehub/internal/codex/backend"
	"clisimplehub/internal/executor"
	appmiddleware "clisimplehub/internal/middleware"
	"clisimplehub/internal/proxy"
	chat_responses "clisimplehub/internal/transformer/chat/openai/responses"
)

type preparedCodexRequest struct {
	body          []byte
	requestModel  string
	isStreaming   bool
	clientHeaders http.Header
	chatConv      *chatCompletionsConversion
}

func (s *CodexService) prepareCodexRequest(ctx context.Context, body []byte, requestPath, userAgent, resolvedModel string, clientHeaders http.Header, isStreaming bool) (*preparedCodexRequest, *executor.ForwardResult) {
	debugLogger := executor.DebugLoggerFromContext(ctx)

	prepared := &preparedCodexRequest{
		body:          body,
		isStreaming:   isStreaming,
		clientHeaders: cloneHeaders(clientHeaders),
	}
	if proxy.IsCodexCompactResponsesPath(requestPath) {
		prepared.isStreaming = false
	}
	suffixModel := extractModelFromBody(prepared.body)
	if strings.TrimSpace(resolvedModel) != "" {
		suffixModel = strings.TrimSpace(resolvedModel)
	}

	if rewrittenBody, rewritten := applyResolvedModelToBody(prepared.body, resolvedModel); rewritten {
		prepared.body = rewrittenBody
		if debugLogger != nil {
			debugLogger.Log("应用模型映射/覆盖: upstreamModel=%q", strings.TrimSpace(resolvedModel))
		}
	}

	currentModel := extractModelFromBody(prepared.body)
	if bodyWithThinking, applied := codexBackend.ApplySuffixThinking(prepared.body, suffixModel); applied {
		prepared.body = bodyWithThinking
		if debugLogger != nil {
			debugLogger.Log("应用模型 suffix thinking: model=%q", suffixModel)
		}
		currentModel = extractModelFromBody(prepared.body)
	}

	if isChatCompletionsFormat(prepared.body) {
		chatOriginal := prepared.body
		requestModel := extractModelFromBody(chatOriginal)
		if strings.TrimSpace(requestModel) == "" {
			requestModel = codexBackend.BaseModelName(strings.TrimSpace(resolvedModel))
		}

		tr := chat_responses.Transformer{}
		converted, err := tr.TransformRequest(requestModel, chatOriginal, prepared.isStreaming)
		if err != nil {
			if debugLogger != nil {
				debugLogger.Log("Chat Completions 请求转换失败: %v", err)
			}
			return nil, buildInvalidRequestError(fmt.Sprintf("chat completions conversion failed: %v", err), err)
		}

		if bodyWithThinking, applied := codexBackend.ApplySuffixThinking(converted, suffixModel); applied {
			converted = bodyWithThinking
		}

		prepared.chatConv = &chatCompletionsConversion{originalBody: chatOriginal}
		prepared.body = converted
		prepared.requestModel = requestModel

		normalizedBody, errResult := normalizeResponsesBodyForCodex(ctx, prepared.body, requestPath, userAgent)
		if errResult != nil {
			return nil, errResult
		}
		prepared.body = normalizedBody

		if debugLogger != nil {
			debugLogger.SetSection("TransformedRequest", string(prepared.body))
			debugLogger.Log("Chat Completions → Responses API 请求转换完成")
		}
		return prepared, nil
	}

	normalizedBody, errResult := normalizeResponsesBodyForCodex(ctx, prepared.body, requestPath, userAgent)
	if errResult != nil {
		return nil, errResult
	}
	prepared.body = normalizedBody

	prepared.requestModel = extractModelFromBody(prepared.body)
	if strings.TrimSpace(prepared.requestModel) == "" {
		prepared.requestModel = currentModel
	}
	if strings.TrimSpace(prepared.requestModel) == "" {
		prepared.requestModel = codexBackend.BaseModelName(strings.TrimSpace(resolvedModel))
	}
	if debugLogger != nil {
		debugLogger.SetSection("TransformedRequest", string(prepared.body))
	}

	return prepared, nil
}

func normalizeResponsesBodyForCodex(ctx context.Context, body []byte, requestPath, userAgent string) ([]byte, *executor.ForwardResult) {
	debugLogger := executor.DebugLoggerFromContext(ctx)

	processedBody, _, err := appmiddleware.NormalizeCodexResponsesRequest(body, requestPath, userAgent)
	if err != nil {
		if debugLogger != nil {
			debugLogger.Log("请求体处理失败: %v", err)
		}
		if errors.Is(err, appmiddleware.ErrCompactStreamingNotSupported) {
			return nil, &executor.ForwardResult{
				StatusCode: http.StatusBadRequest,
				Error:      appmiddleware.ErrCompactStreamingNotSupported,
				Body:       appmiddleware.CompactStreamingErrorPayload(),
				Headers:    http.Header{"Content-Type": []string{"application/json"}},
			}
		}
		return body, nil
	}
	return processedBody, nil
}

func buildInvalidRequestError(message string, err error) *executor.ForwardResult {
	errJSON, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"type":    "invalid_request_error",
			"message": message,
		},
	})
	return &executor.ForwardResult{
		StatusCode: http.StatusBadRequest,
		Body:       errJSON,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Error:      err,
	}
}

func writeExecutorResult(w http.ResponseWriter, result *executor.ForwardResult) {
	if result == nil {
		return
	}
	if result.Headers != nil {
		for k, vals := range result.Headers {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
	}
	if result.StatusCode > 0 {
		w.WriteHeader(result.StatusCode)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
	}
	if len(result.Body) > 0 {
		_, _ = w.Write(result.Body)
	}
}

func cloneHeaders(headers http.Header) http.Header {
	if headers == nil {
		return http.Header{}
	}
	return headers.Clone()
}
