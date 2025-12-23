package proxy

import (
	"strings"
	"time"

	"clisimplehub/internal/executor"
)

func (p *ProxyServer) getVendorNameByID(vendorID int64) string {
	if vendorID == 0 {
		return "unknown"
	}
	p.mu.RLock()
	store := p.store
	p.mu.RUnlock()

	if store == nil {
		return "unknown"
	}
	if vendor, err := store.GetVendorByID(vendorID); err == nil && vendor != nil && strings.TrimSpace(vendor.Name) != "" {
		return vendor.Name
	}
	return "unknown"
}

func (p *ProxyServer) broadcastFallbackSwitch(fromEndpoint, toEndpoint *executor.EndpointConfig, path string, statusCode int, errorMsg string) {
	if p == nil || p.wsHub == nil || !p.IsFallbackEnabled() {
		return
	}

	payload := &FallbackSwitchPayload{
		FromVendor:   p.getVendorNameByID(vendorIDOf(fromEndpoint)),
		FromEndpoint: "",
		ToVendor:     p.getVendorNameByID(vendorIDOf(toEndpoint)),
		ToEndpoint:   "",
		Path:         path,
		StatusCode:   statusCode,
		ErrorMessage: errorMsg,
	}
	if fromEndpoint != nil {
		payload.FromEndpoint = fromEndpoint.Name
	}
	if toEndpoint != nil {
		payload.ToEndpoint = toEndpoint.Name
	}

	p.wsHub.BroadcastFallbackSwitch(payload)
}

func (p *ProxyServer) broadcastEndpointTempDisabled(interfaceType string, endpoint *executor.EndpointConfig, disabledUntil time.Time) {
	if p == nil || p.wsHub == nil || endpoint == nil || disabledUntil.IsZero() {
		return
	}

	p.wsHub.BroadcastEndpointTempDisabled(&EndpointTempDisabledPayload{
		InterfaceType: strings.TrimSpace(interfaceType),
		EndpointID:    endpoint.ID,
		EndpointName:  endpoint.Name,
		DisabledUntil: unixMillis(disabledUntil),
	})
}

func vendorIDOf(endpoint *executor.EndpointConfig) int64 {
	if endpoint == nil {
		return 0
	}
	return endpoint.VendorID
}
