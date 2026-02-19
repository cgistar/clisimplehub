package codex

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"clisimplehub/internal/codex/shared"
)

type CodexAccountPool struct {
	mu         sync.RWMutex
	config     *shared.CodexMultiConfig
	configPath string
	wrrCounters []int
	failed     map[string]shared.CodexAccountStatus
}

var (
	globalPool   *CodexAccountPool
	globalPoolMu sync.Mutex
)

func InitPool(codexJsonPath string) error {
	globalPoolMu.Lock()
	defer globalPoolMu.Unlock()

	if strings.TrimSpace(codexJsonPath) == "" {
		return fmt.Errorf("codexJsonPath is empty")
	}

	cfg, err := shared.LoadCodexMultiConfig(codexJsonPath)
	if err != nil {
		return nil
	}

	pool := &CodexAccountPool{
		config:     cfg,
		configPath: codexJsonPath,
		failed:     make(map[string]shared.CodexAccountStatus),
	}
	pool.resetWRR()
	globalPool = pool
	return nil
}

func GetPool() *CodexAccountPool {
	globalPoolMu.Lock()
	defer globalPoolMu.Unlock()
	return globalPool
}

func (p *CodexAccountPool) Select() *shared.CodexAccount {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.config == nil {
		return nil
	}

	mode := p.config.GetRotationMode()
	switch mode {
	case shared.RotationFixed:
		return p.selectFixed()
	case shared.RotationFailover:
		return p.selectFailover()
	case shared.RotationLoadBalance:
		return p.selectLoadBalance()
	default:
		return p.selectFixed()
	}
}

func (p *CodexAccountPool) MarkFailed(refreshToken string, status shared.CodexAccountStatus, cooldownDuration time.Duration, cooldownReason string) error {
	if p == nil {
		return fmt.Errorf("pool is nil")
	}
	rt := strings.TrimSpace(refreshToken)
	if rt == "" {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.config == nil {
		return fmt.Errorf("pool config is nil")
	}
	if p.failed == nil {
		p.failed = make(map[string]shared.CodexAccountStatus)
	}

	// Add to failed map for permanent failures.
	// Rate-limited accounts use cooldown only and auto-recover.
	if status == shared.CodexStatusBanned || status == shared.CodexStatusExhausted || status == shared.CodexStatusReused {
		p.failed[rt] = status
	}

	for i := range p.config.Accounts {
		if strings.TrimSpace(p.config.Accounts[i].RefreshToken) == rt {
			if status == shared.CodexStatusBanned || status == shared.CodexStatusExhausted || status == shared.CodexStatusReused {
				p.config.Accounts[i].Status = status
			}
			if cooldownDuration > 0 {
				p.config.Accounts[i].CooldownUntil = time.Now().Add(cooldownDuration)
				p.config.Accounts[i].CooldownReason = cooldownReason
			}
			break
		}
	}

	_ = shared.SaveCodexMultiConfig(p.configPath, p.config)

	mode := p.config.GetRotationMode()
	if mode == shared.RotationFailover {
		if strings.TrimSpace(p.config.ActiveRefreshToken) == rt {
			if next := p.findNextAvailable(rt); next != nil {
				p.config.ActiveRefreshToken = next.RefreshToken
				_ = shared.SaveCodexMultiConfig(p.configPath, p.config)
			}
		}
	}
	if mode == shared.RotationLoadBalance {
		p.resetWRR()
	}
	return nil
}

func (p *CodexAccountPool) ReportSuccess(refreshToken string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.failed, refreshToken)
}

func (p *CodexAccountPool) UpdateUsageSnapshot(refreshToken string, snapshot *shared.CodexUsageSnapshot) {
	if p == nil || snapshot == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.config == nil {
		return
	}
	for i := range p.config.Accounts {
		if p.config.Accounts[i].RefreshToken == refreshToken {
			p.config.Accounts[i].CodexUsage = snapshot
			_ = shared.SaveCodexMultiConfig(p.configPath, p.config)
			return
		}
	}
}

func (p *CodexAccountPool) Reload() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if strings.TrimSpace(p.configPath) == "" {
		return
	}

	cfg, err := shared.LoadCodexMultiConfig(p.configPath)
	if err != nil || cfg == nil {
		return
	}
	p.config = cfg
	p.failed = make(map[string]shared.CodexAccountStatus)
	p.resetWRR()
}

