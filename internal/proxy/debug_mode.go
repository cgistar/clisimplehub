package proxy

import "strings"

func (p *ProxyServer) isDebugModeAll() bool {
	p.mu.RLock()
	store := p.store
	p.mu.RUnlock()

	if store == nil {
		return false
	}
	v, err := store.GetConfig("debugMode")
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(v), "all")
}

