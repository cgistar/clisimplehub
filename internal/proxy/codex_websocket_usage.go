package proxy

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"clisimplehub/internal/executor"
	"clisimplehub/internal/plugin"
)

func (p *ProxyServer) recordCodexUsage(endpoint *executor.EndpointConfig, record plugin.CodexUsageRecord) {
	if p == nil || endpoint == nil {
		return
	}
	tokens := codexUsageRecordTokens(record)
	if !codexTokenUsageEmpty(tokens) && p.stats != nil {
		p.stats.RecordTokens(endpoint.Name, &TokenUsage{
			InputTokens:  tokens.InputTokens,
			OutputTokens: tokens.OutputTokens,
			TotalTokens:  tokens.TotalTokens,
			CachedCreate: tokens.CachedCreate,
			CachedRead:   tokens.CachedRead,
			Reasoning:    tokens.Reasoning,
		})
	}

	path := strings.TrimSpace(record.Path)
	if path == "" {
		path = "/v1/responses"
	}
	if !ShouldRecordUsageStats(InterfaceTypeCodex, path) {
		return
	}
	statusCode := record.StatusCode
	if statusCode <= 0 {
		statusCode = http.StatusBadGateway
	}
	status := strings.TrimSpace(record.Status)
	if status == "error" {
		status = fmt.Sprintf("error_%d", statusCode)
	}
	p.insertUsageStat(
		context.Background(),
		InterfaceTypeCodex,
		endpoint,
		path,
		nil,
		"",
		"",
		record.Duration.Milliseconds(),
		statusCode,
		status,
		tokens,
	)
}

func codexUsageRecordTokens(record plugin.CodexUsageRecord) *executor.TokenUsage {
	tokens := &executor.TokenUsage{
		InputTokens:  record.Tokens.InputTokens,
		OutputTokens: record.Tokens.OutputTokens,
		TotalTokens:  record.Tokens.TotalTokens,
		CachedCreate: record.Tokens.CachedCreate,
		CachedRead:   record.Tokens.CachedRead,
		Reasoning:    record.Tokens.Reasoning,
	}
	for _, additional := range record.AdditionalModels {
		tokens.InputTokens += additional.Tokens.InputTokens
		tokens.OutputTokens += additional.Tokens.OutputTokens
		tokens.TotalTokens += additional.Tokens.TotalTokens
		tokens.CachedCreate += additional.Tokens.CachedCreate
		tokens.CachedRead += additional.Tokens.CachedRead
		tokens.Reasoning += additional.Tokens.Reasoning
	}
	return tokens
}

func codexTokenUsageEmpty(tokens *executor.TokenUsage) bool {
	return tokens == nil || (tokens.InputTokens == 0 &&
		tokens.OutputTokens == 0 &&
		tokens.TotalTokens == 0 &&
		tokens.CachedCreate == 0 &&
		tokens.CachedRead == 0 &&
		tokens.Reasoning == 0)
}
