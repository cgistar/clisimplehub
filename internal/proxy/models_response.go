package proxy

import (
	"net/http"
	"strings"
)

type staticOpenAIModel struct {
	ID      string
	Created int64
	OwnedBy string
}

type staticThinkingSupport struct {
	Min            int
	Max            int
	ZeroAllowed    bool
	DynamicAllowed bool
}

type staticClaudeModel struct {
	ID          string
	CreatedAt   int64
	OwnedBy     string
	DisplayName string
	Thinking    *staticThinkingSupport
}

var openAIModelsCatalog = []staticOpenAIModel{
	{ID: "gpt-5", Created: 1754524800, OwnedBy: "openai"},
	{ID: "gpt-5-codex", Created: 1757894400, OwnedBy: "openai"},
	{ID: "gpt-5-codex-mini", Created: 1762473600, OwnedBy: "openai"},
	{ID: "gpt-5.1", Created: 1762905600, OwnedBy: "openai"},
	{ID: "gpt-5.1-codex", Created: 1762905600, OwnedBy: "openai"},
	{ID: "gpt-5.1-codex-mini", Created: 1762905600, OwnedBy: "openai"},
	{ID: "gpt-5.1-codex-max", Created: 1763424000, OwnedBy: "openai"},
	{ID: "gpt-5.2", Created: 1765440000, OwnedBy: "openai"},
	{ID: "gpt-5.2-codex", Created: 1765440000, OwnedBy: "openai"},
	{ID: "gpt-5.3-codex", Created: 1770307200, OwnedBy: "openai"},
	{ID: "gpt-5.3-codex-spark", Created: 1770912000, OwnedBy: "openai"},
	{ID: "gpt-5.4", Created: 1772668800, OwnedBy: "openai"},
}

var claudeModelsCatalog = []staticClaudeModel{
	{
		ID:          "claude-haiku-4-5-20251001",
		CreatedAt:   1759276800,
		OwnedBy:     "anthropic",
		DisplayName: "Claude 4.5 Haiku",
		Thinking:    &staticThinkingSupport{Min: 1024, Max: 128000, ZeroAllowed: true, DynamicAllowed: false},
	},
	{
		ID:          "claude-sonnet-4-5-20250929",
		CreatedAt:   1759104000,
		OwnedBy:     "anthropic",
		DisplayName: "Claude 4.5 Sonnet",
		Thinking:    &staticThinkingSupport{Min: 1024, Max: 128000, ZeroAllowed: true, DynamicAllowed: false},
	},
	{
		ID:          "claude-sonnet-4-6",
		CreatedAt:   1771372800,
		OwnedBy:     "anthropic",
		DisplayName: "Claude 4.6 Sonnet",
		Thinking:    &staticThinkingSupport{Min: 1024, Max: 128000, ZeroAllowed: true, DynamicAllowed: false},
	},
	{
		ID:          "claude-opus-4-6",
		CreatedAt:   1770318000,
		OwnedBy:     "anthropic",
		DisplayName: "Claude 4.6 Opus",
		Thinking:    &staticThinkingSupport{Min: 1024, Max: 128000, ZeroAllowed: true, DynamicAllowed: false},
	},
	{
		ID:          "claude-opus-4-5-20251101",
		CreatedAt:   1761955200,
		OwnedBy:     "anthropic",
		DisplayName: "Claude 4.5 Opus",
		Thinking:    &staticThinkingSupport{Min: 1024, Max: 128000, ZeroAllowed: true, DynamicAllowed: false},
	},
	{
		ID:          "claude-opus-4-1-20250805",
		CreatedAt:   1722945600,
		OwnedBy:     "anthropic",
		DisplayName: "Claude 4.1 Opus",
		Thinking:    &staticThinkingSupport{Min: 1024, Max: 128000, ZeroAllowed: false, DynamicAllowed: false},
	},
	{
		ID:          "claude-opus-4-20250514",
		CreatedAt:   1715644800,
		OwnedBy:     "anthropic",
		DisplayName: "Claude 4 Opus",
		Thinking:    &staticThinkingSupport{Min: 1024, Max: 128000, ZeroAllowed: false, DynamicAllowed: false},
	},
	{
		ID:          "claude-sonnet-4-20250514",
		CreatedAt:   1715644800,
		OwnedBy:     "anthropic",
		DisplayName: "Claude 4 Sonnet",
		Thinking:    &staticThinkingSupport{Min: 1024, Max: 128000, ZeroAllowed: false, DynamicAllowed: false},
	},
	{
		ID:          "claude-3-7-sonnet-20250219",
		CreatedAt:   1708300800,
		OwnedBy:     "anthropic",
		DisplayName: "Claude 3.7 Sonnet",
		Thinking:    &staticThinkingSupport{Min: 1024, Max: 128000, ZeroAllowed: false, DynamicAllowed: false},
	},
	{
		ID:          "claude-3-5-haiku-20241022",
		CreatedAt:   1729555200,
		OwnedBy:     "anthropic",
		DisplayName: "Claude 3.5 Haiku",
	},
}

func handleUnifiedModelsRequest(w http.ResponseWriter, r *http.Request) bool {
	if r == nil {
		return false
	}
	if !IsUnifiedModelsPath(r.URL.Path) || !strings.EqualFold(r.Method, http.MethodGet) {
		return false
	}

	userAgent := r.Header.Get("User-Agent")
	if strings.HasPrefix(userAgent, "claude-cli") {
		writeClaudeModelsResponse(w)
		return true
	}

	writeOpenAIModelsResponse(w)
	return true
}

func (p *ProxyServer) handleUnifiedModelsRoute(w http.ResponseWriter, r *http.Request) {
	_ = handleUnifiedModelsRequest(w, r)
}

func writeOpenAIModelsResponse(w http.ResponseWriter) {
	models := make([]map[string]any, 0, len(openAIModelsCatalog))
	for _, model := range openAIModelsCatalog {
		models = append(models, map[string]any{
			"id":       model.ID,
			"object":   "model",
			"created":  model.Created,
			"owned_by": model.OwnedBy,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   models,
	})
}

func writeClaudeModelsResponse(w http.ResponseWriter) {
	models := make([]map[string]any, 0, len(claudeModelsCatalog))
	firstID := ""
	lastID := ""

	for i, model := range claudeModelsCatalog {
		entry := map[string]any{
			"id":           model.ID,
			"object":       "model",
			"owned_by":     model.OwnedBy,
			"created_at":   model.CreatedAt,
			"type":         "model",
			"display_name": model.DisplayName,
		}

		if model.Thinking != nil {
			entry["thinking"] = true
			entry["extended_thinking"] = map[string]any{
				"supported":       true,
				"min":             model.Thinking.Min,
				"max":             model.Thinking.Max,
				"zero_allowed":    model.Thinking.ZeroAllowed,
				"dynamic_allowed": model.Thinking.DynamicAllowed,
			}
		}

		models = append(models, entry)
		if i == 0 {
			firstID = model.ID
		}
		lastID = model.ID
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data":     models,
		"has_more": false,
		"first_id": firstID,
		"last_id":  lastID,
	})
}
