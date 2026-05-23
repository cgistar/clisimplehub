package backend

import (
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func NewStatusError(statusCode int, body []byte, now time.Time) StatusError {
	errCode := statusCode
	if isModelCapacityError(body) {
		errCode = http.StatusTooManyRequests
	}
	body = classifyStatusError(errCode, body)
	err := StatusError{Code: errCode, Body: body}
	if retryAfter := parseRetryAfter(errCode, body, now); retryAfter != nil {
		err.RetryAfter = retryAfter
	}
	return err
}

func classifyStatusError(statusCode int, body []byte) []byte {
	code, errType, ok := statusErrorClassification(statusCode, body)
	if !ok {
		return body
	}
	message := gjson.GetBytes(body, "error.message").String()
	if message == "" {
		message = gjson.GetBytes(body, "message").String()
	}
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	if message == "" {
		message = http.StatusText(statusCode)
	}
	out := []byte(`{"error":{}}`)
	out, _ = sjson.SetBytes(out, "error.message", message)
	out, _ = sjson.SetBytes(out, "error.type", errType)
	out, _ = sjson.SetBytes(out, "error.code", code)
	return out
}

func statusErrorClassification(statusCode int, body []byte) (code string, errType string, ok bool) {
	errorMessage := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "error.message").String()))
	if errorMessage == "" {
		errorMessage = strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "message").String()))
	}
	lower := strings.ToLower(strings.TrimSpace(string(body)))
	upstreamCode := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "error.code").String()))
	upstreamType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "error.type").String()))
	isInvalidRequest := upstreamType == "" || upstreamType == "invalid_request_error"

	switch {
	case statusCode == http.StatusRequestEntityTooLarge ||
		upstreamCode == "context_length_exceeded" ||
		upstreamCode == "context_too_large" ||
		isInvalidRequest && (strings.Contains(errorMessage, "context length") ||
			strings.Contains(errorMessage, "context_length") ||
			strings.Contains(errorMessage, "maximum context") ||
			strings.Contains(errorMessage, "too many tokens")):
		return "context_too_large", "invalid_request_error", true
	case strings.Contains(lower, "invalid signature in thinking block") || strings.Contains(lower, "invalid_encrypted_content"):
		return "thinking_signature_invalid", "invalid_request_error", true
	case upstreamCode == "previous_response_not_found" ||
		strings.Contains(lower, "previous_response_not_found") ||
		strings.Contains(lower, "previous_response_id") && strings.Contains(lower, "not found"):
		return "previous_response_not_found", "invalid_request_error", true
	case statusCode == http.StatusUnauthorized ||
		upstreamType == "authentication_error" ||
		upstreamCode == "invalid_api_key" ||
		strings.Contains(lower, "invalid or expired token") ||
		strings.Contains(lower, "refresh_token_reused"):
		return "auth_unavailable", "authentication_error", true
	default:
		return "", "", false
	}
}

func isModelCapacityError(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	for _, candidate := range []string{
		gjson.GetBytes(body, "error.message").String(),
		gjson.GetBytes(body, "message").String(),
		string(body),
	} {
		lower := strings.ToLower(strings.TrimSpace(candidate))
		if lower == "" {
			continue
		}
		if strings.Contains(lower, "selected model is at capacity") ||
			strings.Contains(lower, "model is at capacity. please try a different model") {
			return true
		}
	}
	return false
}

func parseRetryAfter(statusCode int, body []byte, now time.Time) *time.Duration {
	if statusCode != http.StatusTooManyRequests || len(body) == 0 {
		return nil
	}
	if strings.TrimSpace(gjson.GetBytes(body, "error.type").String()) != "usage_limit_reached" {
		return nil
	}
	if resetsAt := gjson.GetBytes(body, "error.resets_at").Int(); resetsAt > 0 {
		resetAtTime := time.Unix(resetsAt, 0)
		if resetAtTime.After(now) {
			retryAfter := resetAtTime.Sub(now)
			return &retryAfter
		}
	}
	if resetsInSeconds := gjson.GetBytes(body, "error.resets_in_seconds").Int(); resetsInSeconds > 0 {
		retryAfter := time.Duration(resetsInSeconds) * time.Second
		return &retryAfter
	}
	return nil
}
