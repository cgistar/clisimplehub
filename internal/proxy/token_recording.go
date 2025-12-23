package proxy

import "clisimplehub/internal/executor"

func (p *ProxyServer) recordTokens(endpoint *executor.EndpointConfig, result *executor.ForwardResult) {
	if p == nil || p.stats == nil || endpoint == nil || result == nil || result.Tokens == nil {
		return
	}
	p.stats.RecordTokens(endpoint.Name, &TokenUsage{
		InputTokens:  result.Tokens.InputTokens,
		OutputTokens: result.Tokens.OutputTokens,
		CachedCreate: result.Tokens.CachedCreate,
		CachedRead:   result.Tokens.CachedRead,
		Reasoning:    result.Tokens.Reasoning,
	})
}
