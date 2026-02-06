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
	authManager       *KiroAuthManager
	kiroUserAgentBase string
	kiroVersion       string
	kiroProxyURL      string
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
	// Lazy initialization
	t.initOnce.Do(func() {
		t.initErr = t.initialize()
	})
	if t.initErr != nil {
		return nil, fmt.Errorf("kiro transformer initialization failed: %w", t.initErr)
	}

	// Parse Claude request
	claudeReq, err := shared.DecodeJSONMap(rawJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to parse claude request: %w", err)
	}

	// Get profile ARN from auth manager
	profileArn := ""
	if t.authManager != nil {
		profileArn = t.authManager.GetProfileArn()
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
				if strings.EqualFold(strings.TrimSpace(shared.StringFromAny(v["type"])), "enabled") {
					thinkingEnabled = true
				}
			case string:
				if strings.EqualFold(strings.TrimSpace(v), "enabled") {
					thinkingEnabled = true
				}
			}
		}
	}

	// 读取缓冲流式模式配置
	bufferedStreamingEnabled := false
	if bs, err := getConfig("kiro.bufferedStream"); err == nil && bs == "true" {
		bufferedStreamingEnabled = true
	}

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
				if strings.EqualFold(strings.TrimSpace(shared.StringFromAny(v["type"])), "enabled") {
					thinkingEnabled = true
				}
			case string:
				if strings.EqualFold(strings.TrimSpace(v), "enabled") {
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
	// Capture Kiro client header configuration (best-effort; defaults applied at call sites).
	if ua, err := getConfig("kiro.userAgent"); err == nil {
		t.kiroUserAgentBase = strings.TrimSpace(ua)
	}
	if v, err := getConfig("kiro.version"); err == nil {
		t.kiroVersion = strings.TrimSpace(v)
	}

	// credentials live next to config.json.
	credsPath := ""
	if configPath, err := getConfig("configPath"); err == nil && configPath != "" {
		credsPath = filepath.Join(filepath.Dir(kiroShared.ExpandTilde(configPath)), filepath.Base(kiroShared.GetDefaultKiroCredentialsPath()))
	}
	if credsPath == "" {
		credsPath = kiroShared.GetDefaultKiroCredentialsPath()
	}

	// Load credentials
	creds, err := kiroShared.LoadKiroCredentials(credsPath)
	if err != nil {
		// Don't fail initialization, just log warning
		// The transformer can still work if credentials are provided via endpoint config
		fmt.Fprintf(os.Stderr, "Warning: failed to load kiro credentials from %s: %v\n", credsPath, err)
		return nil
	}
	if v := strings.TrimSpace(creds.MachineID); v != "" {
		t.machineID = v
	}
	if t.machineID == "" {
		t.machineID = kiroapi.ComputeMachineID(creds.RefreshToken)
	}

	// Get proxy configuration
	proxyURL := ""
	if proxy, err := getConfig("kiro.proxyUrl"); err == nil {
		proxyURL = proxy
	}
	t.kiroProxyURL = strings.TrimSpace(proxyURL)

	// Create auth manager with proxy support
	t.authManager = NewKiroAuthManager(creds, credsPath, proxyURL, t.kiroVersion)

	return nil
}

// KiroProxyURL returns the configured global Kiro proxy URL (from config.json `kiro.proxyUrl`).
func (t *Transformer) KiroProxyURL() string {
	t.initOnce.Do(func() {
		t.initErr = t.initialize()
	})
	return strings.TrimSpace(t.kiroProxyURL)
}

// KiroUserAgentBase returns the configured Kiro User-Agent base prefix.
// If config.json doesn't specify one, it returns the built-in default.
func (t *Transformer) KiroUserAgentBase() string {
	t.initOnce.Do(func() {
		t.initErr = t.initialize()
	})
	return kiroShared.KiroUserAgentBaseOrDefault(t.kiroUserAgentBase)
}

// KiroVersion returns the configured Kiro client version token.
// If config.json doesn't specify one, it returns the built-in default.
func (t *Transformer) KiroVersion() string {
	t.initOnce.Do(func() {
		t.initErr = t.initialize()
	})
	return kiroShared.KiroVersionOrDefault(t.kiroVersion)
}

func (t *Transformer) MachineID() string {
	t.initOnce.Do(func() {
		t.initErr = t.initialize()
	})
	return strings.TrimSpace(t.machineID)
}

// GetAuthManager returns the auth manager (for use by proxy server)
func (t *Transformer) GetAuthManager() *KiroAuthManager {
	// Ensure initialization
	t.initOnce.Do(func() {
		t.initErr = t.initialize()
	})
	return t.authManager
}

// GetAccessToken returns a valid access token (convenience method)
func (t *Transformer) GetAccessToken() (string, error) {
	// Ensure initialization
	t.initOnce.Do(func() {
		t.initErr = t.initialize()
	})
	if t.initErr != nil {
		return "", fmt.Errorf("kiro transformer initialization failed: %w", t.initErr)
	}

	if t.authManager == nil {
		return "", fmt.Errorf("auth manager not initialized")
	}
	return t.authManager.GetAccessToken()
}

// GetRegion returns the configured region
func (t *Transformer) GetRegion() string {
	// Ensure initialization
	t.initOnce.Do(func() {
		t.initErr = t.initialize()
	})

	if t.authManager == nil {
		return "us-east-1"
	}
	return t.authManager.GetRegion()
}

// GetAPIURL returns the Kiro API URL for the configured region
func (t *Transformer) GetAPIURL() string {
	return kiroapi.KiroGenerateURL(t.GetRegion())
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

// Reload 重新加载 Kiro 配置
// 此方法会重置初始化状态，下次调用时会重新读取 kiro-auth-token.json
func (t *Transformer) Reload() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// 重置 sync.Once，允许重新初始化
	t.initOnce = sync.Once{}
	t.initErr = nil
	t.authManager = nil
	t.kiroUserAgentBase = ""
	t.kiroVersion = ""
	t.kiroProxyURL = ""
	t.machineID = ""

	return nil
}

// ReloadAllTransformers 重新加载所有已注册的 Kiro Transformer
// 此函数应在 kiro-auth-token.json 更新后调用
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
