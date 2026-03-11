package clashplugin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	speedTestTotalTimeout = 3 * time.Second
	tcpDialTimeout        = 5 * time.Second
	tcpSpeedTestAttempts  = 3
)

const speedTestTargetURL = "https://www.gstatic.com/generate_204"

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

	controllerCfg, proxyName, err := resolveSpeedTestController(ctx, svc, node)
	if err != nil {
		if timeoutResult := speedTestResultForContextError(ctx, err, nodeName); timeoutResult != nil {
			return timeoutResult
		}
		result.Error = err.Error()
		return result
	}

	latency, err := getRuntimeProxyDelay(ctx, controllerCfg, proxyName, speedTestTargetURL, speedTestTotalTimeout)
	if err != nil {
		if timeoutResult := speedTestResultForContextError(ctx, err, nodeName); timeoutResult != nil {
			return timeoutResult
		}
		result.Error = fmt.Sprintf("runtime delay: %v", err)
		return result
	}

	result.Latency = latency
	result.Error = ""
	svc.UpdateNodeLatency(nodeName, latency)
	return result
}

func resolveSpeedTestController(ctx context.Context, svc *ClashService, node *ProxyNode) (*runtimeControllerConfig, string, error) {
	if node == nil {
		return nil, "", fmt.Errorf("node is nil")
	}

	cfg := svc.config.Get()
	subID := strings.TrimSpace(nodeSourceID(*node))
	if subID == "" {
		return nil, "", fmt.Errorf("subscription not found for node: %s", nodeName(*node))
	}

	sub := findSubscriptionByID(cfg, subID)
	if sub == nil || !sub.Enabled || len(sub.Nodes) == 0 {
		return nil, "", fmt.Errorf("subscription not available for speed test: %s", subID)
	}

	proxyName, ok := runtimeProxyNameForNode(cfg, sub.ID, nodeName(*node))
	if !ok {
		return nil, "", fmt.Errorf("runtime proxy not found for node: %s", nodeName(*node))
	}

	svc.mu.RLock()
	running := svc.running
	svc.mu.RUnlock()
	if !running {
		return nil, "", fmt.Errorf("clash runtime is not running")
	}

	controllerCfg, err := resolveRuntimeControllerConfig(cfg)
	if err != nil {
		return nil, "", err
	}

	checkCtx, cancel := context.WithTimeout(ctx, runtimeControllerRequestTimeout)
	exists, err := runtimeControllerHasProxy(checkCtx, controllerCfg, proxyName)
	cancel()
	if err != nil {
		return nil, "", err
	}
	if !exists {
		return nil, "", fmt.Errorf("proxy not available in running clash runtime: %s", proxyName)
	}

	return controllerCfg, proxyName, nil
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

func (s *ClashService) CancelSpeedTests() {
	s.speedTestCtrlMu.Lock()
	if s.speedTestRootCancel != nil {
		s.speedTestRootCancel()
	}
	s.speedTestRootCtx, s.speedTestRootCancel = context.WithCancel(context.Background())
	s.speedTestCtrlMu.Unlock()
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
