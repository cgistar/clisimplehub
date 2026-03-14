package clashplugin

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func startRuntimeInstance(runtimeYAML []byte, dataDir string) (io.Closer, error) {
	if len(runtimeYAML) == 0 {
		return nil, fmt.Errorf("empty runtime config")
	}

	runtimePath, err := writeRuntimeConfig(runtimeYAML, dataDir)
	if err != nil {
		return nil, err
	}

	if path, ok := usableExternalBinaryPath(); ok {
		return startExternalRuntimeInstance(path, runtimePath, dataDir)
	}

	if !hasEmbeddedRuntime() {
		return nil, fmt.Errorf("no usable clash runtime configured")
	}

	return startEmbeddedRuntimeInstance(runtimeYAML, dataDir)
}

func writeRuntimeConfig(runtimeYAML []byte, dataDir string) (string, error) {
	runtimePath := filepath.Join(dataDir, "clash-runtime.yaml")
	if err := os.WriteFile(runtimePath, runtimeYAML, 0o600); err != nil {
		return "", fmt.Errorf("write runtime config: %w", err)
	}
	return runtimePath, nil
}
