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

func LoadXaiMultiConfig(path string) (*XaiMultiConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("empty config path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config XaiMultiConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	config.ActiveAccountID = strings.TrimSpace(config.ActiveAccountID)
	for i := range config.Accounts {
		NormalizeAccount(&config.Accounts[i])
	}
	if strings.TrimSpace(config.Config.BaseURL) == "" {
		config.Config.BaseURL = DefaultXaiConfig().BaseURL
	}
	return &config, nil
}

func SaveXaiMultiConfig(path string, config *XaiMultiConfig) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("empty config path")
	}
	if config == nil {
		return errors.New("nil config")
	}

	now := time.Now()
	for i := range config.Accounts {
		NormalizeAccount(&config.Accounts[i])
		config.Accounts[i].UpdatedAt = now
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

func EnsureXaiMultiConfig(path string) (*XaiMultiConfig, error) {
	cfg, err := LoadXaiMultiConfig(path)
	if err == nil {
		return cfg, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	cfg = &XaiMultiConfig{
		RotationMode: RotationFixed,
		Config:       DefaultXaiConfig(),
		Accounts:     []XaiAccount{},
	}
	if err := SaveXaiMultiConfig(path, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
