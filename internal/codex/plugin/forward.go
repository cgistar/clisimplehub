package codexplugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	codex "clisimplehub/internal/codex"
	codexAuth "clisimplehub/internal/codex/auth"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/executor"
	"clisimplehub/internal/plugin"
)

const maxRetryAccounts = 5

func (s *CodexService) HandleResponses(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	isStreaming := strings.Contains(r.Header.Get("Accept"), "text/event-stream")
	clientHeaders := r.Header.Clone() // Preserve client headers for forwarding

	pool := codex.GetPool()
	if pool == nil {
		http.Error(w, `{"error":"codex pool not initialized"}`, http.StatusInternalServerError)
		return
	}

	var lastErr error
	for attempt := 0; attempt < maxRetryAccounts; attempt++ {
		account := pool.Select()
		if account == nil {
			break
		}

		result, retryable := s.forwardToUpstream(r.Context(), account, body, isStreaming, w, pool, clientHeaders)
		if result == nil {
			return // response already written (streaming)
		}
		if !retryable {
			writeResult(w, result)
			return
		}
		lastErr = fmt.Errorf("account %s: %s", maskToken(account.RefreshToken), result.errMsg)
	}

	if lastErr != nil {
		errJSON, _ := json.Marshal(map[string]string{"error": lastErr.Error()})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write(errJSON)
		return
	}
	http.Error(w, `{"error":"no available codex accounts"}`, http.StatusServiceUnavailable)
}

type forwardResult struct {
	statusCode int
	headers    http.Header
	body       []byte
	errMsg     string
}

