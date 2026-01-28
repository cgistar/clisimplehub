package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"clisimplehub/internal/storage"
	"clisimplehub/internal/transformer/kiro"
	kiroClaude "clisimplehub/internal/transformer/kiro/claude"
	kiroShared "clisimplehub/internal/transformer/kiro/shared"
)

// KiroConfigRequest 请求结构
type KiroConfigRequest struct {
	RefreshToken   string `json:"refreshToken"`
	Region         string `json:"region"`
	AuthMethod     string `json:"authMethod"`
	ClientId       string `json:"clientId"`
	ClientSecret   string `json:"clientSecret"`
	ProxyURL       string `json:"proxyUrl"`
	BufferedStream bool   `json:"bufferedStream"`
}

// KiroConfigResponse 响应结构
type KiroConfigResponse struct {
	AccessToken string     `json:"accessToken"`
	ExpiresAt   string     `json:"expiresAt"`
	ProfileArn  string     `json:"profileArn"`
	Usage       *UsageInfo `json:"usage,omitempty"`
}

// UsageInfo 使用量信息
type UsageInfo struct {
	Used  int `json:"used"`
	Limit int `json:"limit"`
}

// handleKiroConfig 处理 Kiro 配置更新请求
func (p *ProxyServer) handleKiroConfig(w http.ResponseWriter, r *http.Request) {
	// 仅允许 POST 请求
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 解析请求
	var req KiroConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": fmt.Sprintf("Invalid request body: %v", err),
		})
		return
	}

	// 验证必填字段
	refreshToken := strings.TrimSpace(req.RefreshToken)
	if refreshToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "refreshToken is required",
		})
		return
	}

	// 设置默认值
	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = "us-east-1"
	}

	// 确定认证方式
	authMethod := strings.ToLower(strings.TrimSpace(req.AuthMethod))
	if authMethod == "" {
		// 如果未指定，根据是否有 clientId 和 clientSecret 自动判断
		if strings.TrimSpace(req.ClientId) != "" && strings.TrimSpace(req.ClientSecret) != "" {
			authMethod = "idc"
		} else {
			authMethod = "social"
		}
	}

	// 验证 IdC 认证方式的必填字段
	if authMethod == "idc" || authMethod == "builder-id" {
		if strings.TrimSpace(req.ClientId) == "" || strings.TrimSpace(req.ClientSecret) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"error": "clientId and clientSecret are required for IdC authentication",
			})
			return
		}
	}

	// 获取 kiro-auth-token.json 路径
	p.mu.RLock()
	configPath := p.configPath
	p.mu.RUnlock()

	if configPath == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "config path not set",
		})
		return
	}

	credsPath := filepath.Join(filepath.Dir(configPath), "kiro-auth-token.json")

	// 从 storage 读取 version, userAgent, machineId
	p.mu.RLock()
	store := p.store
	p.mu.RUnlock()

	version := ""
	userAgent := ""
	oldBufferedStream := false
	if store != nil {
		if v, err := store.GetConfig("kiro.version"); err == nil {
			version = strings.TrimSpace(v)
		}
		if ua, err := store.GetConfig("kiro.userAgent"); err == nil {
			userAgent = strings.TrimSpace(ua)
		}
		if bs, err := store.GetConfig("kiro.bufferedStream"); err == nil {
			oldBufferedStream = (bs == "true")
		}
	}

	// 应用默认值
	version = kiroShared.KiroVersionOrDefault(version)
	userAgent = kiroShared.KiroUserAgentBaseOrDefault(userAgent)

	// 加载现有凭证
	var existingCreds *kiroShared.KiroCredentials
	if creds, err := kiroShared.LoadKiroCredentials(credsPath); err == nil {
		existingCreds = creds
	}

	// 检查 refreshToken 是否变化
	oldRefreshToken := ""
	if existingCreds != nil {
		oldRefreshToken = strings.TrimSpace(existingCreds.RefreshToken)
	}
	refreshTokenChanged := oldRefreshToken != "" && refreshToken != "" && oldRefreshToken != refreshToken

	// 计算新的 machineId
	machineID := kiro.ComputeMachineID(refreshToken)

	// 检查 bufferedStream 是否变化
	bufferedStreamChanged := req.BufferedStream != oldBufferedStream

	// 如果 refreshToken 未变化且存在有效凭证，检查是否只需要更新 bufferedStream
	if !refreshTokenChanged && existingCreds != nil && existingCreds.AccessToken != "" {
		if !bufferedStreamChanged {
			// 配置未变化，直接返回现有信息
			response := KiroConfigResponse{
				AccessToken: existingCreds.AccessToken,
				ExpiresAt:   "",
				ProfileArn:  existingCreds.ProfileArn,
				Usage:       nil,
			}
			if !existingCreds.ExpiresAt.IsZero() {
				response.ExpiresAt = existingCreds.ExpiresAt.Format(time.RFC3339Nano)
			}
			writeJSON(w, http.StatusOK, response)
			return
		}

		// 如果只是 bufferedStream 变化，更新配置但不刷新 token
		if store != nil {
			if req.BufferedStream {
				_ = store.SetConfig("kiro.bufferedStream", "true")
			} else {
				_ = store.SetConfig("kiro.bufferedStream", "false")
			}
		}

		// 返回现有凭证信息
		response := KiroConfigResponse{
			AccessToken: existingCreds.AccessToken,
			ExpiresAt:   "",
			ProfileArn:  existingCreds.ProfileArn,
			Usage:       nil,
		}
		if !existingCreds.ExpiresAt.IsZero() {
			response.ExpiresAt = existingCreds.ExpiresAt.Format(time.RFC3339Nano)
		}
		writeJSON(w, http.StatusOK, response)
		return
	}

	// refreshToken 变化了，或者没有有效凭证，需要刷新 token

	// 创建临时凭证用于测试
	creds := &kiroShared.KiroCredentials{
		RefreshToken: refreshToken,
		Region:       region,
		AuthMethod:   authMethod,
		ClientId:     req.ClientId,
		ClientSecret: req.ClientSecret,
	}

	// 使用 KiroAuthManager 获取 accessToken
	mgr := kiroClaude.NewKiroAuthManager(creds, "", strings.TrimSpace(req.ProxyURL), version)
	accessToken, err := mgr.ForceRefresh()
	if err != nil {
		// 记录详细错误信息
		fmt.Fprintf(os.Stderr, "Kiro token refresh failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "Request details: authMethod=%s, region=%s, clientId=%s\n",
			authMethod, region, req.ClientId)

		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error": fmt.Sprintf("Failed to refresh token: %v", err),
		})
		return
	}

	// 检查 accessToken 是否为空
	if accessToken == "" {
		fmt.Fprintf(os.Stderr, "Warning: accessToken is empty after refresh\n")
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "Access token is empty after refresh",
		})
		return
	}

	// 获取 profileArn（从 manager 中获取）
	profileArn := mgr.GetProfileArn()

	// IdC tokens do not require (and typically do not return) a CodeWhisperer profileArn.
	// Keeping a stale profileArn (e.g. from a prior Social login) can cause confusing upstream auth errors.
	if authMethod == "idc" || authMethod == "builder-id" {
		profileArn = ""
	}

	// 获取使用量信息
	var usageInfo *UsageInfo
	if accessToken != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		usageQuery := kiro.UsageQuery{
			AccessToken:   accessToken,
			ProfileArn:    profileArn,
			Region:        region,
			MachineID:     machineID,
			UserAgentBase: userAgent,
			Version:       version,
		}

		if usageResult, err := kiro.FetchUsage(ctx, http.DefaultClient, usageQuery); err == nil && usageResult != nil {
			usageInfo = &UsageInfo{
				Used:  int(usageResult.CurrentUsage),
				Limit: int(usageResult.UsageLimit),
			}
		}
	}

	// 准备要保存的凭证
	saveCreds := &kiroShared.KiroCredentials{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ProfileArn:   profileArn,
		Region:       region,
		AuthMethod:   authMethod,
		ClientId:     req.ClientId,
		ClientSecret: req.ClientSecret,
	}

	// 从 creds 中获取 ExpiresAt（ForceRefresh 会更新它）
	if creds.ExpiresAt.IsZero() {
		saveCreds.ExpiresAt = time.Now().Add(1 * time.Hour)
	} else {
		saveCreds.ExpiresAt = creds.ExpiresAt
	}

	// 保存凭证到文件
	if err := kiroShared.SaveKiroCredentials(credsPath, saveCreds); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": fmt.Sprintf("Failed to save credentials: %v", err),
		})
		return
	}

	// 更新 config.json 中的 machineId 和 bufferedStream
	if store != nil {
		// 保存 machineId
		if err := store.SetConfig("kiro.machineId", machineID); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to save machine id: %v\n", err)
		}

		// 保存 bufferedStream
		if req.BufferedStream {
			if err := store.SetConfig("kiro.bufferedStream", "true"); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to save bufferedStream: %v\n", err)
			}
		} else {
			if err := store.SetConfig("kiro.bufferedStream", "false"); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to save bufferedStream: %v\n", err)
			}
		}
	}

	// 触发所有 Kiro Transformer 重新加载配置
	if err := kiroClaude.ReloadAllTransformers(); err != nil {
		// 记录错误但不影响响应，因为配置已经保存成功
		fmt.Fprintf(os.Stderr, "Warning: failed to reload transformers: %v\n", err)
	}

	// 检查并创建 kiro/claude 端点
	if err := p.ensureKiroEndpoint(); err != nil {
		// 记录错误但不影响响应
		fmt.Fprintf(os.Stderr, "Warning: failed to ensure kiro endpoint: %v\n", err)
	}

	// 返回成功响应
	expiresAt := ""
	if !saveCreds.ExpiresAt.IsZero() {
		expiresAt = saveCreds.ExpiresAt.Format(time.RFC3339Nano)
	}

	response := KiroConfigResponse{
		AccessToken: accessToken,
		ExpiresAt:   expiresAt,
		ProfileArn:  profileArn,
		Usage:       usageInfo,
	}

	writeJSON(w, http.StatusOK, response)
}

