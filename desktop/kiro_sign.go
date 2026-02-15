package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// StartKiroSignCallbackServer 启动 127.0.0.1:3128 临时 HTTP 回调服务器
func (a *App) StartKiroSignCallbackServer() error {
	a.kiroSignMu.Lock()
	defer a.kiroSignMu.Unlock()

	if a.kiroSignServer != nil {
		return fmt.Errorf("callback server already running")
	}

	// 预检端口占用
	ln, err := net.Listen("tcp", "127.0.0.1:3128")
	if err != nil {
		return fmt.Errorf("port 3128 is already in use: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/callback", a.handleKiroSignOAuthCallback)
	mux.HandleFunc("/signin/callback", a.handleKiroSignIdcCallback)

	srv := &http.Server{Handler: mux}
	a.kiroSignServer = srv

	// 5 分钟超时自动关闭
	a.kiroSignTimeout = time.AfterFunc(5*time.Minute, func() {
		runtime.EventsEmit(a.ctx, "kiro-sign-timeout", nil)
		a.StopKiroSignCallbackServer()
	})

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Printf("[kiro-sign] server error: %v\n", err)
		}
	}()

	return nil
}

// StopKiroSignCallbackServer 关闭回调服务器
func (a *App) StopKiroSignCallbackServer() {
	a.kiroSignMu.Lock()
	defer a.kiroSignMu.Unlock()

	if a.kiroSignTimeout != nil {
		a.kiroSignTimeout.Stop()
		a.kiroSignTimeout = nil
	}

	if a.kiroSignServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.kiroSignServer.Shutdown(ctx)
		a.kiroSignServer = nil
	}
}

// handleKiroSignOAuthCallback 处理 Social 登录回调（Google/GitHub）
func (a *App) handleKiroSignOAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	loginOption := r.URL.Query().Get("login_option")

	http.Redirect(w, r,
		"https://app.kiro.dev/signin?auth_status=success&redirect_from=KiroIDE",
		http.StatusFound)

	runtime.EventsEmit(a.ctx, "kiro-sign-callback", map[string]string{
		"type":        "social",
		"code":        code,
		"state":       state,
		"loginOption": loginOption,
	})

	go func() {
		time.Sleep(200 * time.Millisecond)
		a.StopKiroSignCallbackServer()
	}()
}

// handleKiroSignIdcCallback 处理 IDC 登录回调（BuilderID/IAM Identity Center）
func (a *App) handleKiroSignIdcCallback(w http.ResponseWriter, r *http.Request) {
	loginOption := r.URL.Query().Get("login_option")
	issuerUrl := r.URL.Query().Get("issuer_url")
	idcRegion := r.URL.Query().Get("idc_region")
	state := r.URL.Query().Get("state")

	http.Redirect(w, r,
		"https://app.kiro.dev/signin?auth_status=success&redirect_from=KiroIDE",
		http.StatusFound)

	runtime.EventsEmit(a.ctx, "kiro-sign-callback", map[string]string{
		"type":        "idc",
		"loginOption": loginOption,
		"issuerUrl":   issuerUrl,
		"idcRegion":   idcRegion,
		"state":       state,
	})

	go func() {
		time.Sleep(200 * time.Millisecond)
		a.StopKiroSignCallbackServer()
	}()
}

// StartKiroSignIdcCallbackServer 启动随机端口 HTTP 回调服务器（用于 IDC Auth Code Flow）
func (a *App) StartKiroSignIdcCallbackServer() (int, error) {
	a.kiroSignIdcMu.Lock()
	defer a.kiroSignIdcMu.Unlock()

	if a.kiroSignIdcServer != nil {
		return 0, fmt.Errorf("IDC callback server already running")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to listen on random port: %w", err)
	}

	port := ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/callback", a.handleKiroSignIdcAuthCodeCallback)

	srv := &http.Server{Handler: mux}
	a.kiroSignIdcServer = srv

	a.kiroSignIdcTimeout = time.AfterFunc(5*time.Minute, func() {
		runtime.EventsEmit(a.ctx, "kiro-sign-idc-timeout", nil)
		a.StopKiroSignIdcCallbackServer()
	})

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Printf("[kiro-sign-idc] server error: %v\n", err)
		}
	}()

	return port, nil
}

// StopKiroSignIdcCallbackServer 关闭 IDC 回调服务器
func (a *App) StopKiroSignIdcCallbackServer() {
	a.kiroSignIdcMu.Lock()
	defer a.kiroSignIdcMu.Unlock()

	if a.kiroSignIdcTimeout != nil {
		a.kiroSignIdcTimeout.Stop()
		a.kiroSignIdcTimeout = nil
	}

	if a.kiroSignIdcServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.kiroSignIdcServer.Shutdown(ctx)
		a.kiroSignIdcServer = nil
	}
}

// handleKiroSignIdcAuthCodeCallback 处理 IDC Authorization Code 回调
func (a *App) handleKiroSignIdcAuthCodeCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	http.Redirect(w, r,
		"https://app.kiro.dev/signin?auth_status=success&redirect_from=KiroIDE",
		http.StatusFound)

	runtime.EventsEmit(a.ctx, "kiro-sign-idc-callback", map[string]string{
		"code":  code,
		"state": state,
	})

	go func() {
		time.Sleep(200 * time.Millisecond)
		a.StopKiroSignIdcCallbackServer()
	}()
}