func (p *CodexAccountPool) Mode() string {
	if p == nil {
		return shared.RotationFixed
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.config == nil {
		return shared.RotationFixed
	}
	return p.config.GetRotationMode()
}

func (p *CodexAccountPool) ConfigPath() string {
	if p == nil {
		return ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.configPath
}

func (p *CodexAccountPool) ProxyURL() string {
	if p == nil {
		return ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.config == nil {
		return ""
	}
	return p.config.ProxyUrl
}

// --- Internal selection strategies ---

func (p *CodexAccountPool) selectFixed() *shared.CodexAccount {
	active := p.config.FindAccountByRefreshToken(p.config.ActiveRefreshToken)
	if active == nil {
		return nil
	}
	active.ClearCooldownIfExpired()
	if active.IsCoolingDown() {
		return nil
	}
	if _, isFailed := p.failed[active.RefreshToken]; isFailed {
		return nil
	}
	return cloneAccount(active)
}

func (p *CodexAccountPool) selectFailover() *shared.CodexAccount {
	active := p.config.FindAccountByRefreshToken(p.config.ActiveRefreshToken)
	if active != nil {
		active.ClearCooldownIfExpired()
		if !active.IsCoolingDown() {
			if _, isFailed := p.failed[active.RefreshToken]; !isFailed {
				return cloneAccount(active)
			}
		}
	}
	next := p.findNextAvailable(p.config.ActiveRefreshToken)
	if next != nil {
		p.config.ActiveRefreshToken = next.RefreshToken
		_ = shared.SaveCodexMultiConfig(p.configPath, p.config)
	}
	return cloneAccount(next)
}

func (p *CodexAccountPool) selectLoadBalance() *shared.CodexAccount {
	avail := p.availableAccounts()
	if len(avail) == 0 {
		return nil
	}

	// Skip accounts with primary usage >= 95% when alternatives exist
	if len(avail) > 1 {
		var filtered []*shared.CodexAccount
		for _, a := range avail {
			if a.CodexUsage != nil && a.CodexUsage.PrimaryUsedPercent >= 95 {
				continue
			}
			filtered = append(filtered, a)
		}
		if len(filtered) > 0 {
			avail = filtered
		}
	}

	if len(p.wrrCounters) != len(avail) {
		p.wrrCounters = make([]int, len(avail))
	}

	totalWeight := 0
	for _, a := range avail {
		totalWeight += a.EffectiveWeight()
	}
	if totalWeight == 0 {
		totalWeight = len(avail)
	}

	bestIdx := 0
	for i := range avail {
		p.wrrCounters[i] += avail[i].EffectiveWeight()
		if p.wrrCounters[i] > p.wrrCounters[bestIdx] {
			bestIdx = i
		}
	}
	p.wrrCounters[bestIdx] -= totalWeight
	return cloneAccount(avail[bestIdx])
}

func (p *CodexAccountPool) availableAccounts() []*shared.CodexAccount {
	var result []*shared.CodexAccount
	for i := range p.config.Accounts {
		a := &p.config.Accounts[i]
		if strings.TrimSpace(a.RefreshToken) == "" {
			continue
		}
		if _, isFailed := p.failed[a.RefreshToken]; isFailed {
			continue
		}
		if a.Status == shared.CodexStatusBanned || a.Status == shared.CodexStatusExhausted || a.Status == shared.CodexStatusReused {
			continue
		}
		a.ClearCooldownIfExpired()
		if a.IsCoolingDown() {
			continue
		}
		result = append(result, a)
	}
	return result
}

func (p *CodexAccountPool) findNextAvailable(currentRT string) *shared.CodexAccount {
	accounts := p.availableAccounts()
	if len(accounts) == 0 {
		return nil
	}
	startIdx := 0
	for i, a := range accounts {
		if a.RefreshToken == currentRT {
			startIdx = i + 1
			break
		}
	}
	return accounts[startIdx%len(accounts)]
}

func cloneAccount(a *shared.CodexAccount) *shared.CodexAccount {
	if a == nil {
		return nil
	}
	cp := *a
	if a.CodexUsage != nil {
		usageCopy := *a.CodexUsage
		cp.CodexUsage = &usageCopy
	}
	return &cp
}

func (p *CodexAccountPool) resetWRR() {
	avail := p.availableAccounts()
	p.wrrCounters = make([]int, len(avail))
}
