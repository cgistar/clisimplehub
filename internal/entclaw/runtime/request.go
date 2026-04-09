package entclawruntime

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type Channel string
type RequestFormat string

const (
	ChannelClaude Channel = "claude"
	ChannelChat   Channel = "chat"
	ChannelCodex  Channel = "codex"

	FormatMessages        RequestFormat = "messages"
	FormatChatCompletions RequestFormat = "chat_completions"
	FormatResponses       RequestFormat = "responses"
)

type TaskRequest struct {
	SessionID string
	Channel   Channel
	Format    RequestFormat
	Model     string
	InputRaw  json.RawMessage
	Stream    bool
	RawBody   []byte
	Headers   http.Header
	Path      string
}

func NormalizeRequest(r *http.Request, body []byte) (*TaskRequest, error) {
	channel, format, err := mapEntclawPath(r.URL.Path)
	if err != nil {
		return nil, err
	}

	var payload struct {
		SessionID string `json:"session_id"`
		Model     string `json:"model"`
		Input     json.RawMessage `json:"input"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("decode request body: %w", err)
		}
	}

	rawBody := append([]byte(nil), body...)
	if sanitized, err := stripSessionIDFromBody(body); err == nil {
		rawBody = sanitized
	}

	return &TaskRequest{
		SessionID: strings.TrimSpace(payload.SessionID),
		Channel:   channel,
		Format:    format,
		Model:     strings.TrimSpace(payload.Model),
		InputRaw:  append(json.RawMessage(nil), payload.Input...),
		Stream:    true,
		RawBody:   rawBody,
		Headers:   r.Header.Clone(),
		Path:      r.URL.Path,
	}, nil
}

func stripSessionIDFromBody(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return []byte{}, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	delete(payload, "session_id")
	return json.Marshal(payload)
}

func mapEntclawPath(path string) (Channel, RequestFormat, error) {
	switch strings.TrimRight(strings.ToLower(strings.TrimSpace(path)), "/") {
	case "/v1/entclaw/messages":
		return ChannelClaude, FormatMessages, nil
	case "/v1/entclaw/chat/completions":
		return ChannelChat, FormatChatCompletions, nil
	case "/v1/entclaw/responses":
		return ChannelCodex, FormatResponses, nil
	default:
		return "", "", fmt.Errorf("unsupported entclaw route: %s", path)
	}
}
