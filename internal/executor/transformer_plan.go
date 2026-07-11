package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	codexBackend "clisimplehub/internal/codex/backend"
	xaiBackend "clisimplehub/internal/xai/backend"
	appmiddleware "clisimplehub/internal/middleware"
	"clisimplehub/internal/transformer"
	chat_anthropic "clisimplehub/internal/transformer/chat/anthropic/messages"
	chat_responses "clisimplehub/internal/transformer/chat/openai/responses"
	claude_responses "clisimplehub/internal/transformer/claude/openai/responses"

	"github.com/tidwall/gjson"
)

func (c *ExecutionContext) BuildTransformationPlan(ctx context.Context, interfaceType string, endpoint *EndpointConfig, req *ForwardRequest) (*TransformationPlan, *ForwardResult) {
	if endpoint == nil || req == nil {
		return nil, &ForwardResult{
			StatusCode: http.StatusBadRequest,
			Error:      fmt.Errorf("nil endpoint or request"),
		}
	}

	if strings.EqualFold(strings.TrimSpace(endpoint.Transformer), "openai/codex") {
		return c.buildCodexTransformationPlan(ctx, interfaceType, endpoint, req)
	}
	if strings.EqualFold(strings.TrimSpace(endpoint.Transformer), "openai/xai") {
		return c.buildXaiTransformationPlan(ctx, interfaceType, endpoint, req)
	}
	if strings.EqualFold(strings.TrimSpace(interfaceType), "chat") && strings.EqualFold(strings.TrimSpace(endpoint.Transformer), "kiro/claude") {
		return c.buildKiroChatTransformationPlan(ctx, endpoint, req)
	}
	return c.buildStandardTransformationPlan(ctx, interfaceType, endpoint, req)
}

func (c *ExecutionContext) buildStandardTransformationPlan(ctx context.Context, interfaceType string, endpoint *EndpointConfig, req *ForwardRequest) (*TransformationPlan, *ForwardResult) {
	tr, err := transformer.Get(interfaceType, endpoint.Transformer)
	if err != nil {
		c.DebugLog(ctx, 3, fmt.Sprintf("[Transformer] 解析失败: interfaceType=%s transformer=%q err=%v", interfaceType, endpoint.Transformer, err))
		return nil, &ForwardResult{StatusCode: http.StatusBadRequest, Error: err}
	}
	if tr == nil {
		err = fmt.Errorf("nil transformer: interfaceType=%s transformer=%q", interfaceType, endpoint.Transformer)
		c.DebugLog(ctx, 3, fmt.Sprintf("[Transformer] 解析失败: %v", err))
		return nil, &ForwardResult{StatusCode: http.StatusBadRequest, Error: err}
	}

	originalBody := req.Body
	requestModel := extractModelFromBody(originalBody)
	upstreamModel := ResolveUpstreamModel(requestModel, endpoint)

	targetPath := tr.TargetPath(req.IsStreaming, upstreamModel)
	if strings.TrimSpace(targetPath) == "" {
		err = fmt.Errorf("empty transformer target path: transformer=%q", endpoint.Transformer)
		c.DebugLog(ctx, 3, fmt.Sprintf("[Transformer] 目标路径为空: endpoint=%s transformer=%q", endpoint.Name, endpoint.Transformer))
		return nil, &ForwardResult{StatusCode: http.StatusBadRequest, Error: err}
	}

	transformedBody, err := tr.TransformRequest(upstreamModel, originalBody, req.IsStreaming)
	if err != nil {
		c.DebugLog(ctx, 3, fmt.Sprintf("[Transformer] 请求转换失败: endpoint=%s transformer=%q err=%v", endpoint.Name, endpoint.Transformer, err))
		return nil, &ForwardResult{StatusCode: http.StatusBadRequest, Error: err}
	}

	normalizedRawQuery := normalizeTransformerRawQuery(tr.TargetInterfaceType(), targetPath, req.RawQuery)
	metadata := map[string]any{
		"request_model":  requestModel,
		"upstream_model": upstreamModel,
	}
	if shouldNormalizeClaudeMessagesRequest(tr.TargetInterfaceType(), targetPath) {
		normalizedBody, targetHeaders, rawQuery := appmiddleware.NormalizeClaudeMessagesRequestForEndpoint(transformedBody, req.Headers, normalizedRawQuery, endpoint)
		transformedBody = normalizedBody
		normalizedRawQuery = rawQuery
		metadata["target_headers"] = targetHeaders
		metadata["claude_messages_oauth_tools"] = strings.EqualFold(appmiddleware.ResolveClaudeMessagesAuthModeForEndpoint(endpoint), "oauth")
	}

	if debugLogger := DebugLoggerFromContext(ctx); debugLogger != nil {
		debugLogger.SetSection("TransformedRequest", string(transformedBody))
	}

	plan := &TransformationPlan{
		Transformer:         tr,
		TargetInterfaceType: tr.TargetInterfaceType(),
		TargetPath:          targetPath,
		RawQuery:            normalizedRawQuery,
		RequestBody:         transformedBody,
		IsStreaming:         req.IsStreaming,
		OutputContentType:   tr.OutputContentType(req.IsStreaming),
		StreamInputMode:     streamInputModeForTransformer(tr),
		Context: &TransformContext{
			OriginalRequestBody:    append([]byte(nil), originalBody...),
			TransformedRequestBody: append([]byte(nil), transformedBody...),
			Metadata:               metadata,
		},
	}
	return plan, nil
}

