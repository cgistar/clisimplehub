package proxy

import (
	"encoding/json"
	"net/http"
	"strings"

	"clisimplehub/internal/transformer"
)

// handleTransformers 提供 transformer 查询接口。
//
// GET /transformers
// - 返回全部：{"transformers": {"claude":[...], "codex":[...]}}
//
// GET /transformers?from=claude
// - 返回指定来源：{"from":"claude","transformers":[...]}
func (p *ProxyServer) handleTransformers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "method not allowed"})
		return
	}

	from := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("from")))
	if from == "" {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"transformers": transformer.ListAll(),
		})
		return
	}

	list, err := transformer.List(from)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"from":         from,
		"transformers": list,
	})
}
