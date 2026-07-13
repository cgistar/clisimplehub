package xaiplugin

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"clisimplehub/internal/executor"
	xaiBackend "clisimplehub/internal/xai/backend"

	"github.com/tidwall/gjson"
)

// HandleImages 按 OpenAI images handler 契约转换输入和输出。
func (s *XaiService) HandleImages(w http.ResponseWriter, r *http.Request, edits bool) {
	raw, images, err := readImageRequest(r, edits)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error(), "invalid_request_error", "invalid_image_request")
		return
	}
	model := strings.TrimSpace(gjson.GetBytes(raw, "model").String())
	if !xaiBackend.IsXAIImageModel(model) {
		writeAPIError(w, http.StatusBadRequest, fmt.Sprintf("Model %s is not supported on xAI images endpoints", model), "invalid_request_error", "unsupported_model")
		return
	}
	if strings.TrimSpace(gjson.GetBytes(raw, "prompt").String()) == "" {
		writeAPIError(w, http.StatusBadRequest, "prompt is required", "invalid_request_error", "missing_prompt")
		return
	}
	if edits && len(images) == 0 {
		writeAPIError(w, http.StatusBadRequest, "at least one image is required", "invalid_request_error", "missing_image")
		return
	}
	format := xaiBackend.NormalizeImageResponseFormat(gjson.GetBytes(raw, "response_format").String())
	upstream := xaiBackend.BuildXAIImageGeneration(raw, model, format)
	path, eventType := "/xai/v1/images/generations", "image_generation.completed"
	if edits {
		upstream = xaiBackend.BuildXAIImageEdit(raw, model, format, images)
		path, eventType = "/xai/v1/images/edits", "image_edit.completed"
	}
	stream := gjson.GetBytes(raw, "stream").Bool()
	result := s.RoundTrip(r.Context(), &executor.UpstreamRequest{Method: http.MethodPost, TargetPath: path, OriginalPath: path, Headers: r.Header.Clone(), Body: upstream, RequestModel: model, TargetInterfaceType: "xai"})
	if result == nil {
		writeAPIError(w, http.StatusBadGateway, "xAI image request failed", "upstream_error", "image_upstream_error")
		return
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		writeUpstreamResult(w, result)
		return
	}
	out, err := xaiBackend.BuildOpenAIImageResponse(result.Body, format)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error(), "upstream_error", "invalid_image_response")
		return
	}
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		usage := gjson.GetBytes(out, "usage")
		for _, img := range gjson.GetBytes(out, "data").Array() {
			event := map[string]any{"type": eventType}
			if v := img.Get("url").String(); v != "" {
				event["url"] = v
			}
			if v := img.Get("b64_json").String(); v != "" {
				event["b64_json"] = v
			}
			if usage.IsObject() {
				var u any
				_ = json.Unmarshal([]byte(usage.Raw), &u)
				event["usage"] = u
			}
			payload, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, payload)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

func readImageRequest(r *http.Request, edits bool) ([]byte, []string, error) {
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(ct), "multipart/form-data") {
		raw, err := io.ReadAll(r.Body)
		if err != nil || !json.Valid(raw) {
			return nil, nil, fmt.Errorf("request body must be valid JSON")
		}
		return raw, xaiBackend.CollectXAIImages(raw), nil
	}
	if !edits {
		return nil, nil, fmt.Errorf("multipart is only supported for image edits")
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return nil, nil, fmt.Errorf("invalid multipart form: %w", err)
	}
	fields := map[string]any{}
	for _, k := range []string{"model", "prompt", "response_format", "size", "aspect_ratio", "resolution"} {
		if v := strings.TrimSpace(r.FormValue(k)); v != "" {
			fields[k] = v
		}
	}
	if v := strings.TrimSpace(r.FormValue("n")); v != "" {
		fields["n"] = json.Number(v)
	}
	if v := strings.TrimSpace(r.FormValue("stream")); v != "" {
		fields["stream"] = strings.EqualFold(v, "true") || v == "1"
	}
	var images []string
	for _, key := range []string{"image", "image[]"} {
		for _, fh := range r.MultipartForm.File[key] {
			f, err := fh.Open()
			if err != nil {
				return nil, nil, err
			}
			b, readErr := io.ReadAll(io.LimitReader(f, 32<<20))
			_ = f.Close()
			if readErr != nil {
				return nil, nil, readErr
			}
			mt := fh.Header.Get("Content-Type")
			if mt == "" || strings.EqualFold(mt, "application/octet-stream") {
				mt = mime.TypeByExtension(strings.ToLower(filepath.Ext(strings.TrimSpace(fh.Filename))))
			}
			if mt == "" {
				mt = http.DetectContentType(b)
			}
			images = append(images, "data:"+mt+";base64,"+base64.StdEncoding.EncodeToString(b))
		}
	}
	raw, err := json.Marshal(fields)
	return raw, images, err
}

func writeAPIError(w http.ResponseWriter, status int, message, typ, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": message, "type": typ, "code": code}})
}
