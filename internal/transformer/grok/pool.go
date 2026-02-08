package grok

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"

	"clisimplehub/internal/transformer/grok/shared"
)

// GrokAccountPool manages account selection with rotation strategies.
type GrokAccountPool struct {
	mu         sync.RWMutex
	config     *shared.GrokMultiConfig
	configPath string
	failed     map[string]shared.GrokAccountStatus
}

var (
	globalPool   *GrokAccountPool
	globalPoolMu sync.Mutex
)

func InitPool(grokJsonPath string) error {
	globalPoolMu.Lock()
	defer globalPoolMu.Unlock()
	if strings.TrimSpace(grokJsonPath) == "" {
		return fmt.Errorf("grokJsonPath is empty")
	}
	cfg, err := shared.LoadGrokMultiConfig(grokJsonPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to load grok config: %w", err)
	}
	pool := &GrokAccountPool{
		config:     cfg,
		configPath: grokJsonPath,
		failed:     make(map[string]shared.GrokAccountStatus),
	}
	globalPool = pool
	return nil
}

func GetPool() *GrokAccountPool {
	globalPoolMu.Lock()
	defer globalPoolMu.Unlock()
	return globalPool
}

func (p *GrokAccountPool) Select() *shared.GrokAccount {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.config == nil {
		return nil
	}
	switch p.config.GetRotationMode() {
	case shared.RotationFailover:
		return p.selectFailover()
	case shared.RotationLoadBalance:
		return p.selectLoadBalance()
	default:
		return p.selectFixed()
	}
}

func (p *GrokAccountPool) MarkFailed(ssoToken string, status shared.GrokAccountStatus) error {
	if p == nil {
		return fmt.Errorf("pool is nil")
	}
	tok := strings.TrimSpace(ssoToken)
	if tok == "" {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.config == nil {
		return fmt.Errorf("pool config is nil")
	}
	if p.failed == nil {
		p.failed = make(map[string]shared.GrokAccountStatus)
	}
	p.failed[tok] = status
	for i := range p.config.Accounts {
		if strings.TrimSpace(p.config.Accounts[i].SsoToken) == tok {
			p.config.Accounts[i].Status = status
			break
		}
	}
	_ = shared.SaveGrokMultiConfig(p.configPath, p.config)
	mode := p.config.GetRotationMode()
	if mode == shared.RotationFailover {
		if strings.TrimSpace(p.config.ActiveSsoToken) == tok {
			if next := p.findNextAvailable(tok); next != nil {
				p.config.ActiveSsoToken = next.SsoToken
				_ = shared.SaveGrokMultiConfig(p.configPath, p.config)
			}
		}
	}
	return nil
}

func (p *GrokAccountPool) ReportSuccess(ssoToken string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.failed, shared.NormalizeSsoToken(ssoToken))
}

func (p *GrokAccountPool) Reload() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.TrimSpace(p.configPath) == "" {
		return
	}
	cfg, err := shared.LoadGrokMultiConfig(p.configPath)
	if err != nil || cfg == nil {
		return
	}
	p.config = cfg
	p.failed = make(map[string]shared.GrokAccountStatus)
}

// --- Internal selection strategies ---

func (p *GrokAccountPool) selectFixed() *shared.GrokAccount {
	active := p.config.FindAccountBySsoToken(p.config.ActiveSsoToken)
	if active == nil {
		return nil
	}
	if _, isFailed := p.failed[active.SsoToken]; isFailed {
		return nil
	}
	return cloneAccount(active)
}

func (p *GrokAccountPool) selectFailover() *shared.GrokAccount {
	active := p.config.FindAccountBySsoToken(p.config.ActiveSsoToken)
	if active != nil {
		if _, isFailed := p.failed[active.SsoToken]; !isFailed {
			return cloneAccount(active)
		}
	}
	next := p.findNextAvailable(p.config.ActiveSsoToken)
	if next != nil {
		p.config.ActiveSsoToken = next.SsoToken
		_ = shared.SaveGrokMultiConfig(p.configPath, p.config)
	}
	return cloneAccount(next)
}

func (p *GrokAccountPool) selectLoadBalance() *shared.GrokAccount {
	avail := p.availableAccounts()
	if len(avail) == 0 {
		return nil
	}
	totalWeight := 0
	for _, a := range avail {
		totalWeight += a.EffectiveWeight()
	}
	if totalWeight == 0 {
		return cloneAccount(avail[0])
	}
	r := rand.Intn(totalWeight)
	for _, a := range avail {
		r -= a.EffectiveWeight()
		if r < 0 {
			return cloneAccount(a)
		}
	}
	return cloneAccount(avail[len(avail)-1])
}

func (p *GrokAccountPool) availableAccounts() []*shared.GrokAccount {
	var result []*shared.GrokAccount
	for i := range p.config.Accounts {
		a := &p.config.Accounts[i]
		if strings.TrimSpace(a.SsoToken) == "" {
			continue
		}
		if _, isFailed := p.failed[a.SsoToken]; isFailed {
			continue
		}
		if a.Status == shared.GrokStatusInvalid || a.Status == shared.GrokStatusCooling {
			continue
		}
		result = append(result, a)
	}
	return result
}

func (p *GrokAccountPool) findNextAvailable(currentToken string) *shared.GrokAccount {
	accounts := p.availableAccounts()
	if len(accounts) == 0 {
		return nil
	}
	startIdx := 0
	for i, a := range accounts {
		if a.SsoToken == currentToken {
			startIdx = i + 1
			break
		}
	}
	return accounts[startIdx%len(accounts)]
}

func cloneAccount(a *shared.GrokAccount) *shared.GrokAccount {
	if a == nil {
		return nil
	}
	cp := *a
	return &cp
}

func (p *GrokAccountPool) GetConfig() *shared.GrokMultiConfig {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.config
}

func GetSettings() *shared.GrokSettings {
	pool := GetPool()
	if pool == nil {
		return nil
	}
	cfg := pool.GetConfig()
	if cfg == nil {
		return nil
	}
	return &cfg.Settings
}

func (p *GrokAccountPool) Mode() string {
	if p == nil || p.config == nil {
		return shared.RotationFixed
	}
	return p.config.GetRotationMode()
}

func (p *GrokAccountPool) TotalCount() int {
	if p == nil || p.config == nil {
		return 0
	}
	return len(p.config.Accounts)
}

// GetAccountStats returns statistics about account status for diagnostics.
func (p *GrokAccountPool) GetAccountStats() map[string]int {
	if p == nil || p.config == nil {
		return map[string]int{"total": 0}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := map[string]int{
		"total":   len(p.config.Accounts),
		"valid":   0,
		"invalid": 0,
		"cooling": 0,
		"unknown": 0,
		"failed":  len(p.failed),
	}

	for i := range p.config.Accounts {
		switch p.config.Accounts[i].Status {
		case shared.GrokStatusValid:
			stats["valid"]++
		case shared.GrokStatusInvalid:
			stats["invalid"]++
		case shared.GrokStatusCooling:
			stats["cooling"]++
		default:
			stats["unknown"]++
		}
	}

	return stats
}
