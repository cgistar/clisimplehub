package codexplugin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	codex "clisimplehub/internal/codex"
	codexShared "clisimplehub/internal/codex/shared"
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

	userAgent := r.Header.Get("User-Agent")
	isStreaming := gjson.GetBytes(body, "stream").Bool() ||
		gjson.GetBytes(body, "stream").String() == "true" ||
		strings.Contains(r.Header.Get("Accept"), "text/event-stream")
	clientHeaders := r.Header.Clone()
	requestModel := extractModelFromBody(body)

	pool := codex.GetPool()
	if pool == nil {
		http.Error(w, `{"error":"codex pool not initialized"}`, http.StatusInternalServerError)
		return
	}

	var debugLogger *logger.RequestDebugLogger
	requestID := executor.RequestIDFromContext(r.Context())
	if requestID == "" {
		requestID = fmt.Sprintf("codex-chat-%d", time.Now().UnixNano())
	}
	if logger.IsDebugFileModeEnabled() {
		debugLogger = logger.NewRequestDebugLogger(requestID)
		debugLogger.SetMetadata("Plugin", "Codex")
		debugLogger.SetMetadata("Path", r.URL.Path)
		debugLogger.SetMetadata("Method", r.Method)
		debugLogger.SetMetadata("Streaming", fmt.Sprintf("%v", isStreaming))
		debugLogger.SetMetadata("ChatConversion", "true")
		debugLogger.Log("Codex Chat Completions 请求开始")
		debugLogger.SetSection("OriginalRequest", string(body))
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

	prepared, prepErr := s.prepareCodexRequest(ctx, body, r.URL.Path, userAgent, "", clientHeaders, isStreaming)
	if prepErr != nil {
		finalStatusCode := prepErr.StatusCode
		finalStatus := "invalid_request"
		if prepErr.Error != nil {
			finalStatus = prepErr.Error.Error()
		}
		runTime := time.Since(startTime).Milliseconds()
		logCodexRequestToConsole(requestID, r.Method, r.URL.Path, nil, finalStatusCode, finalStatus, runTime)
		writeExecutorResult(w, prepErr)
		return
	}
	requestModel = prepared.requestModel

	var lastErr error
	var finalStatusCode int
	var finalStatus string
	var usedAccount *codexShared.CodexAccount
	var finalInputTokens, finalOutputTokens int64

	mode := pool.Mode()

	configPath := pool.ConfigPath()
	config, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil && !os.IsNotExist(err) {
		if debugLogger != nil {
			debugLogger.Log("配置加载失败: %v", err)
		}
		result := buildInternalError(fmt.Errorf("config load failed: %v", err))
		runTime := time.Since(startTime).Milliseconds()
		logCodexRequestToConsole(requestID, r.Method, r.URL.Path, nil, result.StatusCode, "error_config_load", runTime)
		writeExecutorResult(w, result)
		return
	}
	if config == nil {
		config = &codexShared.CodexMultiConfig{}
	}

	defer func() {
		if store := pool.Store(); store != nil && usedAccount != nil {
			now := time.Now()
			stat := &codexShared.CodexAccountStat{
				AccountID:    usedAccount.AccountID,
				AccountEmail: usedAccount.Email,
				Model:        requestModel,
				Date:         now.Format("2006-01-02"),
				Hour:         now.Hour(),
				InputTokens:  finalInputTokens,
				OutputTokens: finalOutputTokens,
				TotalTokens:  finalInputTokens + finalOutputTokens,
				StatusCode:   finalStatusCode,
				DurationMs:   time.Since(startTime).Milliseconds(),
				RequestPath:  r.URL.Path,
			}
			if finalStatusCode == http.StatusOK {
				stat.Status = "success"
			} else {
				stat.Status = "error"
				stat.ErrorType = finalStatus
			}
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = store.InsertStat(ctx, stat)
			}()
		}
	}()

	firstAccount := pool.Select()
	if firstAccount == nil {
		if debugLogger != nil {
			debugLogger.Log("%s 模式下无可用账号", mode)
		}
		runTime := time.Since(startTime).Milliseconds()
		finalStatus = fmt.Sprintf("error_no_accounts_%s", mode)
		finalStatusCode = http.StatusServiceUnavailable
		logCodexRequestToConsole(requestID, r.Method, r.URL.Path, nil, finalStatusCode, finalStatus, runTime)

		statusCode, errJSON := buildNoAccountsError(mode)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = w.Write(errJSON)
		return
	}

	for attempt := 0; attempt < maxRetryAccounts; attempt++ {
		var account *codexShared.CodexAccount
		if attempt == 0 {
			account = firstAccount
		} else {
			account = pool.Select()
			if account == nil {
				if debugLogger != nil {
					debugLogger.Log("第 %d 次尝试：无可用账号", attempt+1)
				}
				break
			}
		}
		usedAccount = account

		if debugLogger != nil {
			debugLogger.Log("尝试账号 %s (attempt %d)", maskToken(account.RefreshToken), attempt+1)
		}

		result, retryable := s.forwardWithAccount(ctx, account, prepared.body, prepared.isStreaming, w, pool, config, prepared.clientHeaders, r.URL.Path, prepared.chatConv)
		if result.Streamed {
			finalStatusCode = http.StatusOK
			finalStatus = "success"
			if result.Tokens != nil {
				finalInputTokens = result.Tokens.InputTokens
				finalOutputTokens = result.Tokens.OutputTokens
			}
			if debugLogger != nil {
				debugLogger.Log("流式响应已写入")
			}
			runTime := time.Since(startTime).Milliseconds()
			logCodexRequestToConsole(requestID, r.Method, r.URL.Path, account, finalStatusCode, finalStatus, runTime)
			return
		}

		finalStatusCode = result.StatusCode
		if result.StatusCode == http.StatusOK {
			finalStatus = "success"
			if result.Tokens != nil {
				finalInputTokens = result.Tokens.InputTokens
				finalOutputTokens = result.Tokens.OutputTokens
			}
		} else if result.Error != nil {
			finalStatus = result.Error.Error()
		} else {
			finalStatus = fmt.Sprintf("upstream_status_%d", result.StatusCode)
		}

		if !retryable {
			if debugLogger != nil {
				debugLogger.SetMetadata("FinalStatusCode", fmt.Sprintf("%d", result.StatusCode))
				debugLogger.Log("请求完成（不可重试）")
			}
			runTime := time.Since(startTime).Milliseconds()
			logCodexRequestToConsole(requestID, r.Method, r.URL.Path, account, finalStatusCode, finalStatus, runTime)
			writeExecutorResult(w, result)
			return
		}
		lastErr = result.Error
		if lastErr == nil {
			lastErr = fmt.Errorf("status %d", result.StatusCode)
		}
		if debugLogger != nil {
			debugLogger.Log("账号失败，准备重试: %v", lastErr)
		}
	}

	if debugLogger != nil {
		debugLogger.Log("所有账号均失败")
	}

	runTime := time.Since(startTime).Milliseconds()
	if lastErr != nil {
		finalStatus = "error_all_failed"
		finalStatusCode = http.StatusBadGateway
	} else {
		finalStatus = "error_no_accounts"
		finalStatusCode = http.StatusServiceUnavailable
	}
	logCodexRequestToConsole(requestID, r.Method, r.URL.Path, usedAccount, finalStatusCode, finalStatus, runTime)

	if lastErr != nil {
		_, errBody := buildAllFailedError(lastErr)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write(errBody)
		return
	}
	http.Error(w, `{"error":"no available codex accounts"}`, http.StatusServiceUnavailable)
}
