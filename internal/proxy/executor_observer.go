package proxy

import (
	"strings"
	"time"

	"clisimplehub/internal/executor"
)

// proxyExecutionObserver 负责把执行器侧的事件转为 SSE 广播（解耦 server.go）。
type proxyExecutionObserver struct {
	server *ProxyServer
}

func (o *proxyExecutionObserver) OnRequestStart(requestID string, interfaceType string, endpoint *executor.EndpointConfig, path string) {
}

func (o *proxyExecutionObserver) OnRequestComplete(requestID string, interfaceType string, endpoint *executor.EndpointConfig, result *executor.ForwardResult, duration time.Duration) {
}

func (o *proxyExecutionObserver) OnEndpointSwitch(from, to *executor.EndpointConfig, path string, statusCode int, errorMsg string) {
	if o == nil || o.server == nil {
		return
	}
	o.server.broadcastFallbackSwitch(from, to, path, statusCode, errorMsg)
}

func (o *proxyExecutionObserver) OnEndpointDisabled(interfaceType string, endpoint *executor.EndpointConfig, until time.Time) {
	if o == nil || o.server == nil {
		return
	}
	o.server.broadcastEndpointTempDisabled(interfaceType, endpoint, until)
}

func (o *proxyExecutionObserver) OnDebugLog(requestID string, level int, message string) {
	if o == nil || o.server == nil {
		return
	}

	logDebugToConsole(requestID, level, message)

	if o.server.sseHub != nil {
		o.server.sseHub.BroadcastDebugLog(&DebugLogPayload{
			RequestID: strings.TrimSpace(requestID),
			Level:     level,
			Message:   strings.TrimSpace(message),
		})
	}
}