func (c *ExecutionContext) buildKiroChatTransformationPlan(ctx context.Context, endpoint *EndpointConfig, req *ForwardRequest) (*TransformationPlan, *ForwardResult) {
	tr := chat_anthropic.Transformer{}

	originalBody := req.Body
	requestModel := extractModelFromBody(originalBody)
	upstreamModel := ResolveUpstreamModel(requestModel, endpoint)

	targetPath := tr.TargetPath(req.IsStreaming, upstreamModel)
	if strings.TrimSpace(targetPath) == "" {
		err := fmt.Errorf("empty transformer target path: transformer=%q", endpoint.Transformer)
		c.DebugLog(ctx, 3, fmt.Sprintf("[Transformer] Kiro chat target path empty: endpoint=%s transformer=%q", endpoint.Name, endpoint.Transformer))
		return nil, &ForwardResult{StatusCode: http.StatusBadRequest, Error: err}
	}

	transformedBody, err := tr.TransformRequest(upstreamModel, originalBody, req.IsStreaming)
	if err != nil {
		c.DebugLog(ctx, 3, fmt.Sprintf("[Transformer] Kiro chat request conversion failed: endpoint=%s transformer=%q err=%v", endpoint.Name, endpoint.Transformer, err))
		return nil, &ForwardResult{StatusCode: http.StatusBadRequest, Error: err}
	}

	normalizedRawQuery := normalizeTransformerRawQuery(tr.TargetInterfaceType(), targetPath, req.RawQuery)
	metadata := map[string]any{
		"request_model":                      requestModel,
		"upstream_model":                     upstreamModel,
		"source_type":                        "chat",
		"chat_conversion":                    true,
		"response_transform_on_success_only": true,
	}
	if shouldNormalizeClaudeMessagesRequest(tr.TargetInterfaceType(), targetPath) {
		normalizedBody, targetHeaders, rawQuery := appmiddleware.NormalizeClaudeMessagesRequestForEndpoint(transformedBody, req.Headers, normalizedRawQuery, endpoint)
		transformedBody = normalizedBody
		normalizedRawQuery = rawQuery
		metadata["target_headers"] = targetHeaders
		metadata["claude_messages_oauth_tools"] = strings.EqualFold(appmiddleware.ResolveClaudeMessagesAuthModeForEndpoint(endpoint), "oauth")
	}

	if debugLogger := DebugLoggerFromContext(ctx); debugLogger != nil {
		debugLogger.SetSection("TransformedRequest", string(transformedBody))
	}

	plan := &TransformationPlan{
		Transformer:         tr,
		TargetInterfaceType: tr.TargetInterfaceType(),
		TargetPath:          targetPath,
		RawQuery:            normalizedRawQuery,
		RequestBody:         transformedBody,
		IsStreaming:         req.IsStreaming,
		OutputContentType:   tr.OutputContentType(req.IsStreaming),
		StreamInputMode:     streamInputModeForTransformer(tr),
		Context: &TransformContext{
			OriginalRequestBody:    append([]byte(nil), originalBody...),
			TransformedRequestBody: append([]byte(nil), transformedBody...),
			Metadata:               metadata,
		},
	}
	return plan, nil
}

