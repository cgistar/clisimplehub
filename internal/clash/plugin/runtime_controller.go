package clashplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const runtimeControllerRequestTimeout = 2 * time.Second

const (
	runtimeControllerSuccessBodyLimit = 1 << 20
	runtimeControllerErrorBodyLimit   = 4 << 10
)

type runtimeControllerConfig struct {
	baseURL string
	secret  string
}

type runtimeDelayResponse struct {
	Delay int `json:"delay"`
}

type runtimeProxyResponse struct {
	Now string `json:"now"`
}

type runtimeControllerError struct {
	StatusCode int
	Message    string
}

func (e *runtimeControllerError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.StatusCode > 0 {
		return http.StatusText(e.StatusCode)
	}
	return "runtime controller request failed"
}

func resolveRuntimeControllerConfig(cfg *ClashConfig) (*runtimeControllerConfig, error) {
	runtimeCfg, err := mergeRuntimeConfigWithUserYAML(buildRuntimeBaseConfig(cfg), cfg)
	if err != nil {
		return nil, err
	}

	rawController := strings.TrimSpace(fmt.Sprint(runtimeCfg["external-controller"]))
	if rawController == "" || rawController == "<nil>" {
		return nil, fmt.Errorf("external-controller is empty")
	}

	baseURL, err := normalizeRuntimeControllerURL(rawController)
	if err != nil {
		return nil, err
	}

	secret := strings.TrimSpace(fmt.Sprint(runtimeCfg["secret"]))
	if secret == "<nil>" {
		secret = ""
	}

	return &runtimeControllerConfig{
		baseURL: baseURL,
		secret:  secret,
	}, nil
}

func normalizeRuntimeControllerURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("external-controller is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse external-controller: %w", err)
	}
	if parsed.Scheme == "" {
		parsed.Scheme = "http"
	}

	host := parsed.Hostname()
	port := parsed.Port()
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	if port == "" {
		return "", fmt.Errorf("external-controller missing port: %s", raw)
	}
	parsed.Host = net.JoinHostPort(host, port)
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func applyRuntimeSelections(ctx context.Context, cfg *ClashConfig, plan *runtimePlan) error {
	if plan == nil {
		return nil
	}

	controllerCfg, err := resolveRuntimeControllerConfig(cfg)
	if err != nil {
		return err
	}

	if plan.exitSelection != "" {
		if err := putRuntimeGroupSelection(ctx, controllerCfg, runtimeGroupExit, plan.exitSelection); err != nil {
			return err
		}
	}
	if plan.middleSelection != "" {
		if err := putRuntimeGroupSelection(ctx, controllerCfg, runtimeGroupMiddle, plan.middleSelection); err != nil {
			return err
		}
	}
	for _, subscription := range plan.subscriptions {
		if subscription.groupName == "" || subscription.selection == "" {
			continue
		}
		if err := putRuntimeGroupSelection(ctx, controllerCfg, subscription.groupName, subscription.selection); err != nil {
			return err
		}
	}
	if plan.trafficGroup != "" && plan.trafficSelection != "" {
		if err := putRuntimeGroupSelection(ctx, controllerCfg, plan.trafficGroup, plan.trafficSelection); err != nil {
			return err
		}
	}
	return nil
}

func applyRuntimeSelectionsWithRetry(cfg *ClashConfig, plan *runtimePlan, attempts int, delay time.Duration) error {
	if attempts <= 0 {
		attempts = 1
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), runtimeControllerRequestTimeout)
		lastErr = applyRuntimeSelections(ctx, cfg, plan)
		cancel()
		if lastErr == nil {
			return nil
		}
		if i+1 < attempts {
			time.Sleep(delay)
		}
	}
	return lastErr
}

func putRuntimeGroupSelection(ctx context.Context, cfg *runtimeControllerConfig, groupName, proxyName string) error {
	if cfg == nil {
		return fmt.Errorf("runtime controller config is nil")
	}

	payload, err := json.Marshal(map[string]string{"name": proxyName})
	if err != nil {
		return fmt.Errorf("marshal controller payload: %w", err)
	}

	_, err = doRuntimeControllerRequest(ctx, cfg, http.MethodPut, "/proxies/"+url.PathEscape(groupName), nil, bytes.NewReader(payload), "application/json")
	if err != nil {
		return fmt.Errorf("set runtime group %s -> %s: %w", groupName, proxyName, err)
	}
	return nil
}

