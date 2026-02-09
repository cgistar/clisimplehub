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

	// 首次加载时自动填充默认 ModelMapping（仅内存，不写盘避免竞争）
	if len(config.ModelMapping) == 0 {
		config.ModelMapping = DefaultKiroModelMapping()
	}
	if strings.TrimSpace(config.UserAgent) == "" {
		config.UserAgent = DefaultKiroUserAgentBase
	}
	if strings.TrimSpace(config.Version) == "" {
		config.Version = DefaultKiroVersion
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

// normalizeAccountTimes 规范化账号中的时间字段
func normalizeAccountTimes(account *KiroAccount) {
	if account.CreatedAt.IsZero() {
		account.CreatedAt = time.Now()
	}
	if account.UpdatedAt.IsZero() {
		account.UpdatedAt = account.CreatedAt
	}
}

// ExpandTilde expands ~ to user home directory
func ExpandTilde(path string) string {
	if strings.TrimSpace(path) == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
		return path
	}

	if strings.HasPrefix(path, "~"+string(filepath.Separator)) || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

