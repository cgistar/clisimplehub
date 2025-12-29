package proxy

import (
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
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	from := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("from")))
	if from == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"transformers": transformer.ListAll(),
		})
		return
	}

	list, err := transformer.List(from)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"from":         from,
		"transformers": list,
	})
}