// =============================================================================
// Kiro IDC Device Flow Authentication
// =============================================================================

type IdcRegisterRequest struct {
	Region string `json:"region,omitempty"`
}

type IdcRegisterResponse struct {
	ClientId     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type IdcDeviceAuthRequest struct {
	ClientId     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	Region       string `json:"region,omitempty"`
	StartUrl     string `json:"startUrl,omitempty"`
}

type IdcDeviceAuthResponse struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationUri         string `json:"verificationUri"`
	VerificationUriComplete string `json:"verificationUriComplete"`
	ExpiresIn               int    `json:"expiresIn"`
	Interval                int    `json:"interval"`
}

type IdcPollTokenRequest struct {
	ClientId     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	DeviceCode   string `json:"deviceCode"`
	Region       string `json:"region,omitempty"`
}

type IdcPollTokenResponse struct {
	AccessToken  string `json:"accessToken,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresIn    int    `json:"expiresIn,omitempty"`
	TokenType    string `json:"tokenType,omitempty"`
	Error        string `json:"error,omitempty"`
}

func (a *App) getKiroProxyURL() string {
	kp := kiroProvider()
	if kp == nil {
		return ""
	}
	return kp.GetProxyURL(a.getKiroMultiConfigPath())
}

func (a *App) RegisterIdcClient(req *IdcRegisterRequest) (*IdcRegisterResponse, error) {
	if req == nil {
		req = &IdcRegisterRequest{}
	}
	kp := kiroProvider()
	if kp == nil {
		return nil, fmt.Errorf("kiro plugin not available")
	}
	proxyURL := a.getKiroProxyURL()
	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = "us-east-1"
	}
	clientId, clientSecret, err := kp.IdcRegisterClient(proxyURL, region)
	if err != nil {
		return nil, err
	}
	return &IdcRegisterResponse{
		ClientId:     clientId,
		ClientSecret: clientSecret,
	}, nil
}

func (a *App) StartDeviceAuthorization(req *IdcDeviceAuthRequest) (*IdcDeviceAuthResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	kp := kiroProvider()
	if kp == nil {
		return nil, fmt.Errorf("kiro plugin not available")
	}
	proxyURL := a.getKiroProxyURL()
	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = "us-east-1"
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	respJSON, err := kp.IdcStartDeviceAuth(proxyURL, region, reqJSON)
	if err != nil {
		return nil, err
	}
	var resp IdcDeviceAuthResponse
	if err := json.Unmarshal(respJSON, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (a *App) PollIdcToken(req *IdcPollTokenRequest) (*IdcPollTokenResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	kp := kiroProvider()
	if kp == nil {
		return nil, fmt.Errorf("kiro plugin not available")
	}
	proxyURL := a.getKiroProxyURL()
	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = "us-east-1"
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	respJSON, err := kp.IdcPollToken(proxyURL, region, reqJSON)
	if err != nil {
		return nil, err
	}
	var resp IdcPollTokenResponse
	if err := json.Unmarshal(respJSON, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// =============================================================================
// Kiro IDC Authorization Code Flow
// =============================================================================

type IdcAuthCodeRegisterRequest struct {
	Region    string `json:"region,omitempty"`
	IssuerUrl string `json:"issuerUrl"`
}

type IdcAuthCodeRegisterResponse struct {
	ClientId     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type IdcAuthCodeTokenRequest struct {
	Region       string `json:"region,omitempty"`
	ClientId     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	Code         string `json:"code"`
	RedirectUri  string `json:"redirectUri"`
	CodeVerifier string `json:"codeVerifier"`
}

type IdcAuthCodeTokenResponse struct {
	AccessToken  string `json:"accessToken,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresIn    int    `json:"expiresIn,omitempty"`
	TokenType    string `json:"tokenType,omitempty"`
}

func (a *App) RegisterIdcAuthCodeClient(req *IdcAuthCodeRegisterRequest) (*IdcAuthCodeRegisterResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	kp := kiroProvider()
	if kp == nil {
		return nil, fmt.Errorf("kiro plugin not available")
	}
	proxyURL := a.getKiroProxyURL()
	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = "us-east-1"
	}
	clientId, clientSecret, err := kp.IdcRegisterAuthCodeClient(proxyURL, region, req.IssuerUrl)
	if err != nil {
		return nil, err
	}
	return &IdcAuthCodeRegisterResponse{
		ClientId:     clientId,
		ClientSecret: clientSecret,
	}, nil
}

func (a *App) ExchangeIdcAuthCode(req *IdcAuthCodeTokenRequest) (*IdcAuthCodeTokenResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	kp := kiroProvider()
	if kp == nil {
		return nil, fmt.Errorf("kiro plugin not available")
	}
	proxyURL := a.getKiroProxyURL()
	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = "us-east-1"
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	respJSON, err := kp.IdcExchangeAuthCode(proxyURL, region, reqJSON)
	if err != nil {
		return nil, err
	}
	var resp IdcAuthCodeTokenResponse
	if err := json.Unmarshal(respJSON, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