func (s *CodexService) forwardToUpstream(ctx context.Context, account *codexShared.CodexAccount, body []byte, isStreaming bool, w http.ResponseWriter, pool *codex.CodexAccountPool, clientHeaders http.Header) (result *forwardResult, retryable bool) {
	configPath := pool.ConfigPath()

	// Load config for URL and headers
	// Only use defaults if config file doesn't exist; propagate other errors
	config, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			// Config file exists but is malformed/unreadable - this is a real error
			return &forwardResult{errMsg: fmt.Sprintf("config load failed: %v", err)}, false
		}
		// Config file doesn't exist - use defaults
		config = &codexShared.CodexMultiConfig{}
	}

	// Resolve proxy: global > account > plugin
	proxyURL := ""
	if gp := plugin.GetGlobalProxyProviderCached(); gp != nil {
		proxyURL = gp.GetGlobalProxyURL()
	}
	if proxyURL == "" {
		proxyURL = strings.TrimSpace(account.ProxyUrl)
	}
	if proxyURL == "" {
		proxyURL = pool.ProxyURL()
	}

	authMgr := s.GetOrCreateAuthManager(account.RefreshToken, configPath, proxyURL)
	accessToken, accountID, err := authMgr.GetAccessToken()
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "refresh_token_reused") {
			pool.MarkFailed(account.RefreshToken, codexShared.CodexStatusReused, 0, "refresh_token_reused")
		} else if strings.Contains(errStr, "invalid_grant") || strings.Contains(errStr, "HTTP 401") || strings.Contains(errStr, "HTTP 403") {
			pool.MarkFailed(account.RefreshToken, codexShared.CodexStatusBanned, 0, "auth_failed")
		} else {
			// Transient failure: short cooldown, not permanent ban
			pool.MarkFailed(account.RefreshToken, codexShared.CodexStatusValid, 2*time.Minute, "auth_transient")
		}
		return &forwardResult{errMsg: fmt.Sprintf("auth failed: %v", err)}, true
	}

	client := executor.NewHTTPClientForcedProxyURL(proxyURL, 0)

	upstreamURL := getCodexUpstreamURL(config)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(body))
	if err != nil {
		return &forwardResult{errMsg: err.Error()}, false
	}
	applyCodexHeaders(req, accessToken, accountID, isStreaming, config, clientHeaders)

	resp, err := client.Do(req)
	if err != nil {
		// Transport error: short cooldown to avoid reselecting same broken account
		pool.MarkFailed(account.RefreshToken, codexShared.CodexStatusValid, 30*time.Second, "transport_error")
		return &forwardResult{errMsg: fmt.Sprintf("upstream error: %v", err)}, true
	}
	defer resp.Body.Close()

	// Handle error status codes
	if resp.StatusCode == http.StatusUnauthorized {
		respBody, _ := io.ReadAll(resp.Body)
		// Try force refresh
		if refreshErr := authMgr.ForceRefresh(); refreshErr == nil {
			newToken, newAccountID, tokenErr := authMgr.GetAccessToken()
			if tokenErr == nil && newToken != "" {
				retryReq, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(body))
				if reqErr == nil {
					applyCodexHeaders(retryReq, newToken, newAccountID, isStreaming, config, clientHeaders)
					retryResp, retryErr := client.Do(retryReq)
					if retryErr == nil {
						defer retryResp.Body.Close()
						if retryResp.StatusCode == http.StatusOK {
							return s.handleSuccess(ctx, retryResp, isStreaming, w, pool, account)
						}
					}
				}
			}
		}
		pool.MarkFailed(account.RefreshToken, codexShared.CodexStatusBanned, 24*time.Hour, "unauthorized")
		return &forwardResult{statusCode: resp.StatusCode, body: respBody, errMsg: "unauthorized"}, true
	}

	if resp.StatusCode == http.StatusForbidden {
		respBody, _ := io.ReadAll(resp.Body)
		pool.MarkFailed(account.RefreshToken, codexShared.CodexStatusBanned, 24*time.Hour, "suspended")
		return &forwardResult{statusCode: resp.StatusCode, body: respBody, errMsg: "forbidden"}, true
	}

	if resp.StatusCode == http.StatusPaymentRequired {
		respBody, _ := io.ReadAll(resp.Body)
		pool.MarkFailed(account.RefreshToken, codexShared.CodexStatusExhausted, 0, "quota_exhausted")
		return &forwardResult{statusCode: resp.StatusCode, body: respBody, errMsg: "payment required"}, false
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		respBody, _ := io.ReadAll(resp.Body)
		cooldown := parseCooldownFromBody(respBody)
		if cooldown <= 0 {
			cooldown = parseCooldownDuration(resp)
		}
		if snapshot := extractCodexUsageHeaders(resp.Header); snapshot != nil {
			pool.UpdateUsageSnapshot(account.RefreshToken, snapshot)
		}
		pool.MarkFailed(account.RefreshToken, codexShared.CodexStatusValid, cooldown, "rate_limit")
		return &forwardResult{statusCode: resp.StatusCode, body: respBody, errMsg: "rate limited"}, true
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return &forwardResult{statusCode: resp.StatusCode, headers: resp.Header.Clone(), body: respBody}, false
	}

	pool.ReportSuccess(account.RefreshToken)
	return s.handleSuccess(ctx, resp, isStreaming, w, pool, account)
}

func (s *CodexService) handleSuccess(ctx context.Context, resp *http.Response, isStreaming bool, w http.ResponseWriter, pool *codex.CodexAccountPool, account *codexShared.CodexAccount) (result *forwardResult, retryable bool) {
	if snapshot := extractCodexUsageHeaders(resp.Header); snapshot != nil {
		pool.UpdateUsageSnapshot(account.RefreshToken, snapshot)
	}

	if isStreaming {
		s.streamResponse(ctx, resp, w)
		return nil, false // response already written
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return &forwardResult{errMsg: fmt.Sprintf("read response: %v", err)}, false
	}
	return &forwardResult{statusCode: resp.StatusCode, headers: resp.Header.Clone(), body: respBody}, false
}

