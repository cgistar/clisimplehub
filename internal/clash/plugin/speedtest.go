package clashplugin

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/tunnel"
	"golang.org/x/net/proxy"
	"gopkg.in/yaml.v3"
)

const (
	speedTestTimeout     = 10 * time.Second
	tcpDialTimeout       = 5 * time.Second
	tcpSpeedTestAttempts = 3
	instanceStartupDelay = 500 * time.Millisecond
	maxRetries           = 2
)

var testTargets = []string{
	"http://www.gstatic.com/generate_204",
	"https://www.msftconnecttest.com/connecttest.txt",
	"https://cp.cloudflare.com/generate_204",
}

type speedTestRuntimeState struct {
	subscriptionID string
	subDigest      string
	instance       io.Closer
}

var speedTestRuntimeMu sync.Mutex
var speedTestRuntime *speedTestRuntimeState

func (s *ClashService) newSpeedTestContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}

	s.speedTestCtrlMu.Lock()
	root := s.speedTestRootCtx
	if root == nil {
		root, s.speedTestRootCancel = context.WithCancel(context.Background())
		s.speedTestRootCtx = root
	}
	s.speedTestCtrlMu.Unlock()

	return mergeContexts(parent, root)
}

func mergeContexts(a, b context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-a.Done():
			cancel()
		case <-b.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func canceledSpeedTestResult(nodeName string) *SpeedTestResult {
	return &SpeedTestResult{
		NodeName: nodeName,
		Latency:  0,
		Error:    "canceled",
	}
}

func isSpeedTestCanceled(ctx context.Context, err error) bool {
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	return errors.Is(err, context.Canceled)
}

func testSingleNode(ctx context.Context, svc *ClashService, nodeName string) *SpeedTestResult {
	result := &SpeedTestResult{NodeName: nodeName, Latency: -1}
	ctx, done := svc.newSpeedTestContext(ctx)
	defer done()

	node := svc.findNodeSafe(nodeName)
	if node == nil {
		result.Error = "node not found"
		return result
	}

	if runtimeResult, handled := testSingleNodeWithRunningRuntime(ctx, svc, node); handled {
		return runtimeResult
	}
	if runtimeResult, handled := testSingleNodeWithSubscriptionRuntime(ctx, svc, node); handled {
		return runtimeResult
	}

	speedTestRuntimeMu.Lock()
	defer speedTestRuntimeMu.Unlock()

	var (
		port    int
		instErr error
		runtime []byte
	)
	for attempt := 0; attempt < 3; attempt++ {
		port, instErr = reserveLocalPort()
		if instErr != nil {
			continue
		}
		tempCfg := &ClashConfig{
			SocksListen: "127.0.0.1",
			SocksPort:   port,
			LogLevel:    "silent",
		}
		runtime, instErr = BuildRuntimeYAMLForSingle(node, tempCfg)
		if instErr == nil {
			break
		}
	}
	if instErr != nil {
		result.Error = fmt.Sprintf("build runtime: %v", instErr)
		return result
	}

	inst, err := startMihomoInstance(runtime, filepath.Join(svc.dataDir, "speedtest"))
	if err != nil {
		if isSpeedTestCanceled(ctx, err) {
			return canceledSpeedTestResult(nodeName)
		}
		result.Error = fmt.Sprintf("start instance: %v", err)
		return result
	}
	defer inst.Close()

	timer := time.NewTimer(instanceStartupDelay)
	select {
	case <-ctx.Done():
		timer.Stop()
		return canceledSpeedTestResult(nodeName)
	case <-timer.C:
	}

	socksAddr := fmt.Sprintf("127.0.0.1:%d", port)
	dialer, err := proxy.SOCKS5("tcp", socksAddr, nil, proxy.Direct)
	if err != nil {
		result.Error = fmt.Sprintf("socks5 dial: %v", err)
		return result
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   speedTestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	bestLatency := -1
	var lastError error

	for _, target := range testTargets {
		for retry := 0; retry <= maxRetries; retry++ {
			start := time.Now()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
			if err != nil {
				lastError = err
				continue
			}

			resp, err := client.Do(req)
			if err != nil {
				lastError = err
				if retry < maxRetries {
					time.Sleep(100 * time.Millisecond)
					continue
				}
				break
			}
			_ = resp.Body.Close()

			latency := int(time.Since(start).Milliseconds())
			if bestLatency < 0 || latency < bestLatency {
				bestLatency = latency
			}
			break
		}
		if bestLatency > 0 {
			break
		}
	}

	if bestLatency < 0 {
		if isSpeedTestCanceled(ctx, lastError) {
			return canceledSpeedTestResult(nodeName)
		}
		result.Error = fmt.Sprintf("connect: %v", lastError)
		return result
	}

	result.Latency = bestLatency
	result.Error = ""
	svc.UpdateNodeLatency(nodeName, bestLatency)
	return result
}

func testSingleNodeWithRunningRuntime(ctx context.Context, svc *ClashService, node *ProxyNode) (*SpeedTestResult, bool) {
	if node == nil {
		return &SpeedTestResult{NodeName: "", Latency: -1, Error: "node is nil"}, true
	}

	svc.mu.RLock()
	running := svc.running
	svc.mu.RUnlock()
	if !running {
		return nil, false
	}

	result := &SpeedTestResult{NodeName: node.Name, Latency: -1}
	proxyName := runtimeProxyName(node.SourceID, node.Name)
	runtimeProxy, ok := tunnel.Proxies()[proxyName]
	if !ok {
		return nil, false
	}

	bestLatency, lastErr := runURLTestTargets(ctx, runtimeProxy)

	if bestLatency < 0 {
		if isSpeedTestCanceled(ctx, lastErr) {
			return canceledSpeedTestResult(node.Name), true
		}
		result.Error = fmt.Sprintf("runtime urltest: %v", lastErr)
		return result, true
	}

	result.Latency = bestLatency
	result.Error = ""
	svc.UpdateNodeLatency(node.Name, bestLatency)
	return result, true
}

func testSingleNodeWithSubscriptionRuntime(ctx context.Context, svc *ClashService, node *ProxyNode) (*SpeedTestResult, bool) {
	if node == nil {
		return &SpeedTestResult{NodeName: "", Latency: -1, Error: "node is nil"}, true
	}
	subID := strings.TrimSpace(node.SourceID)
	if subID == "" {
		return nil, false
	}

	cfg := svc.config.Get()
	var sub *Subscription
	for i := range cfg.Subscriptions {
		if cfg.Subscriptions[i].ID == subID {
			sub = &cfg.Subscriptions[i]
			break
		}
	}
	if sub == nil || !sub.Enabled || len(sub.Nodes) == 0 {
		return nil, false
	}

	if err := ensureSpeedTestRuntimeForSubscription(svc, sub); err != nil {
		if isSpeedTestCanceled(ctx, err) {
			return canceledSpeedTestResult(node.Name), true
		}
		// Fall back to the legacy per-node temporary runtime when subscription runtime is unavailable.
		return nil, false
	}

	result := &SpeedTestResult{NodeName: node.Name, Latency: -1}
	runtimeProxy, ok := tunnel.Proxies()[runtimeProxyName(subID, node.Name)]
	if !ok {
		return nil, false
	}

	bestLatency, lastErr := runURLTestTargets(ctx, runtimeProxy)
	if bestLatency < 0 {
		if isSpeedTestCanceled(ctx, lastErr) {
			return canceledSpeedTestResult(node.Name), true
		}
		result.Error = fmt.Sprintf("runtime urltest: %v", lastErr)
		return result, true
	}

	result.Latency = bestLatency
	result.Error = ""
	svc.UpdateNodeLatency(node.Name, bestLatency)
	return result, true
}

func runURLTestTargets(ctx context.Context, runtimeProxy C.Proxy) (int, error) {
	bestLatency := -1
	var lastErr error
	for _, target := range testTargets {
		for retry := 0; retry <= maxRetries; retry++ {
			if ctx.Err() != nil {
				return -1, ctx.Err()
			}
			testCtx, cancel := context.WithTimeout(ctx, speedTestTimeout)
			delay, err := runtimeProxy.URLTest(testCtx, target, nil)
			cancel()
			if err != nil || delay == 0 {
				lastErr = err
				if retry < maxRetries {
					time.Sleep(100 * time.Millisecond)
					continue
				}
				break
			}
			latency := int(delay)
			if bestLatency < 0 || latency < bestLatency {
				bestLatency = latency
			}
			break
		}
		if bestLatency > 0 {
			break
		}
	}
	return bestLatency, lastErr
}

func ensureSpeedTestRuntimeForSubscription(svc *ClashService, sub *Subscription) error {
	if svc == nil || sub == nil {
		return fmt.Errorf("service/subscription is nil")
	}
	if !sub.Enabled {
		return fmt.Errorf("subscription disabled")
	}
	if len(sub.Nodes) == 0 {
		return fmt.Errorf("subscription has no nodes")
	}

	digest, err := subscriptionDigest(sub)
	if err != nil {
		return err
	}

	speedTestRuntimeMu.Lock()
	defer speedTestRuntimeMu.Unlock()

	if speedTestRuntime != nil &&
		speedTestRuntime.instance != nil &&
		speedTestRuntime.subscriptionID == sub.ID &&
		speedTestRuntime.subDigest == digest {
		return nil
	}

	closeSpeedTestRuntimeLocked()

	port, err := reserveLocalPort()
	if err != nil {
		return fmt.Errorf("reserve speedtest port: %w", err)
	}
	tempCfg := &ClashConfig{
		SocksListen: "127.0.0.1",
		SocksPort:   port,
		LogLevel:    "silent",
	}
	runtimeYAML, err := buildRuntimeYAMLForSubscription(sub, tempCfg)
	if err != nil {
		return err
	}

	inst, err := startMihomoInstance(runtimeYAML, filepath.Join(svc.dataDir, "speedtest"))
	if err != nil {
		return fmt.Errorf("start subscription speedtest runtime: %w", err)
	}
	speedTestRuntime = &speedTestRuntimeState{
		subscriptionID: sub.ID,
		subDigest:      digest,
		instance:       inst,
	}

	time.Sleep(instanceStartupDelay)
	return nil
}

func buildRuntimeYAMLForSubscription(sub *Subscription, cfg *ClashConfig) ([]byte, error) {
	if sub == nil {
		return nil, fmt.Errorf("subscription is nil")
	}
	if len(sub.Nodes) == 0 {
		return nil, fmt.Errorf("subscription has no nodes")
	}

	proxies := make([]any, 0, len(sub.Nodes))
	proxyNames := make([]string, 0, len(sub.Nodes))
	usedNames := make(map[string]struct{}, len(sub.Nodes))

	for i := range sub.Nodes {
		node := sub.Nodes[i]
		name := runtimeProxyName(sub.ID, node.Name)
		if _, exists := usedNames[name]; exists {
			for suffix := 2; ; suffix++ {
				candidate := fmt.Sprintf("%s (%d)", name, suffix)
				if _, collision := usedNames[candidate]; collision {
					continue
				}
				name = candidate
				break
			}
		}
		usedNames[name] = struct{}{}

		nodeCopy := node
		proxyMap, err := buildProxyMap(name, &nodeCopy)
		if err != nil {
			return nil, fmt.Errorf("build speedtest proxy: %w", err)
		}
		proxies = append(proxies, proxyMap)
		proxyNames = append(proxyNames, name)
	}
	if len(proxies) == 0 {
		return nil, fmt.Errorf("subscription has no valid proxies")
	}

	runtimeCfg := map[string]any{
		"socks-port":   socksPort(cfg),
		"bind-address": socksListen(cfg),
		"allow-lan":    false,
		"mode":         "rule",
		"log-level":    clashLogLevel(cfg),
		"ipv6":         false,
		"profile": map[string]any{
			"store-selected": false,
		},
		"dns": map[string]any{
			"enable":             true,
			"ipv6":               false,
			"default-nameserver": []string{"223.5.5.5", "1.1.1.1"},
			"nameserver":         []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"},
		},
		"proxies": proxies,
		"proxy-groups": []any{
			map[string]any{
				"name":    runtimeGroupSelector,
				"type":    "select",
				"proxies": proxyNames,
			},
		},
		"rules": []string{"MATCH," + runtimeGroupSelector},
	}
	return yaml.Marshal(runtimeCfg)
}

func subscriptionDigest(sub *Subscription) (string, error) {
	if sub == nil {
		return "", fmt.Errorf("subscription is nil")
	}
	data, err := json.Marshal(sub.Nodes)
	if err != nil {
		return "", fmt.Errorf("marshal subscription nodes: %w", err)
	}
	sum := sha1.Sum(data)
	return hex.EncodeToString(sum[:]), nil
}

func closeSpeedTestRuntime() {
	speedTestRuntimeMu.Lock()
	defer speedTestRuntimeMu.Unlock()
	closeSpeedTestRuntimeLocked()
}

func closeSpeedTestRuntimeLocked() {
	if speedTestRuntime == nil {
		return
	}
	if speedTestRuntime.instance != nil {
		_ = speedTestRuntime.instance.Close()
	}
	speedTestRuntime = nil
}

func (s *ClashService) CancelSpeedTests() {
	s.speedTestCtrlMu.Lock()
	if s.speedTestRootCancel != nil {
		s.speedTestRootCancel()
	}
	s.speedTestRootCtx, s.speedTestRootCancel = context.WithCancel(context.Background())
	s.speedTestCtrlMu.Unlock()

	closeSpeedTestRuntime()
}

func reserveLocalPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected addr type: %T", l.Addr())
	}
	return addr.Port, nil
}

