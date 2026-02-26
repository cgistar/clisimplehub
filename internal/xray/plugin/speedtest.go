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

const speedTestTimeout = 6 * time.Second

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

	// Small delay for the instance to bind
	time.Sleep(200 * time.Millisecond)

	// Test via SOCKS5 proxy
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

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", "http://www.gstatic.com/generate_204", nil)
	if err != nil {
		result.Error = fmt.Sprintf("request: %v", err)
		return result
	}

	resp, err := client.Do(req)
	if err != nil {
		result.Error = fmt.Sprintf("connect: %v", err)
		return result
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	latency := int(time.Since(start).Milliseconds())
	result.Latency = latency
	result.Error = ""

	// Update node latency in service
	svc.UpdateNodeLatency(nodeName, latency)

	log.Printf("[xray] speed test: %s = %dms", nodeName, latency)
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
