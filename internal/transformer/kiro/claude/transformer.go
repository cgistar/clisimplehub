package claude

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	kiroapi "clisimplehub/internal/transformer/kiro"
	kiroresponse "clisimplehub/internal/transformer/kiro/response"
	kiroShared "clisimplehub/internal/transformer/kiro/shared"
	"clisimplehub/internal/transformer/shared"
)

// Global storage accessor - set by proxy server at startup
var (
	globalConfigGetter func(key string) (string, error)
	globalConfigMu     sync.RWMutex
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

// Transformer implements the transformer.Transformer interface for Kiro -> Claude conversion
type Transformer struct {
	authManager       *KiroAuthManager
	kiroUserAgentBase string
	kiroVersion       string
	kiroProxyURL      string
	machineID         string
	initOnce          sync.Once
	initErr           error
}

// NewTransformer creates a new Kiro to Claude transformer
func NewTransformer() *Transformer {
	return &Transformer{}
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
		if thinkingEnabled && !isKiroThinkingEnabledByConfig() {
			thinkingEnabled = false
		}

		*state = &StreamState{
			MessageID:           "msg_kiro_" + shared.RandomSuffix(),
			Model:               modelName,
			Parser:              kiroresponse.NewEventStreamParser(),
			CurrentToolBlock:    -1,
			InputTokens:         inputTokens,
			ThinkingEnabled:     thinkingEnabled,
			ThinkingBlockIndex:  -1,
			SSEStateManager:     kiroresponse.NewSSEStateManager(false), // 非严格模式
			StopReasonManager:   kiroresponse.NewStopReasonManager(),
			CompletedToolUseIds: make(map[string]bool),
		}
	}

	// Type assertion with nil state recovery
	s, ok := (*state).(*StreamState)
	if !ok || s == nil {
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
		if thinkingEnabled && !isKiroThinkingEnabledByConfig() {
			thinkingEnabled = false
		}

		// Reinitialize if type assertion fails
		*state = &StreamState{
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

		s = (*state).(*StreamState)
	}

	// Upstream can be true SSE (text/event-stream) or AWS EventStream-like binary.
	// Do not trim/normalize the raw bytes unless we detected an SSE `data:` payload.
	if len(rawLine) == 0 {
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
	if thinkingEnabled && !isKiroThinkingEnabledByConfig() {
		thinkingEnabled = false
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

func isKiroThinkingEnabledByConfig() bool {
	v, err := getConfig("kiro.thinking")
	if err != nil {
		return false
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	b, parseErr := strconv.ParseBool(v)
	if parseErr != nil {
		return false
	}
	return b
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
	if v, err := getConfig("kiro.machineId"); err == nil {
		t.machineID = strings.TrimSpace(v)
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
