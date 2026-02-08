package claude

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	kiroapi "clisimplehub/internal/transformer/kiro"
	kiroresponse "clisimplehub/internal/transformer/kiro/response"
	kiroShared "clisimplehub/internal/transformer/kiro/shared"
	"clisimplehub/internal/transformer/shared"
)

// DefaultContextWindow 是 Claude 模型的默认上下文窗口大小（200k tokens）
// TODO: 支持不同模型的上下文窗口大小（如 500k, 1M）
const DefaultContextWindow = 200000

// Global storage accessor - set by proxy server at startup
var (
	globalConfigGetter func(key string) (string, error)
	globalConfigMu     sync.RWMutex

	// Global transformer registry for hot reload
	transformerRegistry   = make(map[*Transformer]struct{})
	transformerRegistryMu sync.RWMutex
)

// SetConfigGetter sets the global config getter function
func SetConfigGetter(getter func(key string) (string, error)) {
	globalConfigMu.Lock()
	defer globalConfigMu.Unlock()
	globalConfigGetter = getter
}

// getConfig retrieves a config value using the global getter
func getConfig(key string) (string, error) {
	globalConfigMu.RLock()
	defer globalConfigMu.RUnlock()
	if globalConfigGetter == nil {
		return "", fmt.Errorf("config getter not initialized")
	}
	return globalConfigGetter(key)
}

// GetConfig exposes the global config getter for non-transformer packages.
// It is intentionally a thin wrapper around the internal getConfig.
func GetConfig(key string) (string, error) {
	return getConfig(key)
}

// Transformer implements the transformer.Transformer interface for Kiro -> Claude conversion
type Transformer struct {
	authManagers      map[string]*KiroAuthManager // refreshToken -> manager cache
	currentAccount    *kiroShared.KiroAccount     // currently bound account
	authManager       *KiroAuthManager            // current active auth manager
	kiroUserAgentBase string
	kiroVersion       string
	kiroProxyURL      string
	kiroRegion        string // top-level region from kiro.json
	kiroJsonPath      string
	machineID         string
	initOnce          sync.Once
	initErr           error
	mu                sync.RWMutex
}

// NewTransformer creates a new Kiro to Claude transformer
func NewTransformer() *Transformer {
	t := &Transformer{}

	// 注册到全局注册表
	transformerRegistryMu.Lock()
	transformerRegistry[t] = struct{}{}
	transformerRegistryMu.Unlock()

	return t
}

// ensureInitialized triggers lazy init and returns any init error.
func (t *Transformer) ensureInitialized() error {
	t.initOnce.Do(func() {
		t.initErr = t.initialize()
	})
	return t.initErr
}

// TargetInterfaceType returns the target interface type
func (t *Transformer) TargetInterfaceType() string {
	return "kiro"
}

// TargetPath returns the target API path
func (t *Transformer) TargetPath(isStreaming bool, upstreamModel string) string {
	return "/generateAssistantResponse"
}

// OutputContentType returns the output content type
func (t *Transformer) OutputContentType(isStreaming bool) string {
	if isStreaming {
		return "text/event-stream"
	}
	return "application/json"
}

// TransformRequest transforms a Claude API request to Kiro API format
func (t *Transformer) TransformRequest(modelName string, rawJSON []byte, stream bool) ([]byte, error) {
	if err := t.ensureInitialized(); err != nil {
		return nil, fmt.Errorf("kiro transformer initialization failed: %w", err)
	}

	// Parse Claude request
	claudeReq, err := shared.DecodeJSONMap(rawJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to parse claude request: %w", err)
	}

	// Get profile ARN from auth manager
	profileArn := ""
	t.mu.RLock()
	am := t.authManager
	t.mu.RUnlock()
	if am != nil {
		profileArn = am.GetProfileArn()
	}

	// Convert to Kiro request
	kiroReq, err := ClaudeToKiroRequest(claudeReq, modelName, profileArn)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to kiro request: %w", err)
	}

	return shared.MarshalNoEscapeHTML(kiroReq)
}

