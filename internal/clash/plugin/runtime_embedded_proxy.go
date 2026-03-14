//go:build proxy

package clashplugin

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	mihomoconfig "github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/hub"
	"github.com/metacubex/mihomo/hub/executor"
)

type mihomoInstance struct {
	once sync.Once
}

func (m *mihomoInstance) Close() error {
	m.once.Do(func() {
		executor.Shutdown()
	})
	return nil
}

func startEmbeddedRuntimeInstance(runtimeYAML []byte, dataDir string) (io.Closer, error) {
	homeDir := filepath.Join(dataDir, "mihomo-home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return nil, fmt.Errorf("create mihomo home: %w", err)
	}

	C.SetHomeDir(homeDir)
	C.SetConfig(filepath.Join(homeDir, "config.yaml"))
	if err := mihomoconfig.Init(C.Path.HomeDir()); err != nil {
		return nil, fmt.Errorf("init mihomo home: %w", err)
	}

	if err := hub.Parse(runtimeYAML); err != nil {
		return nil, fmt.Errorf("parse mihomo config: %w", err)
	}

	return &mihomoInstance{}, nil
}