func (s *CodexService) streamResponse(ctx context.Context, resp *http.Response, w http.ResponseWriter) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		respBody, _ := io.ReadAll(resp.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(respBody)
		return
	}

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		_, _ = w.Write(line)
		_, _ = w.Write([]byte("\n"))
		flusher.Flush()
	}
	if err := scanner.Err(); err != nil {
		// Write a terminal SSE error event if stream was truncated
		_, _ = fmt.Fprintf(w, "data: {\"error\":\"stream read error: %s\"}\n\n", err.Error())
		flusher.Flush()
	}
}

func writeResult(w http.ResponseWriter, result *forwardResult) {
	if result == nil {
		return
	}
	if result.headers != nil {
		for k, vals := range result.headers {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
	}
	if result.statusCode > 0 {
		w.WriteHeader(result.statusCode)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
	}
	if len(result.body) > 0 {
		_, _ = w.Write(result.body)
	}
}

func parseCooldownDuration(resp *http.Response) time.Duration {
	// Try Retry-After header (RFC 7231: delta-seconds or HTTP-date)
	if ra := strings.TrimSpace(resp.Header.Get("Retry-After")); ra != "" {
		// Try delta-seconds format first
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
		// Try HTTP-date format
		if t, err := http.ParseTime(ra); err == nil {
			if duration := time.Until(t); duration > 0 {
				return duration
			}
		}
	}
	// Default: 1 min
	return 1 * time.Minute
}

func parseCooldownFromBody(body []byte) time.Duration {
	var envelope struct {
		Error struct {
			ResetsInSeconds float64 `json:"resets_in_seconds"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.ResetsInSeconds > 0 {
		return time.Duration(envelope.Error.ResetsInSeconds * float64(time.Second))
	}
	return 0
}

func extractCodexUsageHeaders(headers http.Header) *codexShared.CodexUsageSnapshot {
	hasAny := false
	parseFloat := func(key string) float64 {
		v := headers.Get(key)
		if v == "" {
			return 0
		}
		hasAny = true
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0
		}
		return f
	}
	parseInt := func(key string) int {
		v := headers.Get(key)
		if v == "" {
			return 0
		}
		hasAny = true
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0
		}
		return n
	}

	s := &codexShared.CodexUsageSnapshot{
		PrimaryUsedPercent:          parseFloat("x-codex-primary-used-percent"),
		PrimaryResetAfterSeconds:    parseInt("x-codex-primary-reset-after-seconds"),
		PrimaryWindowMinutes:        parseInt("x-codex-primary-window-minutes"),
		SecondaryUsedPercent:        parseFloat("x-codex-secondary-used-percent"),
		SecondaryResetAfterSeconds:  parseInt("x-codex-secondary-reset-after-seconds"),
		SecondaryWindowMinutes:      parseInt("x-codex-secondary-window-minutes"),
		PrimaryOverSecondaryPercent: parseFloat("x-codex-primary-over-secondary-limit-percent"),
	}

	if !hasAny {
		return nil
	}
	s.UpdatedAt = time.Now()
	return s
}

func maskToken(token string) string {
	if len(token) <= 8 {
		return "***"
	}
	return token[:4] + "..." + token[len(token)-4:]
}

// parseCodexUsage extracts token usage from a response.completed SSE event
func parseCodexUsage(data []byte) (inputTokens, outputTokens int64) {
	var event struct {
		Type     string `json:"type"`
		Response struct {
			Usage struct {
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return 0, 0
	}
	return event.Response.Usage.InputTokens, event.Response.Usage.OutputTokens
}

// Placeholder for token estimation
func EstimateCodexInputTokens(body []byte) int {
	return len(body) / 4
}

// -- Helpers for OAuth login via Wails --

func StartCodexLoginWithURL(ctx context.Context, proxyURL string) (string, func() (*codexAuth.CodexLoginResult, error), func(), error) {
	return codexAuth.StartCodexLoginWithURL(ctx, proxyURL)
}