func (c *ExecutionContext) buildCodexTransformationPlan(ctx context.Context, interfaceType string, endpoint *EndpointConfig, req *ForwardRequest) (*TransformationPlan, *ForwardResult) {
	if codexBackend.IsImagesPath(req.Path) {
		resolvedModel := ResolveUpstreamModel(extractModelFromBody(req.Body), endpoint)
		if debugLogger := DebugLoggerFromContext(ctx); debugLogger != nil {
			debugLogger.SetSection("TransformedRequest", string(req.Body))
		}
		return &TransformationPlan{
			TargetInterfaceType: "codex",
			TargetPath:          req.Path,
			RawQuery:            req.RawQuery,
			RequestBody:         append([]byte(nil), req.Body...),
			IsStreaming:         req.IsStreaming,
			OutputContentType:   outputContentTypeForCodex(false, req.IsStreaming),
			StreamInputMode:     StreamInputModeChunk,
			Context: &TransformContext{
				OriginalRequestBody:    append([]byte(nil), req.Body...),
				TransformedRequestBody: append([]byte(nil), req.Body...),
				Metadata: map[string]any{
					"request_model":  extractModelFromBody(req.Body),
					"upstream_model": codexBackend.BaseModelName(strings.TrimSpace(resolvedModel)),
					"source_type":    codexBackend.SourceOpenAIImage,
					"openai_images":  true,
				},
			},
		}, nil
	}

	userAgent := ""
	if req.Headers != nil {
		userAgent = req.Headers.Get("User-Agent")
	}

	isStreaming := normalizeStreamingModeForCodexPath(req.Path, req.IsStreaming)
	resolvedModel := ResolveUpstreamModel(extractModelFromBody(req.Body), endpoint)
	body := append([]byte(nil), req.Body...)
	if rewritten, changed := applyResolvedModelToBodyForCodex(body, resolvedModel); changed {
		body = rewritten
	}

	suffixModel := extractModelFromBody(body)
	if strings.TrimSpace(resolvedModel) != "" {
		suffixModel = strings.TrimSpace(resolvedModel)
	}
	if rewritten, changed := codexBackend.ApplySuffixThinking(body, suffixModel); changed {
		body = rewritten
	}

	var responseTransformer transformer.Transformer
	requestModel := extractModelFromBody(body)
	originalBody := append([]byte(nil), body...)

	if isChatCompletionsFormat(body) {
		requestModel = extractModelFromBody(body)
		if strings.TrimSpace(requestModel) == "" {
			requestModel = codexBackend.BaseModelName(strings.TrimSpace(resolvedModel))
		}

		tr := chat_responses.Transformer{}
		converted, err := tr.TransformRequest(requestModel, body, isStreaming)
		if err != nil {
			if debugLogger := DebugLoggerFromContext(ctx); debugLogger != nil {
				debugLogger.Log("Chat Completions 请求转换失败: %v", err)
			}
			return nil, buildCodexInvalidRequestError(fmt.Sprintf("chat completions conversion failed: %v", err), err)
		}
		if rewritten, changed := codexBackend.ApplySuffixThinking(converted, suffixModel); changed {
			converted = rewritten
		}
		normalizedBody, result := normalizeResponsesBodyForCodex(converted, req.Path, userAgent)
		if result != nil {
			return nil, result
		}
		body = normalizedBody
		responseTransformer = tr
	} else {
		normalizedBody, result := normalizeResponsesBodyForCodex(body, req.Path, userAgent)
		if result != nil {
			return nil, result
		}
		body = normalizedBody
		if strings.TrimSpace(requestModel) == "" {
			requestModel = extractModelFromBody(body)
		}
		if strings.TrimSpace(requestModel) == "" {
			requestModel = codexBackend.BaseModelName(strings.TrimSpace(resolvedModel))
		}
	}

	if debugLogger := DebugLoggerFromContext(ctx); debugLogger != nil {
		debugLogger.SetSection("TransformedRequest", string(body))
	}

	plan := &TransformationPlan{
		Transformer:         responseTransformer,
		TargetInterfaceType: "codex",
		TargetPath:          codexTargetPathForRequest(req.Path),
		RawQuery:            normalizeTransformerRawQuery("codex", codexTargetPathForRequest(req.Path), req.RawQuery),
		RequestBody:         body,
		IsStreaming:         isStreaming,
		OutputContentType:   outputContentTypeForCodex(responseTransformer != nil, isStreaming),
		StreamInputMode:     StreamInputModeLine,
		Context: &TransformContext{
			OriginalRequestBody:    originalBody,
			TransformedRequestBody: append([]byte(nil), body...),
			Metadata: map[string]any{
				"request_model":                      requestModel,
				"upstream_model":                     codexBackend.BaseModelName(strings.TrimSpace(resolvedModel)),
				"source_type":                        interfaceType,
				"chat_conversion":                    responseTransformer != nil,
				"response_transform_on_success_only": true,
			},
		},
	}
	return plan, nil
}