// newStreamState 创建一个新的 StreamState 实例
// 提取公共初始化逻辑，避免代码重复
func newStreamState(modelName string, originalRequestRawJSON []byte) *StreamState {
	thinkingEnabled := false
	inputTokens := 0

	// 解析请求以提取 thinking 和 token 信息
	if len(originalRequestRawJSON) > 0 {
		inputTokens = EstimateClaudeInputTokens(originalRequestRawJSON)
		if req, err := shared.DecodeJSONMap(originalRequestRawJSON); err == nil {
			switch v := req["thinking"].(type) {
			case map[string]any:
				t := strings.ToLower(strings.TrimSpace(shared.StringFromAny(v["type"])))
				if t == "enabled" || t == "adaptive" {
					thinkingEnabled = true
				}
			case string:
				t := strings.ToLower(strings.TrimSpace(v))
				if t == "enabled" || t == "adaptive" {
					thinkingEnabled = true
				}
			}
		}
	}

	// 读取缓冲流式模式配置
	bufferedStreamingEnabled := GetCachedBufferedStream()

	return &StreamState{
		MessageID:                 "msg_kiro_" + shared.RandomSuffix(),
		Model:                     modelName,
		Parser:                    kiroresponse.NewEventStreamParser(),
		CurrentToolBlock:          -1,
		InputTokens:               inputTokens,
		ThinkingEnabled:           thinkingEnabled,
		ThinkingBlockIndex:        -1,
		SSEStateManager:           kiroresponse.NewSSEStateManager(false),
		StopReasonManager:         kiroresponse.NewStopReasonManager(),
		CompletedToolUseIds:       make(map[string]bool),
		BufferedStreamingEnabled:  bufferedStreamingEnabled,
		EstimatedInputTokens:      inputTokens,
		BufferedOutputs:           []string{},
		BufferedMessageStartIndex: -1,
	}
}

// TransformResponseStream transforms Kiro streaming response to Claude SSE format
func (t *Transformer) TransformResponseStream(
	ctx context.Context,
	modelName string,
	originalRequestRawJSON, requestRawJSON, rawLine []byte,
	state *any,
) ([]string, error) {
	if state == nil {
		return nil, fmt.Errorf("nil transformer state")
	}

	// Initialize state on first call
	if *state == nil {
		*state = newStreamState(modelName, originalRequestRawJSON)
	}

	// Type assertion with nil state recovery
	s, ok := (*state).(*StreamState)
	if !ok || s == nil {
		// Reinitialize if type assertion fails
		*state = newStreamState(modelName, originalRequestRawJSON)
		s = (*state).(*StreamState)
	}

	// Upstream can be true SSE (text/event-stream) or AWS EventStream-like binary.
	// Do not trim/normalize the raw bytes unless we detected an SSE `data:` payload.
	if len(rawLine) == 0 {
		// 流结束信号：在缓冲模式下 flush 所有事件
		if s.BufferedStreamingEnabled && len(s.BufferedOutputs) > 0 {
			return flushBufferedStream(s), nil
		}
		return nil, nil
	}

	payload := rawLine
	if p, ok := shared.SSEDataPayload(rawLine); ok {
		payload = bytes.TrimSpace(p)
		if len(payload) == 0 {
			return nil, nil
		}
		// SSE end marker used by some gateways.
		if bytes.Equal(payload, []byte("[DONE]")) {
			if s.BufferedStreamingEnabled {
				return flushBufferedStream(s), nil
			}
			return FinishStream(s), nil
		}
	}

	// Parse EventStream
	events, err := s.Parser.Feed(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to parse event stream: %w", err)
	}

	// Convert events to Claude SSE format
	var outputs []string
	for _, event := range events {
		lines, err := KiroStreamToClaudeSSE(event, s)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, lines...)
	}

	// 缓冲模式：追加到缓冲区而不是直接返回
	if s.BufferedStreamingEnabled {
		// 记录 message_start 的位置
		if s.BufferedMessageStartIndex < 0 {
			for i, output := range outputs {
				if strings.Contains(output, "event: message_start\n") {
					s.BufferedMessageStartIndex = len(s.BufferedOutputs) + i
					break
				}
			}
		}
		s.BufferedOutputs = append(s.BufferedOutputs, outputs...)
		return nil, nil // 不输出，等待流结束
	}

	return outputs, nil
}

