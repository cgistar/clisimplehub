package xaiplugin

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"clisimplehub/internal/executor"
	"clisimplehub/internal/logger"

	"github.com/tidwall/gjson"
)

// HandleChatCompletions OpenAI Chat Completions → Responses → xAI 池。
func (s *XaiService) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	isStreaming := gjson.GetBytes(body, "stream").Bool() ||
		gjson.GetBytes(body, "stream").String() == "true" ||
		strings.Contains(r.Header.Get("Accept"), "text/event-stream")

	requestID := executor.RequestIDFromContext(r.Context())
	if requestID == "" {
		requestID = fmt.Sprintf("xai-chat-%d", time.Now().UnixNano())
	}

	var debugLogger *logger.RequestDebugLogger
	if logger.IsDebugFileModeEnabled() {
		debugLogger = logger.NewRequestDebugLogger(requestID)
		debugLogger.SetMetadata("Plugin", "xAI")
		debugLogger.SetMetadata("Path", r.URL.Path)
		debugLogger.SetMetadata("Method", r.Method)
		debugLogger.SetMetadata("Streaming", fmt.Sprintf("%v", isStreaming))
		debugLogger.SetMetadata("ChatConversion", "true")
		debugLogger.SetOriginalHeader(r.Header)
		debugLogger.SetSection("OriginalRequest", string(body))
		defer func() { _ = debugLogger.Flush() }()
	}

	ctx := r.Context()
	if debugLogger != nil {
		ctx = executor.WithDebugLogger(ctx, debugLogger)
	}

	execCtx := executor.NewExecutionContext(nil)
	endpoint := &executor.EndpointConfig{
		Name:          "xAI Chat Direct",
		InterfaceType: "chat",
		Transformer:   "openai/xai",
	}
	forwardReq := &executor.ForwardRequest{
		Method:       r.Method,
		Path:         r.URL.Path,
		RawQuery:     r.URL.RawQuery,
		Headers:      r.Header.Clone(),
		Body:         body,
		IsStreaming:  isStreaming,
		RequestModel: extractModelFromBody(body),
	}

	plan, prepErr := execCtx.BuildTransformationPlan(ctx, "chat", endpoint, forwardReq)
	if prepErr != nil {
		writeExecutorResult(w, prepErr)
		return
	}

	result := execCtx.FinalizeTransformation(ctx, w, plan, s.RoundTrip(ctx, &executor.UpstreamRequest{
		Method:              forwardReq.Method,
		TargetPath:          plan.TargetPath,
		RawQuery:            plan.RawQuery,
		Headers:             forwardReq.Headers,
		Body:                plan.RequestBody,
		IsStreaming:         plan.IsStreaming,
		RequestModel:        forwardReq.RequestModel,
		OriginalPath:        forwardReq.Path,
		TargetInterfaceType: plan.TargetInterfaceType,
		Endpoint:            endpoint,
		Transformer:         plan.Transformer,
		TransformContext:    plan.Context,
	}))

	_ = time.Since(startTime)
	if result.Streamed {
		return
	}
	writeExecutorResult(w, result)
}

func writeExecutorResult(w http.ResponseWriter, result *executor.ForwardResult) {
	if result == nil {
		http.Error(w, `{"error":"xai request failed"}`, http.StatusBadGateway)
		return
	}
	for key, values := range result.Headers {
		if strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	status := result.StatusCode
	if status <= 0 {
		status = http.StatusBadGateway
	}
	if len(result.Body) > 0 && w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	if len(result.Body) > 0 {
		_, _ = w.Write(result.Body)
	}
}
