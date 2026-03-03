package xrayplugin

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/proxy"
)

const (
	speedTestTimeout     = 10 * time.Second // 增加超时时间到10秒
	instanceStartupDelay = 500 * time.Millisecond
	maxRetries           = 2 // 最大重试次数
)

// 多个测试目标，提高成功率
var testTargets = []string{
	"http://www.gstatic.com/generate_204",
	"http://cp.cloudflare.com/generate_204",
	"http://connectivitycheck.platform.hicloud.com/generate_204",
}

// testSingleNode tests a single node's latency using a temporary xray instance.
func testSingleNode(ctx context.Context, svc *XRayService, nodeName string) *SpeedTestResult {
	result := &SpeedTestResult{NodeName: nodeName, Latency: -1}

	node := svc.findNodeSafe(nodeName)
	if node == nil {
		result.Error = "node not found"
		return result
	}

	// Reserve a free port from the OS and start a temp instance
	var (
		port    int
		inst    io.Closer
		lastErr error
	)
	for attempt := 0; attempt < 3; attempt++ {
		port, lastErr = reserveLocalPort()
		if lastErr != nil {
			continue
		}
		tempCfg := &XRayConfig{
			SocksListen: "127.0.0.1",
			SocksPort:   port,
			LogLevel:    "none",
		}
		runtimeJSON, err := BuildRuntimeJSON(node, tempCfg)
		if err != nil {
			result.Error = fmt.Sprintf("build config: %v", err)
			return result
		}
		inst, lastErr = startXRayInstance(runtimeJSON)
		if lastErr == nil {
			break
		}
	}
	if inst == nil {
		result.Error = fmt.Sprintf("start instance: %v", lastErr)
		return result
	}
	defer inst.Close()

	// Small delay for the instance to bind (increased for stability)
	time.Sleep(instanceStartupDelay)

	// Test via SOCKS5 proxy with retry mechanism
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

	// 尝试多个测试目标，取最快的结果
	var bestLatency int = -1
	var lastError error

	for _, target := range testTargets {
		for retry := 0; retry <= maxRetries; retry++ {
			start := time.Now()
			req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
			if err != nil {
				lastError = err
				continue
			}

			resp, err := client.Do(req)
			if err != nil {
				lastError = err
				if retry < maxRetries {
					time.Sleep(100 * time.Millisecond) // 重试前短暂延迟
					continue
				}
				break
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			latency := int(time.Since(start).Milliseconds())
			if bestLatency < 0 || latency < bestLatency {
				bestLatency = latency
			}
			// 成功测速后跳出重试循环
			break
		}
		// 如果已经有成功的测速结果，不再尝试其他目标
		if bestLatency > 0 {
			break
		}
	}

	if bestLatency < 0 {
		result.Error = fmt.Sprintf("connect: %v", lastError)
		return result
	}

	result.Latency = bestLatency
	result.Error = ""

	// Update node latency in service
	svc.UpdateNodeLatency(nodeName, bestLatency)

	log.Printf("[xray] speed test: %s = %dms", nodeName, bestLatency)
	return result
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