// ensureKiroEndpoint 确保存在 kiro/claude 端点，如果不存在则创建
func (p *ProxyServer) ensureKiroEndpoint() error {
	p.mu.RLock()
	store := p.store
	p.mu.RUnlock()

	if store == nil {
		return fmt.Errorf("storage not initialized")
	}

	// 获取所有端点
	endpoints, err := store.GetEndpoints()
	if err != nil {
		return fmt.Errorf("failed to get endpoints: %w", err)
	}

	// 检查是否已存在 kiro/claude 端点
	for _, ep := range endpoints {
		if ep.Transformer == "kiro/claude" && ep.InterfaceType == "claude" {
			// 已存在，无需创建
			return nil
		}
	}

	// 不存在，创建新端点
	newEndpoint := &storage.Endpoint{
		Name:          "kiro",
		APIURL:        "https://q.us-east-1.amazonaws.com",
		APIKey:        "-",
		Active:        false, // 默认不激活
		Enabled:       true,
		InterfaceType: "claude",
		Transformer:   "kiro/claude",
		Priority:      8,
	}

	// 检查是否是该 interfaceType 的唯一端点
	sameTypeCount := 0
	for _, ep := range endpoints {
		if ep.InterfaceType == "claude" {
			sameTypeCount++
		}
	}

	// 如果是唯一端点，自动设为 active
	if sameTypeCount == 0 {
		newEndpoint.Active = true
	}

	// 保存端点（ID 会由 storage 层自动生成）
	if err := store.SaveEndpoint(newEndpoint); err != nil {
		return fmt.Errorf("failed to save kiro endpoint: %w", err)
	}

	// 触发配置重载
	p.mu.RLock()
	reloadFunc := p.reloadFunc
	p.mu.RUnlock()

	if reloadFunc != nil {
		reloadFunc()
	}

	return nil
}

