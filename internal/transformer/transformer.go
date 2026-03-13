package transformer

import (
	"context"
	"fmt"
	"strings"
	"sync"

	chat_responses "clisimplehub/internal/transformer/chat/openai/responses"
	claude_gemini "clisimplehub/internal/transformer/claude/gemini"
	claude_chat "clisimplehub/internal/transformer/claude/openai/chat-completions"
	claude_responses "clisimplehub/internal/transformer/claude/openai/responses"
	codex_chat "clisimplehub/internal/transformer/codex/openai/chat-completions"
)

type Transformer interface {
	TargetInterfaceType() string
	TargetPath(isStreaming bool, upstreamModel string) string

	TransformRequest(modelName string, rawJSON []byte, stream bool) ([]byte, error)
	TransformResponseStream(ctx context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawLine []byte, state *any) ([]string, error)
	TransformResponseNonStream(ctx context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, state *any) ([]byte, error)

	OutputContentType(isStreaming bool) string
}

// --- Dynamic registration ---

var (
	regMu         sync.RWMutex
	factories     = map[string]func() Transformer{} // key: "from/spec" e.g. "claude/kiro/claude"
	availCheckers = map[string]func() map[string][]string{}
)

// RegisterFactory registers a transformer factory for a given (from, spec) pair.
// Plugins call this during init to register their transformers.
func RegisterFactory(from, spec string, factory func() Transformer) {
	regMu.Lock()
	defer regMu.Unlock()
	key := strings.ToLower(from) + "/" + strings.ToLower(spec)
	factories[key] = factory
}

// RegisterAvailability registers a named availability checker.
// The function should return nil/empty when the plugin's transformers are not available.
// Using a name key ensures idempotency on repeated Init() calls.
func RegisterAvailability(name string, check func() map[string][]string) {
	regMu.Lock()
	defer regMu.Unlock()
	availCheckers[name] = check
}

func getRegistered(from, spec string) (Transformer, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	key := strings.ToLower(from) + "/" + strings.ToLower(spec)
	if f, ok := factories[key]; ok {
		return f(), true
	}
	return nil, false
}

func registeredAvailability() map[string][]string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := map[string][]string{}
	for _, check := range availCheckers {
		for k, v := range check() {
			out[k] = append(out[k], v...)
		}
	}
	return out
}

// --- Core ---

func Get(fromInterfaceType, transformerSpec string) (Transformer, error) {
	from := strings.ToLower(strings.TrimSpace(fromInterfaceType))
	spec := strings.ToLower(strings.TrimSpace(transformerSpec))
	if from == "" || spec == "" {
		return nil, fmt.Errorf("invalid transformer: from=%q spec=%q", fromInterfaceType, transformerSpec)
	}

	// Check registered factories first
	if tr, ok := getRegistered(from, spec); ok {
		return tr, nil
	}

	switch from {
	case "claude":
		return getFromClaude(spec)
	case "chat":
		return getFromChat(spec)
	case "codex":
		return getFromCodex(spec)
	default:
		return nil, fmt.Errorf("unsupported transformer source interfaceType=%q", fromInterfaceType)
	}
}

func getFromClaude(spec string) (Transformer, error) {
	switch {
	case strings.Contains(spec, "chat-completions") || strings.Contains(spec, "chat/completions") || strings.Contains(spec, "chat"):
		return claude_chat.Transformer{}, nil
	case strings.Contains(spec, "responses") || strings.Contains(spec, "codex"):
		return claude_responses.Transformer{}, nil
	case strings.Contains(spec, "gemini"):
		return claude_gemini.Transformer{}, nil
	default:
		return nil, fmt.Errorf("unsupported claude transformer spec=%q (expected openai/chat-completions | openai/responses | gemini)", spec)
	}
}

func getFromCodex(spec string) (Transformer, error) {
	switch {
	case strings.Contains(spec, "chat-completions") || strings.Contains(spec, "chat/completions") || strings.Contains(spec, "chat"):
		return codex_chat.Transformer{}, nil
	default:
		return nil, fmt.Errorf("unsupported codex transformer spec=%q (expected openai/chat-completions)", spec)
	}
}

func getFromChat(spec string) (Transformer, error) {
	switch {
	case strings.Contains(spec, "responses"):
		return chat_responses.Transformer{}, nil
	default:
		return nil, fmt.Errorf("unsupported chat transformer spec=%q (expected openai/responses)", spec)
	}
}

// List returns canonical transformer specs for a given source interfaceType.
func List(fromInterfaceType string) ([]string, error) {
	avail := registeredAvailability()

	switch strings.ToLower(strings.TrimSpace(fromInterfaceType)) {
	case "claude":
		out := append([]string{}, avail["claude"]...)
		out = append(out, "openai/chat-completions", "openai/responses", "gemini")
		return out, nil
	case "codex":
		return []string{"openai/chat-completions"}, nil
	case "chat":
		out := []string{}
		if specs, ok := avail["chat"]; ok && len(specs) > 0 {
			out = append(out, specs...)
		}
		out = append(out, "openai/responses")
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported transformer source interfaceType=%q", fromInterfaceType)
	}
}

// ListAll returns all supported transformer specs grouped by source interfaceType.
func ListAll() map[string][]string {
	out := map[string][]string{
		"claude": {"openai/chat-completions", "openai/responses", "gemini"},
		"codex":  {"openai/chat-completions"},
		"chat":   {"openai/responses"},
	}

	for k, v := range registeredAvailability() {
		if existing, ok := out[k]; ok {
			out[k] = append(v, existing...)
		} else {
			out[k] = v
		}
	}
	return out
}
