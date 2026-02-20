package shared

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func LoadCodexMultiConfig(path string) (*CodexMultiConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("empty config path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config CodexMultiConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	for i := range config.Accounts {
		normalizeAccountTimes(&config.Accounts[i])
		config.Accounts[i].AccountID = strings.TrimSpace(config.Accounts[i].AccountID)
		config.Accounts[i].RefreshToken = strings.TrimSpace(config.Accounts[i].RefreshToken)
	}

	// Normalize active account fields
	config.ActiveAccountID = strings.TrimSpace(config.ActiveAccountID)
	config.ActiveRefreshToken = strings.TrimSpace(config.ActiveRefreshToken)

	return &config, nil
}

func SaveCodexMultiConfig(path string, config *CodexMultiConfig) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("empty config path")
	}
	if config == nil {
		return errors.New("nil config")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
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
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	renamed = true

	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0600); err != nil {
			return fmt.Errorf("chmod failed: %w", err)
		}
	}
	return nil
}

func normalizeAccountTimes(account *CodexAccount) {
	if account.CreatedAt.IsZero() {
		account.CreatedAt = time.Now()
	}
	if account.UpdatedAt.IsZero() {
		account.UpdatedAt = account.CreatedAt
	}
}
