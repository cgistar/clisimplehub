package transformer

import (
	"context"
	"fmt"
	"strings"

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

func Get(fromInterfaceType, transformerSpec string) (Transformer, error) {
	from := strings.ToLower(strings.TrimSpace(fromInterfaceType))
	spec := strings.ToLower(strings.TrimSpace(transformerSpec))
	if from == "" || spec == "" {
		return nil, fmt.Errorf("invalid transformer: from=%q spec=%q", fromInterfaceType, transformerSpec)
	}

	switch from {
	case "claude":
		return getFromClaude(spec)
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

// List returns canonical transformer specs for a given source interfaceType.
// The returned specs are valid inputs for `Get(from, spec)`.
func List(fromInterfaceType string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(fromInterfaceType)) {
	case "claude":
		return []string{
			"openai/chat-completions",
			"openai/responses",
			"gemini",
		}, nil
	case "codex":
		return []string{
			"openai/chat-completions",
		}, nil
	default:
		return nil, fmt.Errorf("unsupported transformer source interfaceType=%q", fromInterfaceType)
	}
}

// ListAll returns all supported transformer specs grouped by source interfaceType.
func ListAll() map[string][]string {
	return map[string][]string{
		"claude": {"openai/chat-completions", "openai/responses", "gemini"},
		"codex":  {"openai/chat-completions"},
	}
}
