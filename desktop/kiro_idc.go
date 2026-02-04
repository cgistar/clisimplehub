package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"clisimplehub/internal/executor"
)

// =============================================================================
// Kiro IDC Device Flow Authentication Methods
// =============================================================================

// IdcRegisterRequest represents the request to register an OIDC client
type IdcRegisterRequest struct {
	Region string `json:"region,omitempty"`
}

// IdcRegisterResponse represents the response from OIDC client registration
type IdcRegisterResponse struct {
	ClientId     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// IdcDeviceAuthRequest represents the request to start device authorization
type IdcDeviceAuthRequest struct {
	ClientId     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	Region       string `json:"region,omitempty"`
	StartUrl     string `json:"startUrl,omitempty"`
}

// IdcDeviceAuthResponse represents the response from device authorization
type IdcDeviceAuthResponse struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationUri         string `json:"verificationUri"`
	VerificationUriComplete string `json:"verificationUriComplete"`
	ExpiresIn               int    `json:"expiresIn"`
	Interval                int    `json:"interval"`
}

// IdcPollTokenRequest represents the request to poll for token
type IdcPollTokenRequest struct {
	ClientId     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	DeviceCode   string `json:"deviceCode"`
	Region       string `json:"region,omitempty"`
}

// IdcPollTokenResponse represents the response from token polling
type IdcPollTokenResponse struct {
	AccessToken  string `json:"accessToken,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresIn    int    `json:"expiresIn,omitempty"`
	TokenType    string `json:"tokenType,omitempty"`
	Error        string `json:"error,omitempty"`
}

// getIdcOidcURL returns the OIDC URL for the given region
func getIdcOidcURL(region string) string {
	region = strings.TrimSpace(region)
	if region == "" {
		region = "us-east-1"
	}
	return fmt.Sprintf("https://oidc.%s.amazonaws.com", region)
}

// RegisterIdcClient registers a new OIDC client for device flow authentication
func (a *App) RegisterIdcClient(req *IdcRegisterRequest) (*IdcRegisterResponse, error) {
	if req == nil {
		req = &IdcRegisterRequest{}
	}

	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = "us-east-1"
	}

	oidcURL := getIdcOidcURL(region)
	registerURL := fmt.Sprintf("%s/client/register", oidcURL)

	// 构建注册请求
	payload := map[string]any{
		"clientName": "Amazon Q Developer for command line",
		"clientType": "public",
		"scopes": []string{
			"codewhisperer:completions",
			"codewhisperer:analysis",
			"codewhisperer:conversations",
		},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal register payload: %w", err)
	}

	// 创建 HTTP 请求
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", registerURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create register request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// 发送请求
	client := executor.NewHTTPClientForcedProxyURL("", 15*time.Second)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to register OIDC client: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read register response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("register failed with status %d: %s", resp.StatusCode, string(body))
	}

	// 解析响应
	var result IdcRegisterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse register response: %w", err)
	}

	return &result, nil
}

// StartDeviceAuthorization starts the device authorization flow
func (a *App) StartDeviceAuthorization(req *IdcDeviceAuthRequest) (*IdcDeviceAuthResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}

	clientId := strings.TrimSpace(req.ClientId)
	clientSecret := strings.TrimSpace(req.ClientSecret)
	if clientId == "" || clientSecret == "" {
		return nil, fmt.Errorf("clientId and clientSecret are required")
	}

	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = "us-east-1"
	}

	oidcURL := getIdcOidcURL(region)
	deviceAuthURL := fmt.Sprintf("%s/device_authorization", oidcURL)

	// 使用自定义 Start URL，为空则使用默认 Builder ID URL
	startURL := strings.TrimSpace(req.StartUrl)
	if startURL == "" {
		startURL = "https://view.awsapps.com/start"
	}

	// 构建设备授权请求
	payload := map[string]string{
		"clientId":     clientId,
		"clientSecret": clientSecret,
		"startUrl":     startURL,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal device auth payload: %w", err)
	}

	// 创建 HTTP 请求
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", deviceAuthURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create device auth request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// 发送请求
	client := executor.NewHTTPClientForcedProxyURL("", 15*time.Second)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to start device authorization: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read device auth response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device authorization failed with status %d: %s", resp.StatusCode, string(body))
	}

	// 解析响应
	var result IdcDeviceAuthResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse device auth response: %w", err)
	}

	return &result, nil
}

// PollIdcToken polls for the access token using device code
func (a *App) PollIdcToken(req *IdcPollTokenRequest) (*IdcPollTokenResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}

	clientId := strings.TrimSpace(req.ClientId)
	clientSecret := strings.TrimSpace(req.ClientSecret)
	deviceCode := strings.TrimSpace(req.DeviceCode)
	if clientId == "" || clientSecret == "" || deviceCode == "" {
		return nil, fmt.Errorf("clientId, clientSecret and deviceCode are required")
	}

	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = "us-east-1"
	}

	oidcURL := getIdcOidcURL(region)
	tokenURL := fmt.Sprintf("%s/token", oidcURL)

	// 构建 token 请求
	payload := map[string]string{
		"clientId":     clientId,
		"clientSecret": clientSecret,
		"deviceCode":   deviceCode,
		"grantType":    "urn:ietf:params:oauth:grant-type:device_code",
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal token poll payload: %w", err)
	}

	// 创建 HTTP 请求
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", tokenURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create token poll request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// 发送请求
	client := executor.NewHTTPClientForcedProxyURL("", 15*time.Second)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to poll token: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token poll response: %w", err)
	}

	// 解析响应（即使是错误响应也要解析）
	var result IdcPollTokenResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse token poll response: %w", err)
	}

	// 对于 400 错误，如果是可预期的业务错误（authorization_pending/slow_down/expired_token），返回结果而不是错误
	// 这样前端可以根据 error 字段做相应处理
	if resp.StatusCode == http.StatusBadRequest {
		if result.Error == "authorization_pending" || result.Error == "slow_down" || result.Error == "expired_token" {
			return &result, nil
		}
	}

	// 对于其他错误状态码，返回错误
	if resp.StatusCode != http.StatusOK {
		if result.Error != "" {
			return &result, fmt.Errorf("token poll failed: %s", result.Error)
		}
		return nil, fmt.Errorf("token poll failed with status %d: %s", resp.StatusCode, string(body))
	}

	return &result, nil
}
