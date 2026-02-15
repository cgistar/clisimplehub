package kiroplugin

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	kiroapi "clisimplehub/internal/kiro"
	kiro_claude "clisimplehub/internal/kiro/claude"
	"clisimplehub/internal/executor"
)

// tryWebSearchShortCircuit checks if the request is a web_search-only request
// and routes it to the Kiro MCP API instead of /generateAssistantResponse.
func (s *KiroService) tryWebSearchShortCircuit(
	ctx context.Context,
	w http.ResponseWriter,
	tr *kiro_claude.Transformer,
	requestModel string,
	originalBody []byte,
	isStreaming bool,
) *executor.ForwardResult {
	modelFromReq, query, ok := kiro_claude.ParseClaudeWebSearchOnlyRequest(originalBody)
	if !ok {
		return nil
	}

	result := &executor.ForwardResult{}

	model := strings.TrimSpace(modelFromReq)
	if model == "" {
		model = strings.TrimSpace(requestModel)
	}
	if model == "" {
		model = "claude-sonnet-4"
	}

	inputTokens := kiro_claude.EstimateClaudeInputTokens(originalBody)
	if inputTokens < 1 {
		inputTokens = 1
	}

	var src kiroapi.KiroAuthSource = tr
	if src == nil {
		result.StatusCode = http.StatusInternalServerError
		result.Error = fmt.Errorf("kiro auth source not available")
		return result
	}

	region := tr.GetRegion()
	mcpURL := kiroapi.KiroMCPURL(region)
	result.TargetURL = mcpURL

	toolUseID, mcpBody, err := kiro_claude.BuildWebSearchMcpRequest(query)
	if err != nil {
		result.StatusCode = http.StatusBadRequest
		result.Error = err
		return result
	}

	buildMcpReq := func() (*http.Request, error) {
		proxyReq, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, bytes.NewReader(mcpBody))
		if err != nil {
			return nil, err
		}
		authApplier := kiroapi.NewAuthApplier(src)
		if err := authApplier.Apply(proxyReq); err != nil {
			return nil, err
		}
		return proxyReq, nil
	}

	proxyReq, err := buildMcpReq()
	if err != nil {
		result.StatusCode = http.StatusInternalServerError
		result.Error = fmt.Errorf("build mcp request failed: %w", err)
		return result
	}

	client := executor.NewHTTPClientForcedProxyURL(tr.KiroProxyURL(), 0)
	resp, err := client.Do(proxyReq)
	if err != nil {
		return s.writeWebSearchResponse(w, isStreaming, result, model, query, toolUseID, nil, inputTokens, 0)
	}

	// handle 401/403: best-effort refresh and retry once; failover if refresh fails
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		refreshOK := false
		if tr.ForceRefreshKiroToken() == nil {
			refreshOK = true
			time.Sleep(50 * time.Millisecond)
			retryReq, buildErr := buildMcpReq()
			if buildErr != nil {
				return s.writeWebSearchResponse(w, isStreaming, result, model, query, toolUseID, nil, inputTokens, 0)
			}
			proxyReq = retryReq
			resp, err = client.Do(retryReq)
		}
		if !refreshOK {
			handleKiroErrorStatus(resp.StatusCode, nil, tr)
			if out := s.tryFailoverRetry(ctx, tr, originalBody, requestModel, isStreaming, w); out != nil {
				return out
			}
			return s.writeWebSearchResponse(w, isStreaming, result, model, query, toolUseID, nil, inputTokens, 0)
		}
		if err != nil {
			return s.writeWebSearchResponse(w, isStreaming, result, model, query, toolUseID, nil, inputTokens, 0)
		}
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if readErr != nil {
		return s.writeWebSearchResponse(w, isStreaming, result, model, query, toolUseID, nil, inputTokens, 0)
	}

	// Failover for 402/403 errors
	if resp.StatusCode == 402 || resp.StatusCode == 403 {
		_, canFailover := handleKiroErrorStatus(resp.StatusCode, body, tr)
		if canFailover {
			if out := s.tryFailoverRetry(ctx, tr, originalBody, requestModel, isStreaming, w); out != nil {
				return out
			}
		}
	}

	var results *kiro_claude.WebSearchResults
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		if parsed, err := kiro_claude.ParseKiroMcpWebSearchResults(body); err == nil {
			results = parsed
		}
	}

	return s.writeWebSearchResponse(w, isStreaming, result, model, query, toolUseID, results, inputTokens, 0)
}

func (s *KiroService) writeWebSearchResponse(
	w http.ResponseWriter,
	isStreaming bool,
	result *executor.ForwardResult,
	model string,
	query string,
	toolUseID string,
	results *kiro_claude.WebSearchResults,
	inputTokens int,
	outputTokens int,
) *executor.ForwardResult {
	if w == nil || result == nil {
		return result
	}

	result.StatusCode = http.StatusOK

	if isStreaming {
		events, outTokens := kiro_claude.BuildWebSearchSSEEvents(model, query, toolUseID, results, inputTokens)
		if outTokens > 0 {
			outputTokens = outTokens
		}
		var capture strings.Builder
		for _, e := range events {
			capture.WriteString(e)
		}
		result.ResponseStream = capture.String()
		result.Streamed = true
		result.Tokens = &executor.TokenUsage{InputTokens: int64(inputTokens), OutputTokens: int64(outputTokens)}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		for _, e := range events {
			_, _ = io.WriteString(w, e)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if flusher != nil {
			flusher.Flush()
		}
		return result
	}

	body, outTokens, err := kiro_claude.BuildWebSearchNonStreamMessage(model, query, toolUseID, results, inputTokens)
	if err != nil {
		result.StatusCode = http.StatusInternalServerError
		result.Error = err
		return result
	}
	if outTokens > 0 {
		outputTokens = outTokens
	}
	result.Body = body
	result.Headers = http.Header{"Content-Type": {"application/json"}}
	result.Tokens = &executor.TokenUsage{InputTokens: int64(inputTokens), OutputTokens: int64(outputTokens)}
	return result
}