func getRuntimeProxyDelay(ctx context.Context, cfg *runtimeControllerConfig, proxyName, targetURL string, timeout time.Duration) (int, error) {
	if cfg == nil {
		return -1, fmt.Errorf("runtime controller config is nil")
	}
	if strings.TrimSpace(proxyName) == "" {
		return -1, fmt.Errorf("proxy name is empty")
	}
	if strings.TrimSpace(targetURL) == "" {
		return -1, fmt.Errorf("target url is empty")
	}

	timeoutMS := timeout.Milliseconds()
	if timeoutMS <= 0 {
		timeoutMS = 1
	}

	query := url.Values{}
	query.Set("url", targetURL)
	query.Set("timeout", strconv.FormatInt(timeoutMS, 10))

	body, err := doRuntimeControllerRequest(ctx, cfg, http.MethodGet, "/proxies/"+url.PathEscape(proxyName)+"/delay", query, nil, "")
	if err != nil {
		return -1, fmt.Errorf("get runtime proxy delay %s: %w", proxyName, err)
	}

	var payload runtimeDelayResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return -1, fmt.Errorf("decode runtime delay response: %w", err)
	}
	if payload.Delay <= 0 {
		return -1, fmt.Errorf("empty delay result")
	}
	return payload.Delay, nil
}

func runtimeControllerHasProxy(ctx context.Context, cfg *runtimeControllerConfig, proxyName string) (bool, error) {
	if cfg == nil {
		return false, fmt.Errorf("runtime controller config is nil")
	}
	if strings.TrimSpace(proxyName) == "" {
		return false, fmt.Errorf("proxy name is empty")
	}

	body, err := doRuntimeControllerRequest(ctx, cfg, http.MethodGet, "/proxies", nil, nil, "")
	if err != nil {
		return false, fmt.Errorf("list runtime proxies: %w", err)
	}

	var payload struct {
		Proxies map[string]json.RawMessage `json:"proxies"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false, fmt.Errorf("decode runtime proxies response: %w", err)
	}
	_, exists := payload.Proxies[proxyName]
	return exists, nil
}

func getRuntimeProxyNow(ctx context.Context, cfg *runtimeControllerConfig, proxyName string) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("runtime controller config is nil")
	}
	if strings.TrimSpace(proxyName) == "" {
		return "", fmt.Errorf("proxy name is empty")
	}

	body, err := doRuntimeControllerRequest(ctx, cfg, http.MethodGet, "/proxies/"+url.PathEscape(proxyName), nil, nil, "")
	if err != nil {
		return "", err
	}

	var payload runtimeProxyResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode runtime proxy response: %w", err)
	}
	return strings.TrimSpace(payload.Now), nil
}

func doRuntimeControllerRequest(ctx context.Context, cfg *runtimeControllerConfig, method, path string, query url.Values, body io.Reader, contentType string) ([]byte, error) {
	if cfg == nil {
		return nil, fmt.Errorf("runtime controller config is nil")
	}
	endpoint, err := buildRuntimeControllerEndpoint(cfg, path, query)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("create runtime controller request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if cfg.secret != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.secret)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyLimit := int64(runtimeControllerErrorBodyLimit)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		bodyLimit = runtimeControllerSuccessBodyLimit
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, bodyLimit))
	if err != nil {
		return nil, fmt.Errorf("read runtime controller response: %w", err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return respBody, nil
	}

	message := extractControllerErrorMessage(respBody)
	if message == "" {
		message = strings.TrimSpace(string(respBody))
	}
	if message == "" {
		message = resp.Status
	}
	return nil, &runtimeControllerError{
		StatusCode: resp.StatusCode,
		Message:    fmt.Sprintf("%s %s: %s", method, path, message),
	}
}

func buildRuntimeControllerEndpoint(cfg *runtimeControllerConfig, path string, query url.Values) (string, error) {
	baseURL, err := url.Parse(cfg.baseURL)
	if err != nil {
		return "", fmt.Errorf("parse runtime controller base url: %w", err)
	}
	path = "/" + strings.TrimLeft(strings.TrimSpace(path), "/")
	decodedPath, err := url.PathUnescape(path)
	if err != nil {
		return "", fmt.Errorf("unescape runtime controller path: %w", err)
	}
	basePath := strings.TrimRight(baseURL.Path, "/")
	baseURL.Path = basePath + decodedPath
	baseURL.RawPath = basePath + path
	baseURL.RawQuery = query.Encode()
	return baseURL.String(), nil
}

func extractControllerErrorMessage(body []byte) string {
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Message)
}
