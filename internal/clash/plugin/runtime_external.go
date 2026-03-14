package clashplugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const externalRuntimePIDFilename = "mihomo.pid"

type externalRuntimeInstance struct {
	cmd     *exec.Cmd
	pid     int
	pidFile string
	managed bool
	once    sync.Once
}

func (e *externalRuntimeInstance) Close() error {
	if e == nil || !e.managed {
		return nil
	}

	var closeErr error
	e.once.Do(func() {
		pid := e.pid
		if pid <= 0 && e.cmd != nil && e.cmd.Process != nil {
			pid = e.cmd.Process.Pid
		}
		if pid <= 0 {
			_ = removeManagedRuntimePIDFile(e.pidFile)
			return
		}

		if err := terminateProcess(pid); err != nil && !errors.Is(err, os.ErrProcessDone) {
			closeErr = err
		}

		if e.cmd != nil {
			if waitErr := e.cmd.Wait(); waitErr != nil {
				var exitErr *exec.ExitError
				if !errors.As(waitErr, &exitErr) && closeErr == nil {
					closeErr = waitErr
				}
			}
		}

		_ = removeManagedRuntimePIDFile(e.pidFile)
	})
	return closeErr
}

func startExternalRuntimeInstance(binaryPath, runtimePath, dataDir string) (io.Closer, error) {
	homeDir := filepath.Join(dataDir, "mihomo-home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return nil, fmt.Errorf("create mihomo home: %w", err)
	}

	controllerCfg, err := resolveRuntimeControllerConfigFromFile(runtimePath)
	if err != nil {
		return nil, err
	}

	pidFile := filepath.Join(dataDir, externalRuntimePIDFilename)
	if existing, err := reuseOrCleanupExistingExternalRuntime(controllerCfg, pidFile); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	cmd := exec.Command(binaryPath, "-d", homeDir, "-f", runtimePath)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start external clash: %w", err)
	}

	instance := &externalRuntimeInstance{
		cmd:     cmd,
		pid:     cmd.Process.Pid,
		pidFile: pidFile,
		managed: true,
	}
	if err := writeManagedRuntimePIDFile(pidFile, instance.pid); err != nil {
		_ = instance.Close()
		return nil, err
	}

	return instance, nil
}

func resolveRuntimeControllerConfigFromFile(runtimePath string) (*runtimeControllerConfig, error) {
	data, err := os.ReadFile(runtimePath)
	if err != nil {
		return nil, fmt.Errorf("read runtime config: %w", err)
	}

	var payload map[string]any
	if err := yaml.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse runtime config: %w", err)
	}

	rawController := strings.TrimSpace(fmt.Sprint(payload["external-controller"]))
	if rawController == "" || rawController == "<nil>" {
		return nil, fmt.Errorf("external-controller is empty")
	}

	baseURL, err := normalizeRuntimeControllerURL(rawController)
	if err != nil {
		return nil, err
	}

	secret := strings.TrimSpace(fmt.Sprint(payload["secret"]))
	if secret == "<nil>" {
		secret = ""
	}

	return &runtimeControllerConfig{
		baseURL: baseURL,
		secret:  secret,
	}, nil
}

func reuseOrCleanupExistingExternalRuntime(cfg *runtimeControllerConfig, pidFile string) (io.Closer, error) {
	pid, hasManagedPID := readManagedRuntimePIDFile(pidFile)
	if hasManagedPID && pid > 0 && !processExists(pid) {
		_ = removeManagedRuntimePIDFile(pidFile)
		hasManagedPID = false
		pid = 0
	}

	if runtimeControllerReachable(cfg) {
		if hasManagedPID && pid > 0 {
			log.Printf("[clash] reusing managed external runtime pid=%d", pid)
			return &externalRuntimeInstance{
				pid:     pid,
				pidFile: pidFile,
				managed: true,
			}, nil
		}

		log.Printf("[clash] external controller already running, skipping new process start")
		return &externalRuntimeInstance{
			pidFile: pidFile,
			managed: false,
		}, nil
	}

	if hasManagedPID && pid > 0 {
		if err := terminateProcess(pid); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return nil, fmt.Errorf("stop stale managed external clash pid=%d: %w", pid, err)
		}
		_ = removeManagedRuntimePIDFile(pidFile)
	}

	return nil, nil
}

func runtimeControllerReachable(cfg *runtimeControllerConfig) bool {
	if cfg == nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), runtimeControllerRequestTimeout)
	defer cancel()

	_, err := doRuntimeControllerRequest(ctx, cfg, "GET", "/version", nil, nil, "")
	return err == nil
}

func readManagedRuntimePIDFile(path string) (int, bool) {
	if strings.TrimSpace(path) == "" {
		return 0, false
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func writeManagedRuntimePIDFile(path string, pid int) error {
	if strings.TrimSpace(path) == "" || pid <= 0 {
		return fmt.Errorf("invalid managed runtime pid file")
	}

	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		return fmt.Errorf("write managed runtime pid file: %w", err)
	}
	return nil
}

func removeManagedRuntimePIDFile(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