// TransformResponseNonStream transforms Kiro non-streaming response to Claude format
func (t *Transformer) TransformResponseNonStream(
	ctx context.Context,
	modelName string,
	originalRequestRawJSON, requestRawJSON, rawJSON []byte,
	state *any,
) ([]byte, error) {
	thinkingEnabled := false
	inputTokens := 0
	if len(originalRequestRawJSON) > 0 {
		inputTokens = EstimateClaudeInputTokens(originalRequestRawJSON)
		if req, err := shared.DecodeJSONMap(originalRequestRawJSON); err == nil {
			switch v := req["thinking"].(type) {
			case map[string]any:
				t := strings.ToLower(strings.TrimSpace(shared.StringFromAny(v["type"])))
				if t == "enabled" || t == "adaptive" {
					thinkingEnabled = true
				}
			case string:
				t := strings.ToLower(strings.TrimSpace(v))
				if t == "enabled" || t == "adaptive" {
					thinkingEnabled = true
				}
			}
		}
	}

	streamState := &StreamState{
		MessageID:           "msg_kiro_" + shared.RandomSuffix(),
		Model:               modelName,
		Parser:              kiroresponse.NewEventStreamParser(),
		CurrentToolBlock:    -1,
		InputTokens:         inputTokens,
		ThinkingEnabled:     thinkingEnabled,
		ThinkingBlockIndex:  -1,
		SSEStateManager:     kiroresponse.NewSSEStateManager(false),
		StopReasonManager:   kiroresponse.NewStopReasonManager(),
		CompletedToolUseIds: make(map[string]bool),
	}

	// Parse Kiro response
	kiroResp, err := shared.DecodeJSONMap(rawJSON)
	if err != nil {
		// Kiro may return AWS EventStream-like payload even for non-stream requests.
		events, streamErr := streamState.Parser.Feed(rawJSON)
		if streamErr != nil {
			return nil, fmt.Errorf("failed to parse kiro response: %w", err)
		}
		for _, event := range events {
			if _, convErr := KiroStreamToClaudeSSE(event, streamState); convErr != nil {
				return nil, convErr
			}
		}
		_ = FinishStream(streamState)

		claudeResp := StreamStateToClaudeMessage(streamState, modelName)
		return shared.MarshalNoEscapeHTML(claudeResp)
	}

	// Convert to Claude message format
	claudeResp := KiroToClaudeMessage(kiroResp, modelName, streamState)

	return shared.MarshalNoEscapeHTML(claudeResp)
}

// initialize loads credentials and creates the auth manager
func (t *Transformer) initialize() error {
	t.authManagers = make(map[string]*KiroAuthManager)

	kiroJsonPath := ""
	if configPath, err := getConfig("configPath"); err == nil && configPath != "" {
		dir := filepath.Dir(kiroShared.ExpandTilde(configPath))
		kiroJsonPath = filepath.Join(dir, filepath.Base(kiroShared.GetDefaultKiroMultiConfigPath()))
	}
	t.kiroJsonPath = kiroJsonPath

	// Read global config from kiro.json
	if kiroJsonPath != "" {
		if mc, err := kiroShared.LoadKiroMultiConfig(kiroJsonPath); err == nil {
			t.kiroUserAgentBase = strings.TrimSpace(mc.UserAgent)
			t.kiroVersion = strings.TrimSpace(mc.Version)
			t.kiroProxyURL = strings.TrimSpace(mc.ProxyURL)
			t.kiroRegion = strings.TrimSpace(mc.Region)
			SetCachedBufferedStream(mc.BufferedStream)
			SetCachedModelMapping(mc.ModelMapping)
		}
	}

	// Select account via pool (if initialized) or fallback to active account in kiro.json
	var account *kiroShared.KiroAccount
	pool := kiroapi.GetPool()
	if pool != nil {
		account = pool.Select()
	}
	if account == nil && kiroJsonPath != "" {
		if mc, err := kiroShared.LoadKiroMultiConfig(kiroJsonPath); err == nil {
			account = mc.GetActiveAccount()
		}
	}
	if account == nil {
		fmt.Fprintf(os.Stderr, "Warning: no kiro account available\n")
		return nil
	}

	t.currentAccount = account
	t.bindAccountLocked(account)
	return nil
}

