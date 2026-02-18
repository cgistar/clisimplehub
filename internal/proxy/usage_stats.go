package proxy

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"clisimplehub/internal/executor"
	"clisimplehub/internal/statsdb"
)

func (p *ProxyServer) insertUsageStat(_ context.Context, interfaceType InterfaceType, endpoint *executor.EndpointConfig, path string, targetHeaders map[string]string, requestBody string, responseBody string, durationMs int64, statusCode int, status string, tokens *executor.TokenUsage) {
	p.mu.RLock()
	usageStats := p.usageStats
	p.mu.RUnlock()

	if usageStats == nil {
		return
	}
	if endpoint == nil {
		return
	}

	endpointID := endpoint.ID
	providerName := strings.TrimSpace(endpoint.ProviderName)
	if providerName == "" {
		providerName = "unknown"
	}
	endpointName := strings.TrimSpace(endpoint.Name)
	if endpointName == "" {
		endpointName = "unknown"
	}

	stat := statsdb.UsageStat{
		EndpointID:    strconv.FormatInt(endpointID, 10),
		EndpointName:  endpointName,
		ProviderName:  providerName,
		Path:          path,
		Date:          time.Now().Format("2006-01-02"),
		InterfaceType: string(interfaceType),
		TargetHeaders: statsdb.MustJSON(targetHeaders),
		RequestBody:   requestBody,
		ResponseBody:  responseBody,
		DurationMs:    durationMs,
		StatusCode:    statusCode,
		Status:        status,
	}

	if tokens != nil {
		stat.InputTokens = tokens.InputTokens
		stat.OutputTokens = tokens.OutputTokens
		stat.CachedCreate = tokens.CachedCreate
		stat.CachedRead = tokens.CachedRead
		stat.Reasoning = tokens.Reasoning
	}

	insertCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := usageStats.InsertUsageStat(insertCtx, stat); err != nil {
		log.Printf("Warning: insert usage_stats failed: %v", err)
	}
}
