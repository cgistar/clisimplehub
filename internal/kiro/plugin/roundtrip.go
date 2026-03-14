package kiroplugin

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"clisimplehub/internal/executor"
)

func (s *KiroService) RoundTrip(ctx context.Context, req *executor.UpstreamRequest) *executor.UpstreamRoundTripResult {
	if req == nil {
		return &executor.UpstreamRoundTripResult{
			StatusCode: 0,
			Error:      io.ErrUnexpectedEOF,
		}
	}

	recorder := httptest.NewRecorder()
	forwardBody, forwardModel := forwardInput(req)
	result := s.Forward(ctx, forwardBody, forwardModel, req.IsStreaming, recorder, req.OriginalPath)
	if result == nil {
		return &executor.UpstreamRoundTripResult{
			StatusCode: 0,
			Error:      io.ErrUnexpectedEOF,
		}
	}

	if req.TransformContext != nil && shouldSkipOuterResponseTransform(req) {
		if req.TransformContext.Metadata == nil {
			req.TransformContext.Metadata = make(map[string]any)
		}
		req.TransformContext.Metadata["skip_response_transform"] = true
	}

	roundTrip := &executor.UpstreamRoundTripResult{
		StatusCode:    result.StatusCode,
		Headers:       selectRoundTripHeaders(result, recorder, false),
		Body:          append([]byte(nil), result.Body...),
		TargetURL:     result.TargetURL,
		TargetHeaders: cloneStringMap(result.TargetHeaders),
		Tokens:        result.Tokens,
		Error:         result.Error,
	}
	if result.Streamed {
		roundTrip.Headers = selectRoundTripHeaders(result, recorder, true)
		roundTrip.Stream = io.NopCloser(bytes.NewReader([]byte(result.ResponseStream)))
		roundTrip.Body = nil
	}
	return roundTrip
}

func forwardInput(req *executor.UpstreamRequest) ([]byte, string) {
	if req == nil {
		return nil, ""
	}

	if isChatConversion(req) {
		body := append([]byte(nil), req.Body...)
		model := strings.TrimSpace(req.RequestModel)
		if model == "" && req.TransformContext != nil && req.TransformContext.Metadata != nil {
			if upstreamModel, _ := req.TransformContext.Metadata["upstream_model"].(string); strings.TrimSpace(upstreamModel) != "" {
				model = strings.TrimSpace(upstreamModel)
			}
		}
		return body, model
	}

	body := append([]byte(nil), req.Body...)
	model := strings.TrimSpace(req.RequestModel)
	if req.TransformContext == nil {
		return body, model
	}

	if len(req.TransformContext.OriginalRequestBody) > 0 {
		body = append([]byte(nil), req.TransformContext.OriginalRequestBody...)
	}
	if req.TransformContext.Metadata == nil {
		return body, model
	}
	if upstreamModel, _ := req.TransformContext.Metadata["upstream_model"].(string); strings.TrimSpace(upstreamModel) != "" {
		model = strings.TrimSpace(upstreamModel)
	} else if requestModel, _ := req.TransformContext.Metadata["request_model"].(string); strings.TrimSpace(requestModel) != "" {
		model = strings.TrimSpace(requestModel)
	}
	return body, model
}

func isChatConversion(req *executor.UpstreamRequest) bool {
	if req == nil || req.TransformContext == nil || req.TransformContext.Metadata == nil {
		return false
	}
	if chatConversion, _ := req.TransformContext.Metadata["chat_conversion"].(bool); chatConversion {
		return true
	}
	if sourceType, _ := req.TransformContext.Metadata["source_type"].(string); strings.EqualFold(strings.TrimSpace(sourceType), "chat") {
		return true
	}
	return false
}

func shouldSkipOuterResponseTransform(req *executor.UpstreamRequest) bool {
	return !isChatConversion(req)
}

func selectRoundTripHeaders(result *executor.ForwardResult, recorder *httptest.ResponseRecorder, streamed bool) http.Header {
	if result != nil && len(result.Headers) > 0 {
		return result.Headers.Clone()
	}
	if recorder == nil {
		return http.Header{}
	}
	if streamed {
		return recorder.Result().Header.Clone()
	}
	return recorder.Header().Clone()
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
