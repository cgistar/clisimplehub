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

func (p *ProxyServer) insertVendorStat(ctx context.Context, interfaceType InterfaceType, endpoint *executor.EndpointConfig, path string, targetHeaders map[string]string, durationMs int64, statusCode int, status string, tokens *executor.TokenUsage) {
	p.mu.RLock()
	store := p.store
	vendorStats := p.vendorStats
	p.mu.RUnlock()

	if vendorStats == nil {
		return
	}
	if endpoint == nil {
		return
	}

	vendorID := endpoint.VendorID
	endpointID := endpoint.ID
	vendorName := "unknown"
	endpointName := strings.TrimSpace(endpoint.Name)
	if endpointName == "" {
		endpointName = "unknown"
	}

	if store != nil && vendorID != 0 {
		if vendor, err := store.GetVendorByID(vendorID); err == nil && vendor != nil && strings.TrimSpace(vendor.Name) != "" {
			vendorName = vendor.Name
		}
	}

	stat := statsdb.VendorStat{
		VendorID:      strconv.FormatInt(vendorID, 10),
		VendorName:    vendorName,
		EndpointID:    strconv.FormatInt(endpointID, 10),
		EndpointName:  endpointName,
		Path:          path,
		Date:          time.Now().Format("2006-01-02"),
		InterfaceType: string(interfaceType),
		TargetHeaders: statsdb.MustJSON(targetHeaders),
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

	if err := vendorStats.InsertVendorStat(insertCtx, stat); err != nil {
		log.Printf("Warning: insert vendor_stats failed: %v", err)
	}
}
