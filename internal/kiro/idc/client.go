package idc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	kiroapi "clisimplehub/internal/kiro"
	kiroClient "clisimplehub/internal/kiro/client"
)

// Client IDC 认证客户端
type Client struct {
	httpClient *http.Client
	region     string
}

// NewClient 创建 IDC 认证客户端
func NewClient(proxyURL string, region string) *Client {
	return &Client{
		httpClient: kiroClient.NewHTTPClientWithProxy(proxyURL, 15*time.Second),
		region:     kiroapi.ResolveRegion(region),
	}
}

// resolveRegion 解析 region，优先级：请求参数 > 客户端配置 > 默认值
func (c *Client) resolveRegion(requestRegion string) string {
	region := strings.TrimSpace(requestRegion)
	if region == "" {
		region = c.region
	}
	return kiroapi.ResolveRegion(region)
}

// RegisterClient 注册 OIDC 客户端
func (c *Client) RegisterClient(req *RegisterRequest) (*RegisterResponse, error) {
	if req == nil {
		req = &RegisterRequest{}
	}

	region := c.resolveRegion(req.Region)
	registerURL := kiroapi.KiroOidcBaseURL(region) + "/client/register"

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
	resp, err := c.httpClient.Do(httpReq)
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
	var result RegisterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse register response: %w", err)
	}

	return &result, nil
}

// StartDeviceAuthorization 启动设备授权流程
func (c *Client) StartDeviceAuthorization(req *DeviceAuthRequest) (*DeviceAuthResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}

	clientId := strings.TrimSpace(req.ClientId)
	clientSecret := strings.TrimSpace(req.ClientSecret)
	if clientId == "" || clientSecret == "" {
		return nil, fmt.Errorf("clientId and clientSecret are required")
	}

	region := c.resolveRegion(req.Region)
	deviceAuthURL := kiroapi.KiroOidcBaseURL(region) + "/device_authorization"

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
	resp, err := c.httpClient.Do(httpReq)
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
	var result DeviceAuthResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse device auth response: %w", err)
	}

	return &result, nil
}

// PollToken 轮询获取 token
func (c *Client) PollToken(req *PollTokenRequest) (*PollTokenResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}

	clientId := strings.TrimSpace(req.ClientId)
	clientSecret := strings.TrimSpace(req.ClientSecret)
	deviceCode := strings.TrimSpace(req.DeviceCode)
	if clientId == "" || clientSecret == "" || deviceCode == "" {
		return nil, fmt.Errorf("clientId, clientSecret and deviceCode are required")
	}

	region := c.resolveRegion(req.Region)
	tokenURL := kiroapi.KiroOidcBaseURL(region) + "/token"

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
	resp, err := c.httpClient.Do(httpReq)
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
	var result PollTokenResponse
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

// RegisterAuthCodeClient 注册 Authorization Code Flow OIDC 客户端
func (c *Client) RegisterAuthCodeClient(req *AuthCodeRegisterRequest) (*AuthCodeRegisterResponse, error) {
	if req == nil {
		req = &AuthCodeRegisterRequest{}
	}

	region := c.resolveRegion(req.Region)
	registerURL := kiroapi.KiroOidcBaseURL(region) + "/client/register"

	payload := map[string]any{
		"clientName": "Kiro IDE",
		"clientType": "public",
		"scopes": []string{
			"codewhisperer:completions",
			"codewhisperer:analysis",
			"codewhisperer:conversations",
			"codewhisperer:transformations",
			"codewhisperer:taskassist",
		},
		"redirectUris": []string{"http://127.0.0.1/oauth/callback"},
		"grantTypes":   []string{"authorization_code", "refresh_token"},
		"issuerUrl":    strings.TrimSpace(req.IssuerUrl),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal register payload: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", registerURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create register request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	kiroapi.ApplyIdcOidc(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to register auth code client: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read register response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("register failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result AuthCodeRegisterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse register response: %w", err)
	}
	return &result, nil
}

// ExchangeAuthCode 用 authorization code 交换 token
func (c *Client) ExchangeAuthCode(req *AuthCodeTokenRequest) (*AuthCodeTokenResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}

	region := c.resolveRegion(req.Region)
	tokenURL := kiroapi.KiroOidcBaseURL(region) + "/token"

	payload := map[string]string{
		"clientId":     req.ClientId,
		"clientSecret": req.ClientSecret,
		"grantType":    "authorization_code",
		"code":         req.Code,
		"redirectUri":  req.RedirectUri,
		"codeVerifier": req.CodeVerifier,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal token request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", tokenURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	kiroapi.ApplyIdcOidc(httpReq)
	httpReq.Header.Set("Host", httpReq.URL.Host)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange auth code: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result AuthCodeTokenResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}
	return &result, nil
}
