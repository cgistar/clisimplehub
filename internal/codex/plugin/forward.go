package codexplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	codexAuth "clisimplehub/internal/codex/auth"
	codexBackend "clisimplehub/internal/codex/backend"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/executor"
	"clisimplehub/internal/logger"
	appmiddleware "clisimplehub/internal/middleware"
	"clisimplehub/internal/proxy"
)

const (
	maxRetryAccounts          = 5
	codexNetworkRetryAttempts = 3
	codexNetworkRetryDelay    = 3 * time.Second
)

// logCodexRequestToConsole 输出 Codex 请求日志到控制台
func logCodexRequestToConsole(requestID, method, path string, account *codexShared.CodexAccount, statusCode int, status string, runTime int64) {
	// 根据状态码确定日志级别
	level := "INFO"
	if strings.HasPrefix(status, "error") {
		level = "WARN"
		if statusCode == 0 || statusCode >= 500 {
			level = "ERROR"
		}
	} else if statusCode >= 500 {
		level = "ERROR"
	} else if statusCode >= 400 {
		level = "WARN"
	}

	// 构建账号信息
	accountInfo := "no-account"
	if account != nil {
		if account.Email != "" {
			accountInfo = account.Email
		} else if account.AccountID != "" {
			accountInfo = account.AccountID
		} else {
			accountInfo = maskToken(account.RefreshToken)
		}
	}

	// 格式化请求ID（取前8位）
	shortID := strings.TrimSpace(requestID)
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	if shortID == "" {
		shortID = "-"
	}

	// 构建状态信息
	statusInfo := status
	if statusCode > 0 {
		statusInfo = fmt.Sprintf("%s (%d)", status, statusCode)
	}

	// 输出日志：[级别] [requestID] 方法 路径 | codex | 账号 | 状态 | 用时
	log.Printf("[%s] [%s] %s %s | codex | %s | %s | %.3fs",
		level, shortID, method, path, accountInfo, statusInfo, float64(runTime)/1000.0)
}

func (s *CodexService) HandleResponses(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	// Extract User-Agent for request body processing
	userAgent := r.Header.Get("User-Agent")

	processedBody, _, err := appmiddleware.NormalizeCodexResponsesRequest(body, r.URL.Path, userAgent)
	if err != nil {
		if errors.Is(err, appmiddleware.ErrCompactStreamingNotSupported) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(appmiddleware.CompactStreamingErrorPayload())
			return
		}
		// Continue with original body if processing fails
		processedBody = body
	}
	inboundModel := extractModelFromBody(processedBody)
	if rewrittenBody, rewritten := applyResolvedModelToBody(processedBody, ""); rewritten {
		processedBody = rewrittenBody
	}
	if bodyWithThinking, applied := codexBackend.ApplySuffixThinking(processedBody, inboundModel); applied {
		processedBody = bodyWithThinking
	}

	isStreaming := strings.Contains(r.Header.Get("Accept"), "text/event-stream")
	if proxy.IsCodexCompactResponsesPath(r.URL.Path) {
		isStreaming = false
	}
	clientHeaders := r.Header.Clone() // Preserve client headers for forwarding

	// 创建请求级别的调试日志记录器（每次检查配置，支持热更新）
	var debugLogger *logger.RequestDebugLogger
	requestID := executor.RequestIDFromContext(r.Context())
	if requestID == "" {
		requestID = fmt.Sprintf("codex-%d", time.Now().UnixNano())
	}
	if logger.IsDebugFileModeEnabled() {
		debugLogger = logger.NewRequestDebugLogger(requestID)
		debugLogger.SetMetadata("Plugin", "Codex")
		debugLogger.SetMetadata("Path", r.URL.Path)
		debugLogger.SetMetadata("Method", r.Method)
		debugLogger.SetMetadata("Streaming", fmt.Sprintf("%v", isStreaming))
		debugLogger.Log("Codex 请求开始")
		originalHeaders := r.Header.Clone()
		if inboundHeaders, ok := appmiddleware.OriginalHeadersFromContext(r.Context()); ok {
			originalHeaders = inboundHeaders
		}
		debugLogger.SetOriginalHeader(originalHeaders)
		debugLogger.SetSection("OriginalRequest", string(processedBody))
		defer func() {
			if debugLogger != nil {
				_ = debugLogger.Flush()
			}
		}()
	}

	ctx := r.Context()
	if debugLogger != nil {
		ctx = executor.WithDebugLogger(ctx, debugLogger)
	}

	requestModel := extractModelFromBody(processedBody)
	result := s.RoundTrip(ctx, &executor.UpstreamRequest{
		Method:              http.MethodPost,
		TargetPath:          r.URL.Path,
		RawQuery:            r.URL.RawQuery,
		Headers:             clientHeaders,
		Body:                processedBody,
		IsStreaming:         isStreaming,
		RequestModel:        requestModel,
		OriginalPath:        r.URL.Path,
		TargetInterfaceType: "codex",
	})
	writeUpstreamRoundTripHTTPResult(w, result)
	statusCode := http.StatusBadGateway
	if result != nil && result.StatusCode > 0 {
		statusCode = result.StatusCode
	}
	logCodexRequestToConsole(requestID, r.Method, r.URL.Path, nil, statusCode, upstreamRoundTripStatus(result), time.Since(startTime).Milliseconds())
}