func buildCodexInvalidRequestError(message string, err error) *ForwardResult {
	errJSON, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"type":    "invalid_request_error",
			"message": message,
		},
	})
	return &ForwardResult{
		StatusCode: http.StatusBadRequest,
		Body:       errJSON,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Error:      err,
	}
}

func normalizeResponsesBodyForCodex(body []byte, requestPath, userAgent string) ([]byte, *ForwardResult) {
	processedBody, _, err := appmiddleware.NormalizeCodexResponsesRequest(body, requestPath, userAgent)
	if err != nil {
		if errors.Is(err, appmiddleware.ErrCompactStreamingNotSupported) {
			return nil, &ForwardResult{
				StatusCode: http.StatusBadRequest,
				Error:      err,
				Body:       appmiddleware.CompactStreamingErrorPayload(),
				Headers:    http.Header{"Content-Type": []string{"application/json"}},
			}
		}
		return body, nil
	}
	return processedBody, nil
}

func outputContentTypeForCodex(hasChatConversion, isStreaming bool) string {
	if isStreaming {
		return "text/event-stream"
	}
	if hasChatConversion {
		return "application/json"
	}
	return "application/json"
}

func codexTargetPathForRequest(path string) string {
	p := strings.ToLower(strings.TrimRight(strings.TrimSpace(path), "/"))
	if strings.HasSuffix(p, "/responses/compact") {
		return "/responses/compact"
	}
	return "/responses"
}

func normalizeStreamingModeForCodexPath(requestPath string, isStreaming bool) bool {
	if appmiddleware.IsCompactResponsesPath(requestPath) {
		return false
	}
	return isStreaming
}

func isChatCompletionsFormat(body []byte) bool {
	return gjson.GetBytes(body, "messages").Exists() && !gjson.GetBytes(body, "input").Exists()
}

func applyResolvedModelToBodyForCodex(body []byte, resolvedModel string) ([]byte, bool) {
	resolvedModel = codexBackend.BaseModelName(strings.TrimSpace(resolvedModel))

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, false
	}

	currentModel, _ := payload["model"].(string)
	targetModel := resolvedModel
	if targetModel == "" {
		targetModel = codexBackend.BaseModelName(currentModel)
	}
	if strings.TrimSpace(targetModel) == "" {
		return body, false
	}
	if strings.EqualFold(strings.TrimSpace(currentModel), targetModel) {
		return body, false
	}

	payload["model"] = targetModel
	updated, err := json.Marshal(payload)
	if err != nil {
		return body, false
	}
	return updated, true
}

func streamInputModeForTransformer(tr transformer.Transformer) StreamInputMode {
	if tr == nil {
		return StreamInputModeLine
	}
	if provider, ok := tr.(interface{ StreamInputMode() string }); ok {
		if strings.EqualFold(strings.TrimSpace(provider.StreamInputMode()), string(StreamInputModeChunk)) {
			return StreamInputModeChunk
		}
	}
	return StreamInputModeLine
}

func shouldNormalizeClaudeMessagesRequest(targetInterfaceType, targetPath string) bool {
	if !strings.EqualFold(strings.TrimSpace(targetInterfaceType), "claude") {
		return false
	}
	path := strings.ToLower(strings.TrimRight(strings.TrimSpace(targetPath), "/"))
	return strings.HasSuffix(path, "/v1/messages")
}

