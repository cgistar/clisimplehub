package entclawruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type MCPStore struct {
	root string
}

func NewMCPStore(dataDir string) MCPStore {
	return MCPStore{
		root: filepath.Join(dataDir, "entclaw", "mcp"),
	}
}

func (s MCPStore) Write(ctx context.Context, name string, config json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	path, err := s.configPath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return fmt.Errorf("create mcp root: %w", err)
	}

	body := config
	if len(body) == 0 {
		body = json.RawMessage(`{}`)
	}
	if !json.Valid(body) {
		return fmt.Errorf("invalid mcp config json")
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("write mcp file: %w", err)
	}
	return nil
}

func (s MCPStore) Read(ctx context.Context, name string) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	path, err := s.configPath(name)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), body...), nil
}

func (s MCPStore) configPath(name string) (string, error) {
	configName := strings.TrimSpace(name)
	if configName == "" {
		return "", fmt.Errorf("mcp name is required")
	}
	if filepath.Base(configName) != configName {
		return "", fmt.Errorf("invalid mcp name %q", name)
	}
	return filepath.Join(s.root, configName+".json"), nil
}
