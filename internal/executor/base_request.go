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

	basePath := normalizeURLPath(u.Path)
	requestPath := normalizeURLPath(path)

	// 兼容用户将 endpoint api_url 配置为完整接口地址的场景（含 gemini 的动态路径）：
	// 若 api_url 已经包含目标接口后缀，则不再重复拼接 transformer 的 TargetPath。
	if shouldKeepEndpointPath(basePath, requestPath) {
		u.Path = basePath
		u.RawQuery = rawQuery
		return u.String(), nil
	}

	u.Path = joinURLPaths(basePath, requestPath)
	u.RawQuery = rawQuery

	return u.String(), nil
}

func BuildTargetURL(apiURL, path, rawQuery string) (string, error) {
	return buildTargetURL(apiURL, path, rawQuery)
}

func normalizeURLPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return strings.TrimRight(p, "/")
}

func joinURLPaths(basePath, requestPath string) string {
	basePath = normalizeURLPath(basePath)
	requestPath = normalizeURLPath(requestPath)
	if requestPath == "" {
		return basePath
	}
	if basePath == "" {
		return requestPath
	}
	if basePath == requestPath || strings.HasSuffix(basePath, requestPath) {
		return basePath
	}

	baseSegs := strings.Split(strings.Trim(basePath, "/"), "/")
	reqSegs := strings.Split(strings.Trim(requestPath, "/"), "/")

	overlap := 0
	maxOverlap := len(baseSegs)
	if len(reqSegs) < maxOverlap {
		maxOverlap = len(reqSegs)
	}
	for k := maxOverlap; k > 0; k-- {
		if equalStringSlice(baseSegs[len(baseSegs)-k:], reqSegs[:k]) {
			overlap = k
			break
		}
	}

	joined := append(baseSegs, reqSegs[overlap:]...)
	return "/" + strings.Join(joined, "/")
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func shouldKeepEndpointPath(basePath, requestPath string) bool {
	basePath = normalizeURLPath(basePath)
	requestPath = normalizeURLPath(requestPath)
	if basePath == "" || requestPath == "" {
		return false
	}

	for _, suffix := range endpointSuffixCandidates(requestPath) {
		if suffix == "" {
			continue
		}
		if basePath == suffix || strings.HasSuffix(basePath, suffix) {
			return true
		}
	}
	return false
}

func endpointSuffixCandidates(requestPath string) []string {
	requestPath = normalizeURLPath(requestPath)
	if requestPath == "" {
		return nil
	}

	segs := strings.Split(strings.Trim(requestPath, "/"), "/")
	if len(segs) == 0 {
		return []string{requestPath}
	}

	candidates := []string{requestPath}

	// 允许 endpoint api_url 省略版本前缀（如 /v1、/v1beta），只配置到资源路径本身。
	if isAPIVersionSegment(segs[0]) && len(segs) > 1 {
		candidates = append(candidates, "/"+strings.Join(segs[1:], "/"))
	}

	// 兼容部分网关将 chat-completions 直接暴露为 /completions。
	if len(segs) >= 2 && segs[len(segs)-1] == "completions" && segs[len(segs)-2] == "chat" {
		candidates = append(candidates, "/completions")
	}

	return uniqueStrings(candidates)
}

func isAPIVersionSegment(seg string) bool {
	seg = strings.ToLower(strings.TrimSpace(seg))
	if seg == "" || seg[0] != 'v' {
		return false
	}

	i := 1
	for i < len(seg) && seg[i] >= '0' && seg[i] <= '9' {
		i++
	}
	if i == 1 {
		return false
	}

	suffix := seg[i:]
	return suffix == "" || suffix == "alpha" || suffix == "beta"
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
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
