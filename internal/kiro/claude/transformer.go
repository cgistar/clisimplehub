package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	kiroapi "clisimplehub/internal/kiro"
	"clisimplehub/internal/kiro/converters"
	kiroShared "clisimplehub/internal/kiro/shared"
	"clisimplehub/internal/kiro/streaming"
	"clisimplehub/internal/plugin"
	"clisimplehub/internal/transformer/shared"
)

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

	// Parse raw request map for compatibility helpers (e.g. metadata.user_id session id extraction).
	claudeReq, err := shared.DecodeJSONMap(rawJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to parse claude request: %w", err)
	}

	// Parse strongly-typed request for the new converter pipeline.
	var anthropicReq converters.AnthropicRequest
	if err := json.Unmarshal(rawJSON, &anthropicReq); err != nil {
		return nil, fmt.Errorf("failed to parse anthropic request: %w", err)
	}

	// Preserve existing model mapping behavior.
	mappedModelSource := strings.TrimSpace(modelName)
	if mappedModelSource == "" {
		mappedModelSource = strings.TrimSpace(anthropicReq.Model)
	}
	if mappedModelSource != "" {
		anthropicReq.Model = GetKiroModelID(mappedModelSource)
	}

	conversationID := generateConversationID(claudeReq)

	// Get profile ARN from auth manager
	profileArn := ""
	t.mu.RLock()
	am := t.authManager
	t.mu.RUnlock()
	if am != nil {
		profileArn = am.GetProfileArn()
	}

	// Convert to Kiro request using new shared converter.
	kiroReq, err := converters.AnthropicToKiro(&anthropicReq, conversationID, profileArn)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to kiro request: %w", err)
	}

	return shared.MarshalNoEscapeHTML(kiroReq)
}

func parseThinkingAndInputTokens(originalRequestRawJSON []byte) (bool, int) {
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

	return thinkingEnabled, inputTokens
}

func newStreamState(modelName string, originalRequestRawJSON []byte) *StreamStateV2 {
	thinkingEnabled, inputTokens := parseThinkingAndInputTokens(originalRequestRawJSON)
	return newStreamStateV2(modelName, inputTokens, thinkingEnabled, GetCachedBufferedStream())
}

func sseEventsToStrings(events []*streaming.SseEvent) []string {
	if len(events) == 0 {
		return nil
	}
	out := make([]string, 0, len(events))
	for _, ev := range events {
		if ev == nil {
			continue
		}
		out = append(out, ev.ToSSEString())
	}
	return out
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

	s, ok := (*state).(*StreamStateV2)
	if !ok || s == nil {
		*state = newStreamState(modelName, originalRequestRawJSON)
		s = (*state).(*StreamStateV2)
	}

	if len(rawLine) == 0 {
		return sseEventsToStrings(s.Finalize()), nil
	}

	payload := rawLine
	if p, ok := shared.SSEDataPayload(rawLine); ok {
		payload = bytes.TrimSpace(p)
		if len(payload) == 0 {
			return nil, nil
		}
		// SSE end marker used by some gateways.
		if bytes.Equal(payload, []byte("[DONE]")) {
			return sseEventsToStrings(s.Finalize()), nil
		}
	}

	events, err := s.ProcessPayload(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to parse event stream: %w", err)
	}

	return sseEventsToStrings(events), nil
}

// TransformResponseNonStream transforms Kiro non-streaming response to Claude format
func (t *Transformer) TransformResponseNonStream(
	ctx context.Context,
	modelName string,
	originalRequestRawJSON, requestRawJSON, rawJSON []byte,
	state *any,
) ([]byte, error) {
	thinkingEnabled, inputTokens := parseThinkingAndInputTokens(originalRequestRawJSON)

	kiroResp, err := shared.DecodeJSONMap(rawJSON)
	if err == nil {
		claudeResp := KiroToClaudeMessage(kiroResp, modelName, inputTokens)
		return shared.MarshalNoEscapeHTML(claudeResp)
	}

	streamState := newStreamStateV2(modelName, inputTokens, thinkingEnabled, false)
	events, streamErr := streamState.ProcessPayload(rawJSON)
	if streamErr != nil {
		return nil, fmt.Errorf("failed to parse kiro response: %w", err)
	}
	events = append(events, streamState.Finalize()...)

	claudeResp := buildMessageFromSSEEvents(events, modelName)
	return shared.MarshalNoEscapeHTML(claudeResp)
}

type collectedSSEBlock struct {
	blockType string
	id        string
	name      string
	text      strings.Builder
	args      strings.Builder
}

