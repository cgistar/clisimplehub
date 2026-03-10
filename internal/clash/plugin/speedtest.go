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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/tunnel"
	"gopkg.in/yaml.v3"
)

const (
	speedTestTotalTimeout = 3 * time.Second
	tcpDialTimeout        = 5 * time.Second
	tcpSpeedTestAttempts  = 3
	instanceStartupDelay  = 500 * time.Millisecond
)

const speedTestTargetURL = "https://www.gstatic.com/generate_204"

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

func timeoutSpeedTestResult(nodeName string) *SpeedTestResult {
	return &SpeedTestResult{
		NodeName: nodeName,
		Latency:  -1,
		Error:    "timeout",
	}
}

func isSpeedTestCanceled(ctx context.Context, err error) bool {
	return errors.Is(err, context.Canceled) || (ctx != nil && errors.Is(ctx.Err(), context.Canceled))
}

func isSpeedTestTimeout(ctx context.Context, err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || (ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded))
}

func speedTestResultForContextError(ctx context.Context, err error, nodeName string) *SpeedTestResult {
	switch {
	case isSpeedTestCanceled(ctx, err):
		return canceledSpeedTestResult(nodeName)
	case isSpeedTestTimeout(ctx, err):
		return timeoutSpeedTestResult(nodeName)
	default:
		return nil
	}
}

func testSingleNode(ctx context.Context, svc *ClashService, nodeName string) *SpeedTestResult {
	result := &SpeedTestResult{NodeName: nodeName, Latency: -1}
	ctx, done := svc.newSpeedTestContext(ctx)
	defer done()
	ctx, cancel := context.WithTimeout(ctx, speedTestTotalTimeout)
	defer cancel()

	node := svc.findNodeSafe(nodeName)
	if node == nil {
		result.Error = "node not found"
		return result
	}

	runtimeProxy, err := getSpeedTestProxy(svc, node)
	if err != nil {
		if timeoutResult := speedTestResultForContextError(ctx, err, nodeName); timeoutResult != nil {
			return timeoutResult
		}
		result.Error = err.Error()
		return result
	}

	latency, err := runURLTest(ctx, runtimeProxy)
	if err != nil {
		if timeoutResult := speedTestResultForContextError(ctx, err, nodeName); timeoutResult != nil {
			return timeoutResult
		}
		result.Error = fmt.Sprintf("runtime urltest: %v", err)
		return result
	}

	result.Latency = latency
	result.Error = ""
	svc.UpdateNodeLatency(nodeName, latency)
	return result
}

func getSpeedTestProxy(svc *ClashService, node *ProxyNode) (C.Proxy, error) {
	if node == nil {
		return nil, fmt.Errorf("node is nil")
	}

	proxyName := runtimeProxyName("", nodeSourceID(*node), nodeName(*node))
	svc.mu.RLock()
	running := svc.running
	svc.mu.RUnlock()
	if running {
		if runtimeProxy, ok := tunnel.Proxies()[proxyName]; ok {
			return runtimeProxy, nil
		}
	}

	subID := strings.TrimSpace(nodeSourceID(*node))
	if subID == "" {
		return nil, fmt.Errorf("subscription not found for node: %s", nodeName(*node))
	}

	sub := findSubscriptionByID(svc.config.Get(), subID)
	if sub == nil || !sub.Enabled || len(sub.Nodes) == 0 {
		return nil, fmt.Errorf("subscription not available for speed test: %s", subID)
	}

	if err := ensureSpeedTestRuntimeForSubscription(svc, sub); err != nil {
		return nil, err
	}
	runtimeProxy, ok := tunnel.Proxies()[proxyName]
	if !ok {
		return nil, fmt.Errorf("proxy not found in speedtest runtime: %s", proxyName)
	}
	return runtimeProxy, nil
}

func findSubscriptionByID(cfg *ClashConfig, id string) *Subscription {
	if cfg == nil {
		return nil
	}
	for i := range cfg.Subscriptions {
		if cfg.Subscriptions[i].ID == id {
			return &cfg.Subscriptions[i]
		}
	}
	return nil
}

func runURLTest(ctx context.Context, runtimeProxy C.Proxy) (int, error) {
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	delay, err := runtimeProxy.URLTest(ctx, speedTestTargetURL, nil)
	if err != nil {
		return -1, err
	}
	if delay <= 0 {
		return -1, fmt.Errorf("empty delay result")
	}
	return int(delay), nil
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
		name := runtimeProxyName(sub.Name, sub.ID, nodeName(node))
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

	runtimeCfg := buildRuntimeBaseConfig(cfg)
	runtimeCfg["profile"] = map[string]any{
		"store-selected": false,
	}
	runtimeCfg["proxies"] = proxies
	runtimeCfg["proxy-groups"] = []any{
		map[string]any{
			"name":    runtimeGroupSelector,
			"type":    "select",
			"proxies": proxyNames,
		},
	}
	runtimeCfg["rules"] = []string{"MATCH," + runtimeGroupSelector}

	runtimeCfg, err := mergeRuntimeConfigWithUserYAML(runtimeCfg, cfg,
		"mixed-port", "bind-address", "allow-lan", "mode", "log-level", "ipv6", "profile", "proxies", "proxy-groups", "rules",
	)
	if err != nil {
		return nil, err
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
	ctx, cancel := context.WithTimeout(ctx, speedTestTotalTimeout)
	defer cancel()

	result := &SpeedTestResult{NodeName: nodeName, Latency: -1}

	node := svc.findNodeSafe(nodeName)
	if node == nil {
		result.Error = "node not found"
		return result
	}

	server := strings.TrimSpace(nodeServer(*node))
	port := nodePort(*node)
	if server == "" || port <= 0 {
		result.Error = "invalid node server or port"
		return result
	}

	addr := net.JoinHostPort(server, strconv.Itoa(port))
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
		if timeoutResult := speedTestResultForContextError(ctx, lastErr, nodeName); timeoutResult != nil {
			return timeoutResult
		}
		result.Error = fmt.Sprintf("tcp connect: %v", lastErr)
		return result
	}

	result.Latency = bestLatency
	result.Error = ""
	svc.UpdateNodeLatency(nodeName, bestLatency)
	return result
}
