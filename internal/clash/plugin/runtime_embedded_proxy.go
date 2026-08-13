//go:build proxy

package clashplugin

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	mihomoconfig "github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/hub"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/listener"
	"github.com/metacubex/mihomo/tunnel"
)

type mihomoInstance struct {
	once sync.Once
}

func (m *mihomoInstance) Close() error {
	m.once.Do(func() {
		shutdownEmbeddedRuntime()
	})
	return nil
}

func shutdownEmbeddedRuntime() {
	// executor.Shutdown 不会关闭普通代理监听；显式撤销 mixed listener，
	// 保证服务状态变为停止后端口也已经同步释放。
	listener.ReCreateMixed(0, tunnel.Tunnel)
	executor.Shutdown()
}

func (m *mihomoInstance) Reload(runtimeYAML []byte) error {
	cfg, err := executor.ParseWithBytes(runtimeYAML)
	if err != nil {
		return fmt.Errorf("parse mihomo config: %w", err)
	}

	// mihomo 的 listener 重建逻辑会保留地址未变化的现有监听，并原位更新配置。
	// 这也避免了同一进程内的全局 runtime 在重启间隙争抢自己的端口。
	hub.ApplyConfig(cfg)
	if actualPort := listener.GetPorts().MixedPort; actualPort != cfg.General.MixedPort {
		return fmt.Errorf("reload mihomo mixed listener: expected port %d, got %d", cfg.General.MixedPort, actualPort)
	}
	if err := verifyEmbeddedMixedListener(cfg.General.MixedPort, cfg.General.BindAddress, cfg.General.AllowLan); err != nil {
		return err
	}
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

	cfg, err := executor.ParseWithBytes(runtimeYAML)
	if err != nil {
		return nil, fmt.Errorf("parse mihomo config: %w", err)
	}
	if err := validateEmbeddedMixedListener(cfg.General.MixedPort, cfg.General.BindAddress, cfg.General.AllowLan); err != nil {
		return nil, err
	}

	hub.ApplyConfig(cfg)
	if actualPort := listener.GetPorts().MixedPort; actualPort != cfg.General.MixedPort {
		shutdownEmbeddedRuntime()
		return nil, fmt.Errorf("start mihomo mixed listener: expected port %d, got %d", cfg.General.MixedPort, actualPort)
	}
	if err := verifyEmbeddedMixedListener(cfg.General.MixedPort, cfg.General.BindAddress, cfg.General.AllowLan); err != nil {
		shutdownEmbeddedRuntime()
		return nil, err
	}

	return &mihomoInstance{}, nil
}

func validateEmbeddedMixedListener(port int, bindAddress string, allowLAN bool) error {
	if port <= 0 {
		return fmt.Errorf("start mihomo mixed listener: invalid port %d", port)
	}

	host := "127.0.0.1"
	if allowLAN {
		host = bindAddress
		if host == "*" {
			host = ""
		}
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	tcpListener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("start mihomo mixed listener on %s: %w", addr, err)
	}
	if err := tcpListener.Close(); err != nil {
		return fmt.Errorf("release mihomo mixed TCP listener check on %s: %w", addr, err)
	}

	udpListener, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("start mihomo mixed UDP listener on %s: %w", addr, err)
	}
	if err := udpListener.Close(); err != nil {
		return fmt.Errorf("release mihomo mixed UDP listener check on %s: %w", addr, err)
	}
	return nil
}

func verifyEmbeddedMixedListener(port int, bindAddress string, allowLAN bool) error {
	host := "127.0.0.1"
	if allowLAN && bindAddress != "" && bindAddress != "*" {
		host = bindAddress
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return fmt.Errorf("verify mihomo mixed listener on %s: %w", addr, err)
	}
	if err := conn.Close(); err != nil {
		return fmt.Errorf("close mihomo mixed listener verification on %s: %w", addr, err)
	}
	return nil
}
