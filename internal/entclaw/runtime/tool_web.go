package entclawruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"time"
)

const (
	defaultWebFetchMaxChars = 20_000
	defaultWebFetchTimeout  = 15 * time.Second
)

type WebSearchRequest struct {
	Query string `json:"query"`
}

type WebFetchRequest struct {
	URL      string `json:"url"`
	MaxChars int    `json:"maxChars"`
}

func (r *ToolRuntime) executeWebSearch(ctx context.Context, raw json.RawMessage) (ToolResult, error) {
	var input WebSearchRequest
	if err := json.Unmarshal(rawJSONObjectOrEmpty(raw), &input); err != nil {
		return ToolResult{}, err
	}
	if strings.TrimSpace(input.Query) == "" {
		return errorToolResult(fmt.Errorf("query is required")), nil
	}

	providers := r.findMaterializedMCPToolsByToolName(ctx, "web_search")
	if len(providers) == 0 {
		return errorToolResult(fmt.Errorf("web_search is enabled but no provider is currently available.")), nil
	}

	output, err := r.invokeMaterializedMCPTool(ctx, providers[0], raw)
	return marshalToolPayload(map[string]any{
		"provider": providers[0].ServerName,
		"query":    strings.TrimSpace(input.Query),
		"output":   json.RawMessage(rawJSONObjectOrEmpty(output)),
	}, err)
}

func executeWebFetch(ctx context.Context, raw json.RawMessage) (ToolResult, error) {
	var input WebFetchRequest
	if err := json.Unmarshal(rawJSONObjectOrEmpty(raw), &input); err != nil {
		return ToolResult{}, err
	}

	target, err := parseWebFetchURL(input.URL)
	if err != nil {
		return errorToolResult(err), nil
	}

	maxChars := input.MaxChars
	if maxChars <= 0 {
		maxChars = defaultWebFetchMaxChars
	}

	reqCtx, cancel := context.WithTimeout(ctx, defaultWebFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, nil)
	if err != nil {
		return errorToolResult(fmt.Errorf("build web_fetch request: %w", err)), nil
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errorToolResult(fmt.Errorf("web_fetch request failed: %w", err)), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxChars+1)))
	if err != nil {
		return errorToolResult(fmt.Errorf("read web_fetch response: %w", err)), nil
	}

	content := string(body)
	truncated := false
	if len(content) > maxChars {
		content = content[:maxChars]
		truncated = true
	}

	return marshalToolPayload(map[string]any{
		"url":         target,
		"statusCode":  resp.StatusCode,
		"contentType": resp.Header.Get("Content-Type"),
		"content":     content,
		"truncated":   truncated,
	}, nil)
}

func parseWebFetchURL(raw string) (string, error) {
	target := strings.TrimSpace(raw)
	if target == "" {
		return "", fmt.Errorf("url is required")
	}

	parsed, err := neturl.Parse(target)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("web_fetch only supports http and https urls")
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("url host is required")
	}
	return parsed.String(), nil
}