func testSingleNodeTCP(ctx context.Context, svc *ClashService, nodeName string) *SpeedTestResult {
	ctx, done := svc.newSpeedTestContext(ctx)
	defer done()

	result := &SpeedTestResult{NodeName: nodeName, Latency: -1}

	node := svc.findNodeSafe(nodeName)
	if node == nil {
		result.Error = "node not found"
		return result
	}

	server := strings.TrimSpace(node.Server)
	if server == "" || node.Port <= 0 {
		result.Error = "invalid node server or port"
		return result
	}

	addr := net.JoinHostPort(server, strconv.Itoa(node.Port))
	dialer := &net.Dialer{}

	bestLatency := -1
	var lastErr error

	for attempt := 0; attempt < tcpSpeedTestAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, tcpDialTimeout)
		start := time.Now()
		conn, err := dialer.DialContext(attemptCtx, "tcp", addr)
		cancel()
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				break
			}
			continue
		}

		latency := int(time.Since(start).Milliseconds())
		_ = conn.Close()
		if bestLatency < 0 || latency < bestLatency {
			bestLatency = latency
		}
	}

	if bestLatency < 0 {
		if isSpeedTestCanceled(ctx, lastErr) {
			return canceledSpeedTestResult(nodeName)
		}
		result.Error = fmt.Sprintf("tcp connect: %v", lastErr)
		return result
	}

	result.Latency = bestLatency
	result.Error = ""
	svc.UpdateNodeLatency(nodeName, bestLatency)
	return result
}
