package kiroplugin

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	kiroapi "clisimplehub/internal/kiro"
	kiro_claude "clisimplehub/internal/kiro/claude"
	kiroShared "clisimplehub/internal/kiro/shared"
	"clisimplehub/internal/executor"
)

// kiroAttemptKey is the context key for tracking kiro failover attempts.
type kiroAttemptKey struct{}

func kiroFailoverAttempt(ctx context.Context) int {
	if v, ok := ctx.Value(kiroAttemptKey{}).(int); ok {
		return v
	}
	return 0
}

// tryFailoverRetry attempts to rebind to a different kiro account and retry.
func (s *KiroService) tryFailoverRetry(ctx context.Context, tr *kiro_claude.Transformer, body []byte, model string, isStreaming bool, w http.ResponseWriter) *executor.ForwardResult {
	pool := kiroapi.GetPool()
	if pool == nil || pool.Mode() == kiroShared.RotationFixed {
		return nil
	}
	if !tr.RebindAccount() {
		return nil
	}
	attempt := kiroFailoverAttempt(ctx)
	maxAttempts := pool.TotalCount()
	if attempt >= maxAttempts {
		return nil
	}
	nextCtx := context.WithValue(ctx, kiroAttemptKey{}, attempt+1)
	fmt.Fprintf(os.Stderr, "Info: kiro failover attempt %d/%d, switching account\n", attempt+1, maxAttempts)
	return s.Forward(nextCtx, body, model, isStreaming, w)
}

// handleKiroErrorStatus checks and updates Kiro account status on HTTP errors.
// Returns the detected status and whether failover should be attempted.
func handleKiroErrorStatus(statusCode int, body []byte, tr *kiro_claude.Transformer) (kiroShared.KiroAccountStatus, bool) {
	if tr == nil {
		return "", false
	}

	bodyStr := string(body)
	var targetStatus kiroShared.KiroAccountStatus
	var errorType string

	if statusCode == 402 && kiroShared.IsMonthlyRequestCountError(bodyStr) {
		targetStatus = kiroShared.KiroStatusExhausted
		errorType = "MONTHLY_REQUEST_COUNT"
	} else if statusCode == 403 && kiroShared.IsTemporarilySuspendedError(bodyStr) {
		targetStatus = kiroShared.KiroStatusBanned
		errorType = "TEMPORARILY_SUSPENDED"
	} else if statusCode == 401 {
		targetStatus = kiroShared.KiroStatusUnknown
		errorType = "AUTH_FAILURE"
	} else {
		return "", false
	}

	refreshToken := tr.CurrentAccountRefreshToken()

	// Fallback: determine from kiro.json active account
	if refreshToken == "" {
		configPath := strings.TrimSpace(os.Getenv("CONFIG_PATH"))
		if configPath == "" {
			if v, err := kiro_claude.GetConfig("configPath"); err == nil {
				configPath = strings.TrimSpace(v)
			}
		}
		multiConfigPath := ""
		if configPath != "" {
			multiConfigPath = filepath.Join(filepath.Dir(kiroShared.ExpandTilde(configPath)), filepath.Base(kiroShared.GetDefaultKiroMultiConfigPath()))
		}
		if multiConfigPath == "" {
			multiConfigPath = kiroShared.GetDefaultKiroMultiConfigPath()
		}
		if multiConfig, err := kiroShared.LoadKiroMultiConfig(multiConfigPath); err == nil && multiConfig != nil {
			refreshToken = strings.TrimSpace(multiConfig.ActiveRefreshToken)
		}
	}

	if refreshToken == "" {
		fmt.Fprintf(os.Stderr, "Warning: cannot determine refreshToken to update account status\n")
		return targetStatus, true
	}

	pool := kiroapi.GetPool()
	if pool != nil {
		if err := pool.MarkFailed(refreshToken, targetStatus); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to persist kiro account status: %v\n", err)
		}
		fmt.Fprintf(os.Stderr, "Info: pool marked account %s as %s due to %s\n", refreshToken[:min(8, len(refreshToken))], targetStatus, errorType)
		return targetStatus, true
	}

	// Fallback: direct update to kiro.json
	configPath := strings.TrimSpace(os.Getenv("CONFIG_PATH"))
	if configPath == "" {
		if v, err := kiro_claude.GetConfig("configPath"); err == nil {
			configPath = strings.TrimSpace(v)
		}
	}
	multiConfigPath := ""
	if configPath != "" {
		multiConfigPath = filepath.Join(filepath.Dir(kiroShared.ExpandTilde(configPath)), filepath.Base(kiroShared.GetDefaultKiroMultiConfigPath()))
	}
	if multiConfigPath == "" {
		multiConfigPath = kiroShared.GetDefaultKiroMultiConfigPath()
	}

	if err := kiroShared.UpdateAccountStatusByRefreshToken(refreshToken, targetStatus, multiConfigPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to update account status to %s (%s): %v\n", targetStatus, errorType, err)
	} else {
		fmt.Fprintf(os.Stderr, "Info: updated account status to %s due to %s error\n", targetStatus, errorType)
	}
	return targetStatus, true
}
