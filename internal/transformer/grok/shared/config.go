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

func GetDefaultGrokMultiConfigPath() string {
	return "grok.json"
}

func LoadGrokMultiConfig(path string) (*GrokMultiConfig, error) {
	expandedPath := ExpandTilde(path)
	data, err := os.ReadFile(expandedPath)
	if err != nil {
		return nil, err
	}
	var config GrokMultiConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	for i := range config.Accounts {
		normalizeAccountTimes(&config.Accounts[i])
	}
	return &config, nil
}

func SaveGrokMultiConfig(path string, config *GrokMultiConfig) error {
	expandedPath := ExpandTilde(path)
	if strings.TrimSpace(expandedPath) == "" {
		return errors.New("empty config path")
	}
	if config == nil {
		return errors.New("nil config")
	}
	now := time.Now()
	for i := range config.Accounts {
		config.Accounts[i].UpdatedAt = now
	}
	dir := filepath.Dir(expandedPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
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
		_ = os.Chmod(expandedPath, 0600)
	}
	return nil
}

func normalizeAccountTimes(account *GrokAccount) {
	if account.CreatedAt.IsZero() {
		account.CreatedAt = time.Now()
	}
	if account.UpdatedAt.IsZero() {
		account.UpdatedAt = account.CreatedAt
	}
}