// bindAccountLocked creates/reuses a KiroAuthManager for the given account and binds it.
// Caller must hold t.mu.
func (t *Transformer) bindAccountLocked(account *kiroShared.KiroAccount) {
	if account == nil {
		return
	}
	if t.authManagers == nil {
		t.authManagers = make(map[string]*KiroAuthManager)
	}
	t.currentAccount = account
	creds := account.ToCredentials()
	mid := strings.TrimSpace(creds.MachineID)
	if mid == "" {
		mid = kiroapi.ComputeMachineID(creds.RefreshToken)
	}
	t.machineID = mid

	proxyURL := t.resolveProxyURL(account)

	// Reuse existing auth manager if available
	if am, ok := t.authManagers[account.RefreshToken]; ok {
		t.authManager = am
		return
	}

	am := NewKiroAuthManager(creds, t.kiroJsonPath, proxyURL, t.kiroVersion)
	t.authManagers[account.RefreshToken] = am
	t.authManager = am
}

// resolveProxyURL returns per-account proxyUrl if set, else global proxyUrl.
func (t *Transformer) resolveProxyURL(account *kiroShared.KiroAccount) string {
	if account != nil && strings.TrimSpace(account.ProxyUrl) != "" {
		return strings.TrimSpace(account.ProxyUrl)
	}
	return strings.TrimSpace(t.kiroProxyURL)
}

// RebindAccount selects the next account from the pool and binds it.
// Returns false if no different account is available.
func (t *Transformer) RebindAccount() bool {
	pool := kiroapi.GetPool()
	if pool == nil {
		return false
	}
	account := pool.Select()
	if account == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.currentAccount != nil && account.RefreshToken == t.currentAccount.RefreshToken {
		return false
	}
	t.bindAccountLocked(account)
	return true
}

// CurrentAccountRefreshToken returns the refresh token of the currently bound account.
func (t *Transformer) CurrentAccountRefreshToken() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.currentAccount == nil {
		return ""
	}
	return t.currentAccount.RefreshToken
}

// KiroProxyURL returns the effective Kiro proxy URL for the currently bound account.
// Priority: per-account proxyUrl -> kiro.json top-level proxyUrl.
func (t *Transformer) KiroProxyURL() string {
	_ = t.ensureInitialized()
	t.mu.RLock()
	account := t.currentAccount
	globalProxyURL := t.kiroProxyURL
	t.mu.RUnlock()

	// Priority: per-account proxyUrl -> kiro.json top-level proxyUrl.
	if account != nil {
		if v := strings.TrimSpace(account.ProxyUrl); v != "" {
			return v
		}
	}
	return strings.TrimSpace(globalProxyURL)
}

// KiroUserAgentBase returns the configured Kiro User-Agent base prefix.
func (t *Transformer) KiroUserAgentBase() string {
	_ = t.ensureInitialized()
	t.mu.RLock()
	v := t.kiroUserAgentBase
	t.mu.RUnlock()
	return kiroShared.KiroUserAgentBaseOrDefault(v)
}

// KiroVersion returns the configured Kiro client version token.
func (t *Transformer) KiroVersion() string {
	_ = t.ensureInitialized()
	t.mu.RLock()
	v := t.kiroVersion
	t.mu.RUnlock()
	return kiroShared.KiroVersionOrDefault(v)
}

func (t *Transformer) MachineID() string {
	_ = t.ensureInitialized()
	t.mu.RLock()
	v := t.machineID
	t.mu.RUnlock()
	return strings.TrimSpace(v)
}

// GetAuthManager returns the auth manager (for use by proxy server)
func (t *Transformer) GetAuthManager() *KiroAuthManager {
	_ = t.ensureInitialized()
	t.mu.RLock()
	am := t.authManager
	t.mu.RUnlock()
	return am
}

// GetAccessToken returns a valid access token (convenience method)
func (t *Transformer) GetAccessToken() (string, error) {
	if err := t.ensureInitialized(); err != nil {
		return "", fmt.Errorf("kiro transformer initialization failed: %w", err)
	}

	t.mu.RLock()
	am := t.authManager
	t.mu.RUnlock()
	if am == nil {
		return "", fmt.Errorf("auth manager not initialized")
	}
	return am.GetAccessToken()
}

// GetRegion returns the top-level kiro.json region (per-account region is not used).
func (t *Transformer) GetRegion() string {
	_ = t.ensureInitialized()
	t.mu.RLock()
	r := t.kiroRegion
	t.mu.RUnlock()
	if v := strings.TrimSpace(r); v != "" {
		return v
	}
	return "us-east-1"
}

