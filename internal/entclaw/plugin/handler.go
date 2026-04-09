package entclawplugin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"reflect"
	"strings"

	entclawruntime "clisimplehub/internal/entclaw/runtime"
)

func (p *EntclawPlugin) handleInference(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeProtocolError(w, r.URL.Path, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if p.orchestrator == nil {
		writeProtocolError(w, r.URL.Path, http.StatusInternalServerError, "entclaw plugin not initialized")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeProtocolError(w, r.URL.Path, http.StatusBadRequest, "failed to read request body")
		return
	}

	task, err := entclawruntime.NormalizeRequest(r, body)
	if err != nil {
		writeProtocolError(w, r.URL.Path, http.StatusBadRequest, err.Error())
		return
	}

	result, err := p.orchestrator.Run(r.Context(), r, task)
	if err != nil {
		if task.Format == entclawruntime.FormatResponses && result != nil && len(result.Events) > 0 {
			if result.Response != nil {
				defer result.Response.Body.Close()
			}
			writeResponsesProgress(w, result)
			return
		}
		status := http.StatusInternalServerError
		var loopbackErr *entclawruntime.LoopbackStatusError
		if errors.As(err, &loopbackErr) && loopbackErr.StatusCode > 0 {
			status = loopbackErr.StatusCode
		}
		writeProtocolError(w, r.URL.Path, status, err.Error())
		return
	}
	if result == nil || result.Response == nil {
		writeProtocolError(w, r.URL.Path, http.StatusInternalServerError, "entclaw orchestrator returned no response")
		return
	}

	defer result.Response.Body.Close()
	if task.Format == entclawruntime.FormatResponses {
		writeResponsesProgress(w, result)
		return
	}

	if task.Format == entclawruntime.FormatChatCompletions || task.Format == entclawruntime.FormatMessages {
		finalBody, err := io.ReadAll(result.Response.Body)
		if err != nil {
			writeProtocolError(w, r.URL.Path, http.StatusInternalServerError, "failed to read final stream body")
			return
		}
		copyHeaders(w.Header(), result.Response.Header)
		status := result.Response.StatusCode
		if status <= 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		if task.Format == entclawruntime.FormatChatCompletions {
			_, _ = io.WriteString(w, entclawruntime.BuildChatProgressStream(result.Events, finalBody))
			return
		}
		_, _ = io.WriteString(w, entclawruntime.BuildMessagesProgressStream(result.Events, finalBody))
		return
	}

	copyHeaders(w.Header(), result.Response.Header)
	w.WriteHeader(result.Response.StatusCode)
	_, _ = io.Copy(w, result.Response.Body)
}

func writeResponsesProgress(w http.ResponseWriter, result *entclawruntime.RunResult) {
	if w == nil || result == nil {
		return
	}
	if result.Response != nil {
		copyHeaders(w.Header(), result.Response.Header)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	if strings.TrimSpace(w.Header().Get("Cache-Control")) == "" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	status := http.StatusOK
	if result.Response != nil && result.Response.StatusCode > 0 {
		status = result.Response.StatusCode
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, entclawruntime.BuildResponsesProgressStream(result.ResponseID, result.Events))
}

func (p *EntclawPlugin) handleSkills(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !skillStoreConfigured(p) {
			writeJSON(w, http.StatusInternalServerError, nil, errors.New("entclaw skill store not initialized"))
			return
		}
		names, err := p.skills.List(r.Context())
		writeJSON(w, http.StatusOK, names, err)
	case http.MethodPost, http.MethodPut:
		var input struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeJSON(w, http.StatusBadRequest, nil, err)
			return
		}
		if r.Method == http.MethodPut {
			input.Name = path.Base(r.URL.Path)
			if input.Name == "skills" || input.Name == "." || strings.TrimSpace(input.Name) == "" {
				writeJSON(w, http.StatusBadRequest, nil, errors.New("skill name is required in path"))
				return
			}
		}
		if !skillStoreConfigured(p) {
			writeJSON(w, http.StatusInternalServerError, nil, errors.New("entclaw skill store not initialized"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": strings.TrimSpace(input.Name)}, p.skills.Write(r.Context(), input.Name, input.Content))
	case http.MethodDelete:
		name := path.Base(r.URL.Path)
		if name == "skills" || name == "." || strings.TrimSpace(name) == "" {
			writeJSON(w, http.StatusBadRequest, nil, errors.New("skill name is required in path"))
			return
		}
		if !skillStoreConfigured(p) {
			writeJSON(w, http.StatusInternalServerError, nil, errors.New("entclaw skill store not initialized"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": name}, p.skills.Delete(r.Context(), name))
	default:
		writeJSON(w, http.StatusMethodNotAllowed, nil, errors.New("method not allowed"))
	}
}

func copyHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func skillStoreConfigured(p *EntclawPlugin) bool {
	return p != nil && !reflect.ValueOf(p.skills).IsZero()
}

func writeProtocolError(w http.ResponseWriter, requestPath string, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if strings.Contains(requestPath, "/messages") {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "invalid_request_error",
				"message": message,
			},
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "invalid_request_error",
			"code":    nil,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}