func writeUpstreamRoundTripHTTPResult(w http.ResponseWriter, result *executor.UpstreamRoundTripResult) {
	if result == nil {
		http.Error(w, `{"error":"codex request failed"}`, http.StatusBadGateway)
		return
	}
	// 下游响应头：剥离 hop-by-hop / 网关指纹，不覆盖本侧已设 Content-Type。
	proxy.WriteUpstreamResponseHeaders(w.Header(), result.Headers)
	if ct := result.Headers.Get("Content-Type"); ct != "" && w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", ct)
	}
	statusCode := result.StatusCode
	if statusCode <= 0 {
		statusCode = http.StatusBadGateway
	}
	if result.Stream != nil {
		defer result.Stream.Close()
		w.WriteHeader(statusCode)
		buf := make([]byte, 32*1024)
		flusher, _ := w.(http.Flusher)
		framer := &codexBackend.ResponsesSSEFramer{}
		for {
			n, readErr := result.Stream.Read(buf)
			if n > 0 {
				framer.WriteChunk(w, buf[:n])
				if flusher != nil {
					flusher.Flush()
				}
			}
			if readErr != nil {
				framer.Flush(w)
				if flusher != nil {
					flusher.Flush()
				}
				return
			}
		}
	}
	w.WriteHeader(statusCode)
	if len(result.Body) > 0 {
		_, _ = w.Write(result.Body)
		return
	}
	if result.Error != nil {
		_, _ = w.Write([]byte(fmt.Sprintf(`{"error":{"message":%q}}`, result.Error.Error())))
	}
}

func upstreamRoundTripStatus(result *executor.UpstreamRoundTripResult) string {
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
			ResetsAt        int64   `json:"resets_at"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		if envelope.Error.ResetsInSeconds > 0 {
			return time.Duration(envelope.Error.ResetsInSeconds * float64(time.Second))
		}
		if envelope.Error.ResetsAt > 0 {
			if duration := time.Until(time.Unix(envelope.Error.ResetsAt, 0)); duration > 0 {
				return duration
			}
		}
	}
	return 0
}

func retryAfterFromBackendError(err error) time.Duration {
	var statusErr codexBackend.StatusError
	if errors.As(err, &statusErr) && statusErr.RetryAfter != nil {
		return *statusErr.RetryAfter
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

func extractModelFromBody(body []byte) string {
	var m struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &m) == nil {
		return m.Model
	}
	return ""
}

// -- Helpers for OAuth login via Wails --

func StartCodexLoginWithURL(ctx context.Context, proxyURL string) (string, func() (*codexAuth.CodexLoginResult, error), func(), error) {
	return codexAuth.StartCodexLoginWithURL(ctx, proxyURL)
}
