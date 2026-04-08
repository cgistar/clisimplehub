package entclawruntime

import (
	"bytes"
	"net/http"
	"testing"
)

func TestNormalizeRequestMapsMessagesRoute(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/v1/entclaw/messages", bytes.NewReader([]byte(`{"model":"gpt-5.4"}`)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	task, err := NormalizeRequest(req, []byte(`{"model":"gpt-5.4"}`))
	if err != nil {
		t.Fatalf("NormalizeRequest: %v", err)
	}
	if task.Channel != ChannelClaude {
		t.Fatalf("channel = %q, want %q", task.Channel, ChannelClaude)
	}
	if task.Format != FormatMessages {
		t.Fatalf("format = %q, want %q", task.Format, FormatMessages)
	}
	if !task.Stream {
		t.Fatal("expected stream mode to be forced on")
	}
}

func TestNormalizeRequestRejectsUnknownEntclawPath(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/v1/entclaw/unknown", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if _, err := NormalizeRequest(req, []byte(`{}`)); err == nil {
		t.Fatal("expected unsupported path error")
	}
}

func TestNormalizeRequestMapsChatCompletionsRoute(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/v1/entclaw/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-5.4"}`)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	task, err := NormalizeRequest(req, []byte(`{"model":"gpt-5.4"}`))
	if err != nil {
		t.Fatalf("NormalizeRequest: %v", err)
	}
	if task.Channel != ChannelChat {
		t.Fatalf("channel = %q, want %q", task.Channel, ChannelChat)
	}
	if task.Format != FormatChatCompletions {
		t.Fatalf("format = %q, want %q", task.Format, FormatChatCompletions)
	}
	if !task.Stream {
		t.Fatal("expected stream mode to be forced on")
	}
}

func TestNormalizeRequestMapsResponsesRoute(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/v1/entclaw/responses", bytes.NewReader([]byte(`{"model":"gpt-5.4"}`)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	task, err := NormalizeRequest(req, []byte(`{"model":"gpt-5.4"}`))
	if err != nil {
		t.Fatalf("NormalizeRequest: %v", err)
	}
	if task.Channel != ChannelCodex {
		t.Fatalf("channel = %q, want %q", task.Channel, ChannelCodex)
	}
	if task.Format != FormatResponses {
		t.Fatalf("format = %q, want %q", task.Format, FormatResponses)
	}
	if !task.Stream {
		t.Fatal("expected stream mode to be forced on")
	}
}