// buildXaiTransformationPlan 对齐 openai/codex，目标上游为 xAI Responses。
// 支持 claude / chat / codex(responses) → xAI；端点 Models/Routes 经 ResolveUpstreamModel 生效。
func (c *ExecutionContext) buildXaiTransformationPlan(ctx context.Context, interfaceType string, endpoint *EndpointConfig, req *ForwardRequest) (*TransformationPlan, *ForwardResult) {
	if xaiBackend.IsImagesPath(req.Path) || xaiBackend.IsVideosPath(req.Path) {
		resolvedModel := ResolveUpstreamModel(extractModelFromBody(req.Body), endpoint)
		if debugLogger := DebugLoggerFromContext(ctx); debugLogger != nil {
			debugLogger.SetSection("TransformedRequest", string(req.Body))
		}
		return &TransformationPlan{
			TargetInterfaceType: "xai",
			TargetPath:          req.Path,
			RawQuery:            req.RawQuery,
			RequestBody:         append([]byte(nil), req.Body...),
			IsStreaming:         req.IsStreaming,
			OutputContentType:   "application/json",
			StreamInputMode:     StreamInputModeChunk,
			Context: &TransformContext{
				OriginalRequestBody:    append([]byte(nil), req.Body...),
				TransformedRequestBody: append([]byte(nil), req.Body...),
				Metadata: map[string]any{
					"request_model":  extractModelFromBody(req.Body),
					"upstream_model": xaiBackend.BaseModelName(strings.TrimSpace(resolvedModel)),
					"source_type":    interfaceType,
				},
			},
		}, nil
	}

	isStreaming := req.IsStreaming
	if xaiBackend.IsCompactPath(req.Path) {
		isStreaming = false
	}
	resolvedModel := ResolveUpstreamModel(extractModelFromBody(req.Body), endpoint)
	body := append([]byte(nil), req.Body...)

	var responseTransformer transformer.Transformer
	requestModel := extractModelFromBody(body)
	originalBody := append([]byte(nil), body...)
	sourceType := strings.TrimSpace(interfaceType)
	enableReplay := false

	// Claude Messages → Responses（interfaceType=claude 优先，避免 messages 被 chat 误判）
	var originalClaudeBody []byte
	if strings.EqualFold(sourceType, "claude") || isClaudeMessagesFormat(body, sourceType) {
		requestModel = extractModelFromBody(body)
		if strings.TrimSpace(requestModel) == "" {
			requestModel = xaiBackend.BaseModelName(strings.TrimSpace(resolvedModel))
		}
		// 转换前保留 Claude 原文：session / metadata 在 Responses 中会丢失
		originalClaudeBody = append([]byte(nil), body...)
		tr := claude_responses.Transformer{}
		converted, err := tr.TransformRequest(requestModel, body, isStreaming)
		if err != nil {
			return nil, &ForwardResult{
				StatusCode: http.StatusBadRequest,
				Error:      err,
				Body: mustJSONBytes(map[string]any{
					"error": map[string]any{"type": "invalid_request_error", "message": fmt.Sprintf("claude messages conversion failed: %v", err)},
				}),
				Headers: http.Header{"Content-Type": []string{"application/json"}},
			}
		}
		body = converted
		responseTransformer = tr
		sourceType = "claude"
		enableReplay = true
	} else if isChatCompletionsFormat(body) {
		requestModel = extractModelFromBody(body)
		if strings.TrimSpace(requestModel) == "" {
			requestModel = xaiBackend.BaseModelName(strings.TrimSpace(resolvedModel))
		}
		tr := chat_responses.Transformer{}
		converted, err := tr.TransformRequest(requestModel, body, isStreaming)
		if err != nil {
			return nil, &ForwardResult{
				StatusCode: http.StatusBadRequest,
				Error:      err,
				Body: mustJSONBytes(map[string]any{
					"error": map[string]any{"type": "invalid_request_error", "message": fmt.Sprintf("chat completions conversion failed: %v", err)},
				}),
				Headers: http.Header{"Content-Type": []string{"application/json"}},
			}
		}
		body = converted
		responseTransformer = tr
		if sourceType == "" {
			sourceType = "chat"
		}
	} else if strings.TrimSpace(requestModel) == "" {
		requestModel = xaiBackend.BaseModelName(strings.TrimSpace(resolvedModel))
	}
	if sourceType == "" {
		sourceType = "codex"
	}

	suffixModel := requestModel
	if strings.TrimSpace(resolvedModel) != "" {
		suffixModel = strings.TrimSpace(resolvedModel)
	}
	sessionID := ""
	if req.Headers != nil {
		sessionID = strings.TrimSpace(req.Headers.Get("x-grok-conv-id"))
	}
	// Claude session 必须用原始 Messages body（metadata.user_id / X-Claude-Code-Session-Id）
	replayKey := xaiBackend.ResolveReplaySessionKeyWithClaude(body, originalClaudeBody, req.Headers, sessionID)
	if replayKey == "" && enableReplay {
		// 仍无 session 时不写 cache key，避免跨会话串扰
		enableReplay = false
	}
	prepared, err := xaiBackend.PrepareResponsesBody(body, xaiBackend.PrepareOptions{
		Stream:           isStreaming,
		Model:            suffixModel,
		SessionID:        sessionID,
		IsCompact:        xaiBackend.IsCompactPath(req.Path),
		EnableReplay:     enableReplay,
		ReplaySessionKey: replayKey,
	})
	if err != nil {
		return nil, &ForwardResult{
			StatusCode: http.StatusBadRequest,
			Error:      err,
			Body: mustJSONBytes(map[string]any{
				"error": map[string]any{"type": "invalid_request_error", "message": err.Error()},
			}),
			Headers: http.Header{"Content-Type": []string{"application/json"}},
		}
	}
	body = prepared.Body
	upstreamModel := prepared.BaseModel
	if upstreamModel == "" {
		upstreamModel = xaiBackend.BaseModelName(suffixModel)
	}

	if debugLogger := DebugLoggerFromContext(ctx); debugLogger != nil {
		debugLogger.SetSection("TransformedRequest", string(body))
	}

	targetPath := "/xai/v1/responses"
	if xaiBackend.IsCompactPath(req.Path) {
		targetPath = "/xai/v1/responses/compact"
	}

	plan := &TransformationPlan{
		Transformer:         responseTransformer,
		TargetInterfaceType: "xai",
		TargetPath:          targetPath,
		RawQuery:            req.RawQuery,
		RequestBody:         body,
		IsStreaming:         isStreaming,
		OutputContentType:   outputContentTypeForCodex(responseTransformer != nil, isStreaming),
		StreamInputMode:     StreamInputModeLine,
		Context: &TransformContext{
			OriginalRequestBody:    originalBody,
			TransformedRequestBody: append([]byte(nil), body...),
			Metadata: map[string]any{
				"request_model":                      requestModel,
				"upstream_model":                     upstreamModel,
				"source_type":                        sourceType,
				"chat_conversion":                    responseTransformer != nil,
				"response_transform_on_success_only": true,
				"xai_session_id":                     prepared.SessionID,
				"enable_xai_replay":                  enableReplay,
				"xai_replay_session":                 prepared.ReplayScope.SessionKey,
			},
		},
	}
	return plan, nil
}

