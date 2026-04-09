package entclawruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

const (
	defaultMCPProtocolVersion = "2025-03-26"
	defaultMCPStartupTimeout  = 10 * time.Second
	defaultMCPCallTimeout     = 30 * time.Second
)

type stdioMCPConfig struct {
	Command          string            `json:"command"`
	Args             []string          `json:"args"`
	Env              map[string]string `json:"env"`
	Cwd              string            `json:"cwd"`
	StartupTimeoutMS int               `json:"startup_timeout_ms"`
	CallTimeoutMS    int               `json:"call_timeout_ms"`
	Disabled         bool              `json:"disabled"`
	Description      string            `json:"description"`
}

type stdioMCPInvocation struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type stdioRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type stdioRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Result  json.RawMessage `json:"result"`
	Error   *stdioRPCError  `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
}

type stdioRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type stdioMCPClient struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    *bufio.Reader
	nextID    int64
	startup   time.Duration
	stderr    strings.Builder
	waitDone  chan error
	closeDone chan struct{}
}

func NewStdioMCPCaller(dataRoot string) MCPCaller {
	root := strings.TrimSpace(dataRoot)
	return func(ctx context.Context, name string, config json.RawMessage, arguments json.RawMessage) (json.RawMessage, error) {
		return callStdioMCP(ctx, root, name, config, arguments)
	}
}

func callStdioMCP(ctx context.Context, dataRoot, name string, configRaw json.RawMessage, arguments json.RawMessage) (json.RawMessage, error) {
	cfg, err := parseStdioMCPConfig(configRaw)
	if err != nil {
		return nil, err
	}
	if cfg.Disabled {
		return nil, fmt.Errorf("mcp %q is disabled", name)
	}

	invocation, err := parseStdioMCPInvocation(arguments)
	if err != nil {
		return nil, err
	}

	client, err := startStdioMCPClient(ctx, dataRoot, cfg)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	if err := client.Initialize(ctx); err != nil {
		return nil, err
	}

	callCtx, cancel := context.WithTimeout(ctx, cfg.callTimeout())
	defer cancel()

	return client.Request(callCtx, invocation.Method, decodeRawJSON(invocation.Params))
}

func parseStdioMCPConfig(raw json.RawMessage) (stdioMCPConfig, error) {
	var cfg stdioMCPConfig
	if len(raw) == 0 {
		return cfg, fmt.Errorf("mcp config is empty")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("decode mcp config: %w", err)
	}
	if strings.TrimSpace(cfg.Command) == "" {
		return cfg, fmt.Errorf("mcp command is required")
	}
	return cfg, nil
}

func parseStdioMCPInvocation(raw json.RawMessage) (stdioMCPInvocation, error) {
	var invocation stdioMCPInvocation
	if len(raw) == 0 {
		return invocation, fmt.Errorf("mcp arguments are required")
	}
	if err := json.Unmarshal(raw, &invocation); err != nil {
		return invocation, fmt.Errorf("decode mcp arguments: %w", err)
	}
	invocation.Method = strings.TrimSpace(invocation.Method)
	if invocation.Method == "" {
		return invocation, fmt.Errorf("mcp method is required")
	}
	return invocation, nil
}

func (cfg stdioMCPConfig) startupTimeout() time.Duration {
	if cfg.StartupTimeoutMS > 0 {
		return time.Duration(cfg.StartupTimeoutMS) * time.Millisecond
	}
	return defaultMCPStartupTimeout
}

func (cfg stdioMCPConfig) callTimeout() time.Duration {
	if cfg.CallTimeoutMS > 0 {
		return time.Duration(cfg.CallTimeoutMS) * time.Millisecond
	}
	return defaultMCPCallTimeout
}

func startStdioMCPClient(ctx context.Context, dataRoot string, cfg stdioMCPConfig) (*stdioMCPClient, error) {
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	cmd.Dir = resolveMCPCwd(dataRoot, cfg.Cwd)
	cmd.Env = mergeMCPEnv(cfg.Env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create mcp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create mcp stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("create mcp stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mcp process: %w", err)
	}

	client := &stdioMCPClient{
		cmd:       cmd,
		stdin:     stdin,
		stdout:    bufio.NewReader(stdout),
		startup:   cfg.startupTimeout(),
		waitDone:  make(chan error, 1),
		closeDone: make(chan struct{}),
	}

	go func() {
		_, _ = io.Copy(&client.stderr, stderr)
	}()
	go func() {
		client.waitDone <- cmd.Wait()
	}()

	return client, nil
}

func resolveMCPCwd(dataRoot, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return dataRoot
	}
	if filepath.IsAbs(trimmed) {
		return trimmed
	}
	if strings.TrimSpace(dataRoot) == "" {
		return trimmed
	}
	return filepath.Join(dataRoot, trimmed)
}

func mergeMCPEnv(extra map[string]string) []string {
	env := append([]string(nil), os.Environ()...)
	for key, value := range extra {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		env = append(env, key+"="+value)
	}
	return env
}

func (c *stdioMCPClient) Initialize(ctx context.Context) error {
	timeout := c.startup
	if timeout <= 0 {
		timeout = defaultMCPStartupTimeout
	}
	initCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := c.Request(initCtx, "initialize", map[string]any{
		"protocolVersion": defaultMCPProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "clisimplehub-entclaw",
			"version": "0.1.0",
		},
	})
	if err != nil {
		return err
	}

	var payload struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		return fmt.Errorf("decode mcp initialize response: %w", err)
	}
	if strings.TrimSpace(payload.ProtocolVersion) == "" {
		return fmt.Errorf("mcp initialize response missing protocolVersion")
	}
	return c.Notify(ctx, "notifications/initialized", nil)
}

func (c *stdioMCPClient) Notify(ctx context.Context, method string, params any) error {
	request := stdioRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	return c.write(ctx, request)
}

func (c *stdioMCPClient) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	request := stdioRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	if err := c.write(ctx, request); err != nil {
		return nil, err
	}

	for {
		line, err := c.readLine(ctx)
		if err != nil {
			return nil, err
		}

		var response stdioRPCResponse
		if err := json.Unmarshal(line, &response); err != nil {
			return nil, fmt.Errorf("decode mcp response: %w", err)
		}
		if response.Method != "" && response.ID == 0 {
			continue
		}
		if response.ID != id {
			continue
		}
		if response.Error != nil {
			return nil, fmt.Errorf("mcp %s failed: code=%d message=%s", method, response.Error.Code, response.Error.Message)
		}
		return append(json.RawMessage(nil), response.Result...), nil
	}
}

func (c *stdioMCPClient) write(ctx context.Context, payload stdioRPCRequest) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal mcp request: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := c.stdin.Write(append(body, '\n'))
		done <- err
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("write mcp request: %w", err)
		}
		return nil
	}
}

func (c *stdioMCPClient) readLine(ctx context.Context) ([]byte, error) {
	type result struct {
		line []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		line, err := c.stdout.ReadBytes('\n')
		done <- result{line: line, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-c.waitDone:
		if err != nil {
			return nil, fmt.Errorf("mcp process exited: %w: %s", err, strings.TrimSpace(c.stderr.String()))
		}
		return nil, io.EOF
	case res := <-done:
		if res.err != nil {
			if res.err == io.EOF {
				waitErr := <-c.waitDone
				if waitErr != nil {
					return nil, fmt.Errorf("mcp stdout closed: %w: %s", waitErr, strings.TrimSpace(c.stderr.String()))
				}
			}
			return nil, res.err
		}
		return []byte(strings.TrimSpace(string(res.line))), nil
	}
}

func (c *stdioMCPClient) Close() error {
	select {
	case <-c.closeDone:
		return nil
	default:
		close(c.closeDone)
	}

	if c.stdin != nil {
		_ = c.stdin.Close()
	}

	select {
	case err := <-c.waitDone:
		if err != nil && !strings.Contains(err.Error(), "killed") {
			return err
		}
	case <-time.After(2 * time.Second):
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	}
	return nil
}

func decodeRawJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}

	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return nil
}
