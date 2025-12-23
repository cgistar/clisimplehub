package proxy

import (
	"strings"
	"time"

	"clisimplehub/internal/executor"
)

type proxyExecutor struct {
	provider executor.EndpointProvider
	ctx      *executor.ExecutionContext
	retry    *executor.RetryExecutor
	observer *proxyExecutionObserver
}

func (p *ProxyServer) ensureExecutor() *proxyExecutor {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.exec != nil {
		return p.exec
	}

	provider := newRouterEndpointProvider(p.router)
	execCtx := executor.NewExecutionContext(provider)
	observer := &proxyExecutionObserver{server: p}
	execCtx.SetObserver(observer)

	p.exec = &proxyExecutor{
		provider: provider,
		ctx:      execCtx,
		retry:    executor.NewRetryExecutor(execCtx, executor.DefaultRetryConfig()),
		observer: observer,
	}
	return p.exec
}

type routerEndpointProvider struct {
	router Router
}

func newRouterEndpointProvider(router Router) *routerEndpointProvider {
	return &routerEndpointProvider{router: router}
}

func (p *routerEndpointProvider) DetectInterfaceType(path string) string {
	if p.router == nil {
		return ""
	}
	return string(p.router.DetectInterfaceType(path))
}

func (p *routerEndpointProvider) GetActiveEndpoint(interfaceType string) *executor.EndpointConfig {
	if p.router == nil {
		return nil
	}
	ep := p.router.GetActiveEndpoint(InterfaceType(normalizeInterfaceType(interfaceType)))
	return toExecutorEndpointConfig(ep)
}

func (p *routerEndpointProvider) GetEndpointsByType(interfaceType string) []*executor.EndpointConfig {
	if p.router == nil {
		return nil
	}
	eps := p.router.GetEndpointsByType(InterfaceType(normalizeInterfaceType(interfaceType)))
	if len(eps) == 0 {
		return nil
	}
	result := make([]*executor.EndpointConfig, 0, len(eps))
	for _, ep := range eps {
		if ep == nil || !ep.Enabled {
			continue
		}
		result = append(result, toExecutorEndpointConfig(ep))
	}
	return result
}

func (p *routerEndpointProvider) GetNextEndpoint(interfaceType string, current *executor.EndpointConfig) *executor.EndpointConfig {
	if p.router == nil {
		return nil
	}
	next := p.router.GetNextEndpoint(InterfaceType(normalizeInterfaceType(interfaceType)), proxyEndpointFromConfig(current))
	return toExecutorEndpointConfig(next)
}

func (p *routerEndpointProvider) FindNextUntried(interfaceType string, current *executor.EndpointConfig, exhausted map[string]bool) *executor.EndpointConfig {
	if p.router == nil {
		return nil
	}

	it := InterfaceType(normalizeInterfaceType(interfaceType))
	eps := p.router.GetEndpointsByType(it)
	if len(eps) == 0 {
		return nil
	}

	currentIdx := -1
	for i, ep := range eps {
		if current == nil || ep == nil {
			continue
		}
		if current.ID != 0 {
			if ep.ID == current.ID {
				currentIdx = i
				break
			}
			continue
		}
		if current.Name != "" && ep.Name == current.Name {
			currentIdx = i
			break
		}
	}

	for i := currentIdx + 1; i < len(eps); i++ {
		ep := eps[i]
		if ep == nil || !ep.Enabled {
			continue
		}
		if exhausted[endpointKeyFromProxy(ep)] {
			continue
		}
		return toExecutorEndpointConfig(ep)
	}

	for i := 0; i < currentIdx; i++ {
		ep := eps[i]
		if ep == nil || !ep.Enabled {
			continue
		}
		if exhausted[endpointKeyFromProxy(ep)] {
			continue
		}
		return toExecutorEndpointConfig(ep)
	}

	return nil
}

func (p *routerEndpointProvider) DisableEndpoint(interfaceType string, endpoint *executor.EndpointConfig) time.Time {
	if p.router == nil || endpoint == nil {
		return time.Time{}
	}
	return p.router.DisableEndpoint(InterfaceType(normalizeInterfaceType(interfaceType)), proxyEndpointFromConfig(endpoint))
}

func (p *routerEndpointProvider) SetActiveEndpoint(interfaceType string, endpoint *executor.EndpointConfig) error {
	if p.router == nil || endpoint == nil {
		return ErrEndpointNotFound
	}
	return p.router.SetActiveEndpoint(InterfaceType(normalizeInterfaceType(interfaceType)), proxyEndpointFromConfig(endpoint))
}

func normalizeInterfaceType(interfaceType string) string {
	return strings.ToLower(strings.TrimSpace(interfaceType))
}

func proxyEndpointFromConfig(cfg *executor.EndpointConfig) *Endpoint {
	if cfg == nil {
		return nil
	}
	return &Endpoint{ID: cfg.ID, Name: cfg.Name}
}

func toExecutorEndpointConfig(ep *Endpoint) *executor.EndpointConfig {
	if ep == nil {
		return nil
	}
	return &executor.EndpointConfig{
		ID:            ep.ID,
		Name:          ep.Name,
		APIURL:        ep.APIURL,
		APIKey:        ep.APIKey,
		InterfaceType: ep.InterfaceType,
		Transformer:   ep.Transformer,
		VendorID:      ep.VendorID,
		Model:         ep.Model,
		ProxyURL:      ep.ProxyURL,
		Models:        toExecutorModelMappings(ep.Models),
		Headers:       cloneStringMap(ep.Headers),
	}
}

func toExecutorModelMappings(models []ModelMapping) []executor.ModelMapping {
	if len(models) == 0 {
		return nil
	}
	out := make([]executor.ModelMapping, 0, len(models))
	for _, m := range models {
		out = append(out, executor.ModelMapping{Name: m.Name, Alias: m.Alias})
	}
	return out
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func endpointKeyFromProxy(ep *Endpoint) string {
	if ep == nil {
		return ""
	}
	return executor.EndpointKey(&executor.EndpointConfig{
		ID:   ep.ID,
		Name: ep.Name,
	})
}
