package main

import (
	"fmt"
	"strings"

	kiroIdc "clisimplehub/internal/kiro/idc"
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

// getKiroProxyURL 从 kiro.json 读取 Kiro 代理配置
func (a *App) getKiroProxyURL() string {
	if mc, err := a.loadOrCreateMultiConfig(); err == nil && mc != nil {
		return strings.TrimSpace(mc.ProxyURL)
	}
	return ""
}

// RegisterIdcClient registers a new OIDC client for device flow authentication
func (a *App) RegisterIdcClient(req *IdcRegisterRequest) (*IdcRegisterResponse, error) {
	if req == nil {
		req = &IdcRegisterRequest{}
	}

	// 从 storage 读取代理配置
	proxyURL := a.getKiroProxyURL()

	// 创建 IDC 客户端
	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = "us-east-1"
	}
	client := kiroIdc.NewClient(proxyURL, region)

	// 调用核心层的 RegisterClient 方法
	coreReq := &kiroIdc.RegisterRequest{
		Region: req.Region,
	}
	coreResp, err := client.RegisterClient(coreReq)
	if err != nil {
		return nil, err
	}

	// 转换响应类型
	return &IdcRegisterResponse{
		ClientId:     coreResp.ClientId,
		ClientSecret: coreResp.ClientSecret,
	}, nil
}

// StartDeviceAuthorization starts the device authorization flow
func (a *App) StartDeviceAuthorization(req *IdcDeviceAuthRequest) (*IdcDeviceAuthResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}

	// 从 storage 读取代理配置
	proxyURL := a.getKiroProxyURL()

	// 创建 IDC 客户端
	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = "us-east-1"
	}
	client := kiroIdc.NewClient(proxyURL, region)

	// 调用核心层的 StartDeviceAuthorization 方法
	coreReq := &kiroIdc.DeviceAuthRequest{
		ClientId:     req.ClientId,
		ClientSecret: req.ClientSecret,
		Region:       req.Region,
		StartUrl:     req.StartUrl,
	}
	coreResp, err := client.StartDeviceAuthorization(coreReq)
	if err != nil {
		return nil, err
	}

	// 转换响应类型
	return &IdcDeviceAuthResponse{
		DeviceCode:              coreResp.DeviceCode,
		UserCode:                coreResp.UserCode,
		VerificationUri:         coreResp.VerificationUri,
		VerificationUriComplete: coreResp.VerificationUriComplete,
		ExpiresIn:               coreResp.ExpiresIn,
		Interval:                coreResp.Interval,
	}, nil
}

// PollIdcToken polls for the access token using device code
func (a *App) PollIdcToken(req *IdcPollTokenRequest) (*IdcPollTokenResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}

	// 从 storage 读取代理配置
	proxyURL := a.getKiroProxyURL()

	// 创建 IDC 客户端
	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = "us-east-1"
	}
	client := kiroIdc.NewClient(proxyURL, region)

	// 调用核心层的 PollToken 方法
	coreReq := &kiroIdc.PollTokenRequest{
		ClientId:     req.ClientId,
		ClientSecret: req.ClientSecret,
		DeviceCode:   req.DeviceCode,
		Region:       req.Region,
	}
	coreResp, err := client.PollToken(coreReq)
	if err != nil {
		return nil, err
	}

	// 转换响应类型
	return &IdcPollTokenResponse{
		AccessToken:  coreResp.AccessToken,
		RefreshToken: coreResp.RefreshToken,
		ExpiresIn:    coreResp.ExpiresIn,
		TokenType:    coreResp.TokenType,
		Error:        coreResp.Error,
	}, nil
}

// IdcAuthCodeRegisterRequest Authorization Code Flow 客户端注册请求
type IdcAuthCodeRegisterRequest struct {
	Region    string `json:"region,omitempty"`
	IssuerUrl string `json:"issuerUrl"`
}

// IdcAuthCodeRegisterResponse Authorization Code Flow 客户端注册响应
type IdcAuthCodeRegisterResponse struct {
	ClientId     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// IdcAuthCodeTokenRequest Authorization Code Flow token 交换请求
type IdcAuthCodeTokenRequest struct {
	Region       string `json:"region,omitempty"`
	ClientId     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	Code         string `json:"code"`
	RedirectUri  string `json:"redirectUri"`
	CodeVerifier string `json:"codeVerifier"`
}

// IdcAuthCodeTokenResponse Authorization Code Flow token 交换响应
type IdcAuthCodeTokenResponse struct {
	AccessToken  string `json:"accessToken,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresIn    int    `json:"expiresIn,omitempty"`
	TokenType    string `json:"tokenType,omitempty"`
}

// RegisterIdcAuthCodeClient 注册 Authorization Code Flow OIDC 客户端
func (a *App) RegisterIdcAuthCodeClient(req *IdcAuthCodeRegisterRequest) (*IdcAuthCodeRegisterResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}

	proxyURL := a.getKiroProxyURL()
	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = "us-east-1"
	}
	client := kiroIdc.NewClient(proxyURL, region)

	coreResp, err := client.RegisterAuthCodeClient(&kiroIdc.AuthCodeRegisterRequest{
		Region:    region,
		IssuerUrl: req.IssuerUrl,
	})
	if err != nil {
		return nil, err
	}

	return &IdcAuthCodeRegisterResponse{
		ClientId:     coreResp.ClientId,
		ClientSecret: coreResp.ClientSecret,
	}, nil
}

// ExchangeIdcAuthCode 用 authorization code 交换 token
func (a *App) ExchangeIdcAuthCode(req *IdcAuthCodeTokenRequest) (*IdcAuthCodeTokenResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}

	proxyURL := a.getKiroProxyURL()
	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = "us-east-1"
	}
	client := kiroIdc.NewClient(proxyURL, region)

	coreResp, err := client.ExchangeAuthCode(&kiroIdc.AuthCodeTokenRequest{
		Region:       region,
		ClientId:     req.ClientId,
		ClientSecret: req.ClientSecret,
		Code:         req.Code,
		RedirectUri:  req.RedirectUri,
		CodeVerifier: req.CodeVerifier,
	})
	if err != nil {
		return nil, err
	}

	return &IdcAuthCodeTokenResponse{
		AccessToken:  coreResp.AccessToken,
		RefreshToken: coreResp.RefreshToken,
		ExpiresIn:    coreResp.ExpiresIn,
		TokenType:    coreResp.TokenType,
	}, nil
}