// isClaudeMessagesFormat 区分 Anthropic messages 与 OpenAI chat completions。
func isClaudeMessagesFormat(body []byte, interfaceType string) bool {
	if strings.EqualFold(strings.TrimSpace(interfaceType), "claude") {
		return gjson.GetBytes(body, "messages").Exists()
	}
	// Anthropic 特征：input_schema 工具、system 为数组、anthropic-version 不在 body
	if !gjson.GetBytes(body, "messages").Exists() || gjson.GetBytes(body, "input").Exists() {
		return false
	}
	// tools[].input_schema 是 Claude 形态；chat 用 parameters
	tools := gjson.GetBytes(body, "tools")
	if tools.IsArray() {
		for _, t := range tools.Array() {
			if t.Get("input_schema").Exists() {
				return true
			}
		}
	}
	// system 为 content block 数组
	if sys := gjson.GetBytes(body, "system"); sys.IsArray() {
		return true
	}
	// max_tokens 存在且无 stream_options（弱启发）
	if gjson.GetBytes(body, "max_tokens").Exists() && !gjson.GetBytes(body, "max_completion_tokens").Exists() {
		// 进一步：messages[].content 为数组块
		for _, m := range gjson.GetBytes(body, "messages").Array() {
			if m.Get("content").IsArray() {
				return true
			}
		}
	}
	return false
}

func mustJSONBytes(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
