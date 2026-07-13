package xaiplugin

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"clisimplehub/internal/executor"
	xaiBackend "clisimplehub/internal/xai/backend"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type videoBinding struct {
	AccountID, Model string
	ExpiresAt        time.Time
}

var xaiVideoBindings = struct {
	sync.Mutex
	items map[string]videoBinding
}{items: make(map[string]videoBinding)}

func validVideoModel(model string) bool {
	model = strings.ToLower(xaiBackend.BaseModelName(model))
	return model == "grok-imagine-video" || model == "grok-imagine-video-1.5-preview"
}

func (s *XaiService) HandleVideos(w http.ResponseWriter, r *http.Request) {
	path := normalizeXaiRoutePath(r.URL.Path)
	body := []byte(nil)
	model, preferred := "", ""
	if r.Method == http.MethodPost {
		var err error
		body, err = readAllAndClose(r)
		if err != nil || !json.Valid(body) {
			writeAPIError(w, http.StatusBadRequest, "request body must be valid JSON", "invalid_request_error", "invalid_json")
			return
		}
		model = strings.TrimSpace(gjson.GetBytes(body, "model").String())
		if model == "" {
			model = "grok-imagine-video"
			body, _ = sjson.SetBytes(body, "model", model)
		}
		if !validVideoModel(model) {
			writeAPIError(w, http.StatusBadRequest, "unsupported xAI video model", "invalid_request_error", "unsupported_model")
			return
		}
	} else {
		id := strings.TrimPrefix(path, "/xai/v1/videos/")
		xaiVideoBindings.Lock()
		b, ok := xaiVideoBindings.items[id]
		if ok && time.Now().Before(b.ExpiresAt) {
			preferred, model = b.AccountID, b.Model
		} else if ok {
			delete(xaiVideoBindings.items, id)
		}
		xaiVideoBindings.Unlock()
	}
	meta := map[string]any{}
	if preferred != "" {
		meta["xai_preferred_account_id"] = preferred
	}
	result := s.RoundTrip(r.Context(), &executor.UpstreamRequest{Method: r.Method, TargetPath: path, OriginalPath: path, RawQuery: r.URL.RawQuery, Headers: r.Header.Clone(), Body: body, RequestModel: model, TargetInterfaceType: "xai", TransformContext: &executor.TransformContext{Metadata: meta}})
	if result != nil && r.Method == http.MethodPost && result.StatusCode >= 200 && result.StatusCode < 300 {
		id := strings.TrimSpace(gjson.GetBytes(result.Body, "request_id").String())
		if id == "" {
			id = strings.TrimSpace(gjson.GetBytes(result.Body, "id").String())
		}
		if id != "" {
			accountID := result.Headers.Get("X-Clisimplehub-XAI-Account-ID")
			xaiVideoBindings.Lock()
			xaiVideoBindings.items[id] = videoBinding{AccountID: accountID, Model: xaiBackend.BaseModelName(model), ExpiresAt: time.Now().Add(3 * time.Hour)}
			xaiVideoBindings.Unlock()
		}
	}
	writeUpstreamResult(w, result)
}

func readAllAndClose(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	var v json.RawMessage
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return []byte(v), nil
}
