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
	"strings"
	"time"
)

const runtimeControllerRequestTimeout = 2 * time.Second

type runtimeControllerConfig struct {
	baseURL string
	secret  string
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

	endpoint := cfg.baseURL + "/proxies/" + url.PathEscape(groupName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create runtime controller request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.secret != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.secret)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("set runtime group %s -> %s: %w", groupName, proxyName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	message := extractControllerErrorMessage(body)
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	if message == "" {
		message = resp.Status
	}
	return fmt.Errorf("set runtime group %s -> %s: %s", groupName, proxyName, message)
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
