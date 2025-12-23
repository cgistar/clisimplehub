package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleTransformers_ListAll(t *testing.T) {
	t.Parallel()

	p := &ProxyServer{}
	req := httptest.NewRequest(http.MethodGet, "/transformers", nil)
	rr := httptest.NewRecorder()

	p.handleTransformers(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "\"claude\"") {
		t.Fatalf("missing claude: %s", rr.Body.String())
	}
}

func TestHandleTransformers_ListFrom(t *testing.T) {
	t.Parallel()

	p := &ProxyServer{}
	req := httptest.NewRequest(http.MethodGet, "/transformers?from=claude", nil)
	rr := httptest.NewRecorder()

	p.handleTransformers(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "\"from\":\"claude\"") {
		t.Fatalf("missing from=claude: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "openai/chat-completions") {
		t.Fatalf("missing transformer: %s", rr.Body.String())
	}
}

func TestHandleTransformers_BadFrom(t *testing.T) {
	t.Parallel()

	p := &ProxyServer{}
	req := httptest.NewRequest(http.MethodGet, "/transformers?from=unknown", nil)
	rr := httptest.NewRecorder()

	p.handleTransformers(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}