// handleKiroGetUsage 处理获取 Kiro 使用量请求
func (p *ProxyServer) handleKiroGetUsage(w http.ResponseWriter, r *http.Request) {
	// 仅允许 GET 请求
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 获取配置路径
	p.mu.RLock()
	configPath := p.configPath
	store := p.store
	p.mu.RUnlock()

	if configPath == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "config path not set",
		})
		return
	}

	// 加载凭证文件
	credsPath := filepath.Join(filepath.Dir(configPath), "kiro-auth-token.json")
	creds, err := kiroShared.LoadKiroCredentials(credsPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": fmt.Sprintf("Failed to load credentials: %v", err),
		})
		return
	}

	// 检查 accessToken 是否存在
	accessToken := strings.TrimSpace(creds.AccessToken)
	if accessToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "No access token available, please configure Kiro first",
		})
		return
	}

	// 获取配置信息
	region := strings.TrimSpace(creds.Region)
	if region == "" {
		region = "us-east-1"
	}

	profileArn := strings.TrimSpace(creds.ProfileArn)
	refreshToken := strings.TrimSpace(creds.RefreshToken)

	// 从 storage 读取 version, userAgent, machineId
	version := ""
	userAgent := ""
	machineID := ""
	if store != nil {
		if v, err := store.GetConfig("kiro.version"); err == nil {
			version = strings.TrimSpace(v)
		}
		if ua, err := store.GetConfig("kiro.userAgent"); err == nil {
			userAgent = strings.TrimSpace(ua)
		}
		if mid, err := store.GetConfig("kiro.machineId"); err == nil {
			machineID = strings.TrimSpace(mid)
		}
	}

	// 应用默认值
	version = kiroShared.KiroVersionOrDefault(version)
	userAgent = kiroShared.KiroUserAgentBaseOrDefault(userAgent)

	// 如果 machineId 为空，从 refreshToken 计算
	if machineID == "" && refreshToken != "" {
		machineID = kiro.ComputeMachineID(refreshToken)
	}

	// 获取使用量信息
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	usageQuery := kiro.UsageQuery{
		AccessToken:   accessToken,
		ProfileArn:    profileArn,
		Region:        region,
		MachineID:     machineID,
		UserAgentBase: userAgent,
		Version:       version,
	}

	usageResult, err := kiro.FetchUsage(ctx, http.DefaultClient, usageQuery)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": fmt.Sprintf("Failed to fetch usage: %v", err),
		})
		return
	}

	if usageResult == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "No usage data returned",
		})
		return
	}

	// 返回使用量信息
	response := map[string]interface{}{
		"subscriptionTitle": usageResult.SubscriptionTitle,
		"usageLimit":        usageResult.UsageLimit,
		"currentUsage":      usageResult.CurrentUsage,
		"balance":           usageResult.Balance,
		"usagePct":          usageResult.UsagePct,
		"isLowBalance":      usageResult.IsLowBalance,
	}

	writeJSON(w, http.StatusOK, response)
}