// GetAPIURL returns the Kiro API URL using the top-level kiro.json region only.
func (t *Transformer) GetAPIURL() string {
	_ = t.ensureInitialized()
	t.mu.RLock()
	r := t.kiroRegion
	t.mu.RUnlock()
	return kiroapi.KiroGenerateURL(r) // KiroGenerateURL handles empty → "us-east-1"
}

// ForceRefreshKiroToken forces a refresh of the access token (best-effort).
func (t *Transformer) ForceRefreshKiroToken() error {
	if t == nil {
		return fmt.Errorf("nil transformer")
	}
	if am := t.GetAuthManager(); am != nil {
		_, err := am.ForceRefresh()
		return err
	}
	return fmt.Errorf("auth manager not initialized")
}

// flushBufferedStream 在缓冲流式模式结束时，修正 token 并一次性返回所有事件
func flushBufferedStream(s *StreamState) []string {
	if s == nil || !s.BufferedStreamingEnabled {
		return nil
	}

	// 1. 生成最终事件（message_delta, message_stop）
	if !s.Finished {
		finalEvents := FinishStream(s)
		s.BufferedOutputs = append(s.BufferedOutputs, finalEvents...)
	}

	// 2. 计算最终的 input_tokens（优先使用 contextUsageEvent 计算的值）
	finalInputTokens := s.InputTokens
	if s.ContextUsagePct > 0 {
		// contextUsageEvent 提供了精确值
		finalInputTokens = int(s.ContextUsagePct * DefaultContextWindow / 100)
	} else if finalInputTokens == 0 {
		// 回退到估算值
		finalInputTokens = s.EstimatedInputTokens
	}

	// 3. 回溯修正 message_start 中的 input_tokens
	if s.BufferedMessageStartIndex >= 0 && s.BufferedMessageStartIndex < len(s.BufferedOutputs) {
		// 重新生成 message_start 事件（使用现有的 buildMessageStart 函数）
		correctedMessageStart := buildMessageStart(s.MessageID, s.Model, finalInputTokens)
		s.BufferedOutputs[s.BufferedMessageStartIndex] = correctedMessageStart
	} else if len(s.BufferedOutputs) > 0 {
		// message_start 未找到，在开头插入
		correctedMessageStart := buildMessageStart(s.MessageID, s.Model, finalInputTokens)
		s.BufferedOutputs = append([]string{correctedMessageStart}, s.BufferedOutputs...)
	}

	// 4. 返回所有缓冲事件
	result := s.BufferedOutputs
	s.BufferedOutputs = nil // 清空缓冲区
	return result
}

// Reload resets initialization state and reloads kiro.json cached values.
func (t *Transformer) Reload() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.initOnce = sync.Once{}
	t.initErr = nil
	t.authManager = nil
	t.authManagers = nil
	t.currentAccount = nil
	t.kiroUserAgentBase = ""
	t.kiroVersion = ""
	t.kiroProxyURL = ""
	t.kiroRegion = ""
	t.kiroJsonPath = ""
	t.machineID = ""

	// Reload pool if available
	if pool := kiroapi.GetPool(); pool != nil {
		pool.Reload()
	}

	// Eagerly reload kiro.json cached values
	if configPath, err := getConfig("configPath"); err == nil && configPath != "" {
		dir := filepath.Dir(kiroShared.ExpandTilde(configPath))
		kiroJsonPath := filepath.Join(dir, filepath.Base(kiroShared.GetDefaultKiroMultiConfigPath()))
		if mc, err := kiroShared.LoadKiroMultiConfig(kiroJsonPath); err == nil {
			SetCachedBufferedStream(mc.BufferedStream)
			SetCachedModelMapping(mc.ModelMapping)
		}
	}

	return nil
}

// ReloadAllTransformers reloads all registered Kiro transformers.
func ReloadAllTransformers() error {
	transformerRegistryMu.RLock()
	defer transformerRegistryMu.RUnlock()

	for t := range transformerRegistry {
		if err := t.Reload(); err != nil {
			return fmt.Errorf("failed to reload transformer: %w", err)
		}
	}

	return nil
}
