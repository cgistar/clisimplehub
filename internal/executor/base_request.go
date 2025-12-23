package executor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func buildTargetURL(apiURL, path, rawQuery string) (string, error) {
	base := strings.TrimSpace(apiURL)
	if base == "" {
		return "", fmt.Errorf("empty api_url")
	}

	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "https://" + base
	}
	base = strings.TrimRight(base, "/")

	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid api_url: %w", err)
	}

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = rawQuery

	return u.String(), nil
}

func applyModelMapping(body []byte, endpoint *EndpointConfig) []byte {
	if endpoint == nil || (len(endpoint.Models) == 0 && endpoint.Model == "") {
		return body
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}

	requestModel, _ := req["model"].(string)
	if strings.TrimSpace(requestModel) == "" {
		if endpoint.Model != "" {
			req["model"] = endpoint.Model
			if result, err := json.Marshal(req); err == nil {
				return result
			}
		}
		return body
	}

	upstreamModel := ResolveUpstreamModel(requestModel, endpoint)
	if upstreamModel == requestModel {
		return body
	}
	req["model"] = upstreamModel
	if result, err := json.Marshal(req); err == nil {
		return result
	}
	return body
}

func copyRequestHeaders(dst *http.Request, src http.Header) {
	skipHeaders := map[string]bool{
		"host":            true,
		"accept-encoding": true,
		"content-length":  true,
		"authorization":   true,
		"x-api-key":       true,
	}

	for key, values := range src {
		if skipHeaders[strings.ToLower(key)] {
			continue
		}
		for _, v := range values {
			dst.Header.Add(key, v)
		}
	}
}

func sanitizeHeaders(h http.Header) map[string]string {
	result := make(map[string]string)
	sensitiveKeys := map[string]bool{
		"authorization": true,
		"x-api-key":     true,
		"api-key":       true,
	}

	for key, values := range h {
		if sensitiveKeys[strings.ToLower(key)] {
			if len(values) > 0 && len(values[0]) > 8 {
				result[key] = values[0][:4] + "****" + values[0][len(values[0])-4:]
			} else {
				result[key] = "****"
			}
			continue
		}
		if len(values) > 0 {
			result[key] = values[0]
		}
	}
	return result
}
