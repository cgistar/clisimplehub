package codexplugin

import (
	"encoding/json"
	"net/http"

	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/executor"
	"clisimplehub/internal/usage"
)

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

// extractTokensFromBody extracts token usage from a non-streaming response body.
func extractTokensFromBody(body []byte) *executor.TokenUsage {
	u := usage.ExtractFromResponse(body)
	if u == nil {
		return nil
	}
	return &executor.TokenUsage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		CachedCreate: u.CachedCreate,
		CachedRead:   u.CachedRead,
		Reasoning:    u.Reasoning,
	}
}

// sanitizeHeaders removes sensitive headers for logging.
func sanitizeHeaders(headers http.Header) http.Header {
	sanitized := headers.Clone()
	sanitized.Del("Authorization")
	sanitized.Del("X-Api-Key")
	return sanitized
}
