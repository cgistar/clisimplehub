package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
)

// writeJSON writes a JSON response without escaping '<', '>' and '&' into \u003c, \u003e, \u0026.
// This keeps captured request/response payloads copy-pastable for replay/debugging.
func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	if w == nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}

func marshalJSONNoEscapeHTML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	b := buf.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b, nil
}