func buildMessageFromSSEEvents(events []*streaming.SseEvent, modelName string) map[string]any {
	messageID := "msg_kiro_" + shared.RandomSuffix()
	stopReason := "end_turn"
	inputTokens := 0
	outputTokens := 0

	blocks := make(map[int]*collectedSSEBlock)
	blockOrder := make([]int, 0)

	for _, ev := range events {
		if ev == nil {
			continue
		}
		data := ev.Data
		switch ev.Event {
		case "message_start":
			msg, _ := data["message"].(map[string]any)
			if msg != nil {
				if id := strings.TrimSpace(shared.StringFromAny(msg["id"])); id != "" {
					messageID = id
				}
				if model := strings.TrimSpace(shared.StringFromAny(msg["model"])); model != "" {
					modelName = model
				}
				if usage, ok := msg["usage"].(map[string]any); ok {
					if v := shared.IntFromAny(usage["input_tokens"]); v >= 0 {
						inputTokens = v
					}
					if v := shared.IntFromAny(usage["output_tokens"]); v >= 0 {
						outputTokens = v
					}
				}
			}
		case "content_block_start":
			idx := shared.IntFromAny(data["index"])
			if _, exists := blocks[idx]; !exists {
				blockOrder = append(blockOrder, idx)
			}
			cb, _ := data["content_block"].(map[string]any)
			blockType := strings.TrimSpace(shared.StringFromAny(cb["type"]))
			blocks[idx] = &collectedSSEBlock{
				blockType: blockType,
				id:        strings.TrimSpace(shared.StringFromAny(cb["id"])),
				name:      strings.TrimSpace(shared.StringFromAny(cb["name"])),
			}
		case "content_block_delta":
			idx := shared.IntFromAny(data["index"])
			block := blocks[idx]
			if block == nil {
				continue
			}
			delta, _ := data["delta"].(map[string]any)
			switch strings.TrimSpace(shared.StringFromAny(delta["type"])) {
			case "text_delta":
				block.text.WriteString(shared.StringFromAny(delta["text"]))
			case "input_json_delta":
				block.args.WriteString(shared.StringFromAny(delta["partial_json"]))
			}
		case "message_delta":
			delta, _ := data["delta"].(map[string]any)
			if delta != nil {
				if reason := strings.TrimSpace(shared.StringFromAny(delta["stop_reason"])); reason != "" {
					stopReason = reason
				}
			}
			if usage, ok := data["usage"].(map[string]any); ok {
				if v := shared.IntFromAny(usage["input_tokens"]); v >= 0 {
					inputTokens = v
				}
				if v := shared.IntFromAny(usage["output_tokens"]); v >= 0 {
					outputTokens = v
				}
			}
		}
	}

	sort.Ints(blockOrder)
	content := make([]any, 0, len(blockOrder))
	hasToolUse := false
	for _, idx := range blockOrder {
		block := blocks[idx]
		if block == nil {
			continue
		}
		switch block.blockType {
		case "text":
			text := block.text.String()
			if strings.TrimSpace(text) == "" {
				continue
			}
			content = append(content, map[string]any{
				"type": "text",
				"text": text,
			})
		case "tool_use":
			hasToolUse = true
			rawArgs := strings.TrimSpace(block.args.String())
			input := map[string]any{}
			if rawArgs != "" {
				var parsed any
				if err := json.Unmarshal([]byte(rawArgs), &parsed); err == nil {
					if m, ok := parsed.(map[string]any); ok {
						input = m
					}
				}
			}
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    block.id,
				"name":  block.name,
				"input": input,
			})
		}
	}

	if hasToolUse && stopReason == "end_turn" {
		stopReason = "tool_use"
	}

	return map[string]any{
		"id":            messageID,
		"type":          "message",
		"role":          "assistant",
		"content":       content,
		"model":         modelName,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
	}
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
		am.SetProxyURL(proxyURL)
		t.authManager = am
		return
	}

	am := NewKiroAuthManager(creds, t.kiroJsonPath, proxyURL, t.kiroVersion)
	t.authManagers[account.RefreshToken] = am
	t.authManager = am
}

// resolveProxyURL returns the effective proxy URL.
// Priority: global proxy (xray) -> per-account proxyUrl -> kiro.json proxyUrl.
func (t *Transformer) resolveProxyURL(account *kiroShared.KiroAccount) string {
	if gp := plugin.GetGlobalProxyProviderCached(); gp != nil {
		if gpURL := gp.GetGlobalProxyURL(); gpURL != "" {
			return gpURL
		}
	}
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
// Priority: global proxy (xray) -> per-account proxyUrl -> kiro.json top-level proxyUrl.
func (t *Transformer) KiroProxyURL() string {
	// Global proxy takes highest priority, no need for kiro init.
	if gp := plugin.GetGlobalProxyProviderCached(); gp != nil {
		if gpURL := gp.GetGlobalProxyURL(); gpURL != "" {
			return gpURL
		}
	}

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
	// Keep token refresh aligned with current global-proxy state.
	am.SetProxyURL(t.KiroProxyURL())
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
		// Keep token refresh aligned with current global-proxy state.
		am.SetProxyURL(t.KiroProxyURL())
		_, err := am.ForceRefresh()
		return err
	}
	return fmt.Errorf("auth manager not initialized")
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
