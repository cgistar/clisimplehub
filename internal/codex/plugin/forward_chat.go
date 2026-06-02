package codexplugin

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

func (s *CodexService) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
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
		requestID = fmt.Sprintf("codex-chat-%d", time.Now().UnixNano())
	}

	var debugLogger *logger.RequestDebugLogger
	if logger.IsDebugFileModeEnabled() {
		debugLogger = logger.NewRequestDebugLogger(requestID)
		debugLogger.SetMetadata("Plugin", "Codex")
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
		Name:          "Codex Chat Direct",
		InterfaceType: "chat",
		Transformer:   "openai/codex",
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
		logCodexRequestToConsole(requestID, r.Method, r.URL.Path, nil, prepErr.StatusCode, errorStatus(prepErr), time.Since(startTime).Milliseconds())
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

	runTime := time.Since(startTime).Milliseconds()
	logCodexRequestToConsole(requestID, r.Method, r.URL.Path, nil, result.StatusCode, errorStatus(result), runTime)

	if result.Streamed {
		return
	}
	writeExecutorResult(w, result)
}

func errorStatus(result *executor.ForwardResult) string {
	if result == nil {
		return "error"
	}
	if result.Error != nil {
		return result.Error.Error()
	}
	if result.StatusCode == http.StatusOK {
		return "success"
	}
	return fmt.Sprintf("upstream_status_%d", result.StatusCode)
}
