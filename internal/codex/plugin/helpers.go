package codexplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/executor"
	"clisimplehub/internal/usage"
)

const statusClientClosedRequest = 499

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// buildInternalError constructs a structured error response for internal errors.
func buildInternalError(err error) *executor.ForwardResult {
	message := "Internal server error"
	if err != nil {
		message = err.Error()
	}

	errJSON, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"type":    "internal_error",
			"message": message,
		},
	})

	return &executor.ForwardResult{
		StatusCode: http.StatusInternalServerError,
		Body:       errJSON,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Error:      err,
	}
}

// buildNoAccountsError constructs a structured error response when no accounts are available.
func buildNoAccountsError(mode string) (statusCode int, body []byte) {
	var message string
	switch mode {
	case codexShared.RotationFixed:
		message = "No available Codex accounts in fixed mode. The active account may be banned, exhausted, or cooling down."
	case codexShared.RotationFailover:
		message = "No available Codex accounts in failover mode. All accounts may be banned, exhausted, or cooling down."
	case codexShared.RotationLoadBalance:
		message = "No available Codex accounts in load balance mode. All accounts may be banned, exhausted, or cooling down."
	default:
		message = "No available Codex accounts."
	}

	errJSON, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"type":    "no_available_accounts",
			"message": message,
			"code":    "codex_account_unavailable",
			"mode":    mode,
		},
	})
	return http.StatusServiceUnavailable, errJSON
}

// buildAllFailedError constructs a structured error response when all accounts failed.
func buildAllFailedError(lastErr error) (statusCode int, body []byte) {
	message := "All Codex accounts failed"
	if lastErr != nil {
		message = lastErr.Error()
	}

	errJSON, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"type":    "all_accounts_failed",
			"message": message,
			"code":    "codex_all_failed",
		},
	})
	return http.StatusBadGateway, errJSON
}

func buildGatewayErrorPayload(errorType, message string, details map[string]any) []byte {
	if details == nil {
		details = map[string]any{}
	}

	errorPayload := map[string]any{
		"type":    errorType,
		"message": message,
	}
	if len(details) > 0 {
		errorPayload["details"] = details
	}

	payload := map[string]any{"error": errorPayload}
	errJSON, _ := json.Marshal(payload)
	return errJSON
}

// buildGatewayError constructs a structured error response for upstream/network/auth transient errors.
func buildGatewayError(errorType, message string, details map[string]any) *forwardResult {
	errJSON := buildGatewayErrorPayload(errorType, message, details)
	return &forwardResult{
		statusCode: http.StatusBadGateway,
		body:       errJSON,
		headers:    http.Header{"Content-Type": []string{"application/json"}},
		errMsg:     message,
	}
}

// buildGatewayExecutorError constructs a structured executor error response for upstream/network/auth transient errors.
func buildGatewayExecutorError(errorType, message string, details map[string]any, err error, targetURL string) *executor.ForwardResult {
	errJSON := buildGatewayErrorPayload(errorType, message, details)
	return &executor.ForwardResult{
		StatusCode: http.StatusBadGateway,
		Body:       errJSON,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Error:      err,
		TargetURL:  targetURL,
	}
}

func buildCancelledForwardError(err error) *forwardResult {
	message := "Request cancelled by client"
	if err != nil {
		message = fmt.Sprintf("%s: %v", message, err)
	}

	return &forwardResult{
		statusCode: statusClientClosedRequest,
		body:       buildGatewayErrorPayload("request_cancelled", message, nil),
		headers:    http.Header{"Content-Type": []string{"application/json"}},
		errMsg:     message,
	}
}

func buildCancelledExecutorError(err error) *executor.ForwardResult {
	message := "Request cancelled by client"
	if err != nil {
		message = fmt.Sprintf("%s: %v", message, err)
	}

	return &executor.ForwardResult{
		StatusCode: statusClientClosedRequest,
		Body:       buildGatewayErrorPayload("request_cancelled", message, nil),
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Error:      err,
	}
}

// extractTokensFromBody extracts token usage from a non-streaming response body.
func extractTokensFromBody(body []byte) *executor.TokenUsage {
	u := usage.ExtractFromResponse(body)
	if u == nil {
		return nil
	}
	return &executor.TokenUsage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		TotalTokens:  u.Total(),
		CachedCreate: u.CachedCreate,
		CachedRead:   u.CachedRead,
		Reasoning:    u.Reasoning,
	}
}

func tokenUsageTotal(tokens *executor.TokenUsage) int64 {
	if tokens == nil {
		return 0
	}
	if tokens.TotalTokens > 0 {
		return tokens.TotalTokens
	}
	return tokens.InputTokens + tokens.OutputTokens
}

// sanitizeHeaders removes sensitive headers for logging.
func sanitizeHeaders(headers http.Header) http.Header {
	sanitized := headers.Clone()
	sanitized.Del("Authorization")
	sanitized.Del("X-Api-Key")
	return sanitized
}
