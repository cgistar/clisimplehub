package shared

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// GetDefaultKiroMultiConfigPath 返回多账号配置文件的默认路径
func GetDefaultKiroMultiConfigPath() string {
	return "kiro.json"
}

// LoadKiroMultiConfig 从 JSON 文件加载多账号配置
func LoadKiroMultiConfig(path string) (*KiroMultiConfig, error) {
	expandedPath := ExpandTilde(path)

	data, err := os.ReadFile(expandedPath)
	if err != nil {
		return nil, err
	}

	var config KiroMultiConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	// 规范化账号中的时间字段
	for i := range config.Accounts {
		normalizeAccountTimes(&config.Accounts[i])
	}

	return &config, nil
}

// SaveKiroMultiConfig 保存多账号配置到 JSON 文件
func SaveKiroMultiConfig(path string, config *KiroMultiConfig) error {
	expandedPath := ExpandTilde(path)
	if strings.TrimSpace(expandedPath) == "" {
		return errors.New("empty config path")
	}
	if config == nil {
		return errors.New("nil config")
	}

	// 更新所有账号的 UpdatedAt
	now := time.Now()
	for i := range config.Accounts {
		config.Accounts[i].UpdatedAt = now
	}

	// 确保目录存在
	dir := filepath.Dir(expandedPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	// 序列化
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	// 原子写入
	tmp, err := os.CreateTemp(dir, filepath.Base(expandedPath)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		_ = tmp.Close()
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, expandedPath); err != nil {
		return err
	}
	renamed = true

	if runtime.GOOS != "windows" {
		if err := os.Chmod(expandedPath, 0600); err != nil {
			return err
		}
	}
	return nil
}

// MigrateFromSingleAccount 从单账号配置迁移到多账号配置
// oldPath: kiro-auth-token.json 路径
// newPath: kiro.json 路径
// computeMachineId: 计算 machineId 的函数
// 返回: 是否执行了迁移
func MigrateFromSingleAccount(oldPath, newPath string, computeMachineId func(string) string) (bool, error) {
	expandedOldPath := ExpandTilde(oldPath)
	expandedNewPath := ExpandTilde(newPath)

	// 检查新配置是否已存在（只要文件可读可解析，就视为已进入多账号模式）
	// 注意：即使 accounts 为空，也不应从单账号配置自动迁移，否则会导致“删除最后一个账号后又被迁回”的问题。
	if _, err := LoadKiroMultiConfig(expandedNewPath); err == nil {
		return false, nil
	}

	// 检查旧配置是否存在
	oldCreds, err := LoadKiroCredentials(expandedOldPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // 旧配置不存在，无需迁移
		}
		return false, err
	}

	// 旧配置存在但没有 refreshToken，无需迁移
	if strings.TrimSpace(oldCreds.RefreshToken) == "" {
		return false, nil
	}

	// 创建新的多账号配置
	now := time.Now()

	account := KiroAccount{
		RefreshToken: oldCreds.RefreshToken,
		AccessToken:  oldCreds.AccessToken,
		ProfileArn:   oldCreds.ProfileArn,
		ExpiresAt:    oldCreds.ExpiresAt,
		Region:       oldCreds.Region,
		AuthMethod:   oldCreds.AuthMethod,
		Provider:     oldCreds.Provider,
		ClientId:     oldCreds.ClientId,
		ClientSecret: oldCreds.ClientSecret,
		Status:       KiroStatusUnknown,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// 计算 machineId
	if strings.TrimSpace(oldCreds.MachineID) != "" {
		account.MachineId = strings.TrimSpace(oldCreds.MachineID)
	} else if computeMachineId != nil {
		account.MachineId = computeMachineId(oldCreds.RefreshToken)
	}

	// 设置默认 region
	if account.Region == "" {
		account.Region = "us-east-1"
	}

	newConfig := &KiroMultiConfig{
		ActiveRefreshToken: oldCreds.RefreshToken,
		Accounts:           []KiroAccount{account},
	}

	// 保存新配置
	if err := SaveKiroMultiConfig(expandedNewPath, newConfig); err != nil {
		return false, err
	}

	return true, nil
}

// normalizeAccountTimes 规范化账号中的时间字段
func normalizeAccountTimes(account *KiroAccount) {
	if account.CreatedAt.IsZero() {
		account.CreatedAt = time.Now()
	}
	if account.UpdatedAt.IsZero() {
		account.UpdatedAt = account.CreatedAt
	}
}

// CreateAccountFromCredentials 从凭证创建新账号
func CreateAccountFromCredentials(creds *KiroCredentials, computeMachineId func(string) string) *KiroAccount {
	now := time.Now()
	account := &KiroAccount{
		Status:    KiroStatusUnknown,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if creds != nil {
		account.FromCredentials(creds)
		if strings.TrimSpace(creds.MachineID) != "" {
			account.MachineId = strings.TrimSpace(creds.MachineID)
		} else if computeMachineId != nil && creds.RefreshToken != "" {
			account.MachineId = computeMachineId(creds.RefreshToken)
		}
	}
	if account.Region == "" {
		account.Region = "us-east-1"
	}
	return account
}
