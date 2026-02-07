package kiro

import (
	"fmt"
	"strings"
	"sync"

	"clisimplehub/internal/transformer/kiro/shared"
)

// KiroAccountPool manages account selection with rotation strategies.
// It is purely an account selector — no auth/HTTP logic.
type KiroAccountPool struct {
	mu         sync.RWMutex
	config     *shared.KiroMultiConfig
	configPath string

	// WRR state
	wrrCounters []int

	// Failed accounts: refreshToken -> status
	failed map[string]shared.KiroAccountStatus
}

var (
	globalPool   *KiroAccountPool
	globalPoolMu sync.Mutex
)

// InitPool initializes the global account pool from kiro.json.
func InitPool(kiroJsonPath string) error {
	globalPoolMu.Lock()
	defer globalPoolMu.Unlock()

	if strings.TrimSpace(kiroJsonPath) == "" {
		return fmt.Errorf("kiroJsonPath is empty")
	}

	cfg, err := shared.LoadKiroMultiConfig(kiroJsonPath)
	if err != nil {
		// No kiro.json — pool stays nil (single-account fallback)
		return nil
	}

	pool := &KiroAccountPool{
		config:     cfg,
		configPath: kiroJsonPath,
		failed:     make(map[string]shared.KiroAccountStatus),
	}
	pool.resetWRR()
	globalPool = pool
	return nil
}

// GetPool returns the global pool (may be nil if not initialized).
func GetPool() *KiroAccountPool {
	globalPoolMu.Lock()
	defer globalPoolMu.Unlock()
	return globalPool
}

// Select picks an account according to rotationMode.
func (p *KiroAccountPool) Select() *shared.KiroAccount {
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

// MarkFailed marks an account as failed and persists status to kiro.json.
func (p *KiroAccountPool) MarkFailed(refreshToken string, status shared.KiroAccountStatus) error {
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
		p.failed = make(map[string]shared.KiroAccountStatus)
	}

	p.failed[rt] = status

	// Update account status in config
	for i := range p.config.Accounts {
		if strings.TrimSpace(p.config.Accounts[i].RefreshToken) == rt {
			p.config.Accounts[i].Status = status
			break
		}
	}

	// Persist
	_ = shared.SaveKiroMultiConfig(p.configPath, p.config)

	mode := p.config.GetRotationMode()

	// Failover: if the failed account is the active one, advance
	if mode == shared.RotationFailover {
		if strings.TrimSpace(p.config.ActiveRefreshToken) == rt {
			if next := p.findNextAvailable(rt); next != nil {
				p.config.ActiveRefreshToken = next.RefreshToken
				_ = shared.SaveKiroMultiConfig(p.configPath, p.config)
			}
		}
	}

	// Loadbalance: rebuild WRR counters
	if mode == shared.RotationLoadBalance {
		p.resetWRR()
	}
	return nil
}

// ReportSuccess clears the failed flag for an account.
func (p *KiroAccountPool) ReportSuccess(refreshToken string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.failed, refreshToken)
}

// AvailableCount returns the number of non-failed accounts.
func (p *KiroAccountPool) AvailableCount() int {
	if p == nil {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.availableAccounts())
}

// TotalCount returns the total number of accounts.
func (p *KiroAccountPool) TotalCount() int {
	if p == nil {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.config.Accounts)
}

// Mode returns the current rotation mode.
func (p *KiroAccountPool) Mode() string {
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

// Reload reloads kiro.json and resets WRR / failed state.
func (p *KiroAccountPool) Reload() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if strings.TrimSpace(p.configPath) == "" {
		return
	}

	cfg, err := shared.LoadKiroMultiConfig(p.configPath)
	if err != nil || cfg == nil {
		return
	}
	p.config = cfg
	p.failed = make(map[string]shared.KiroAccountStatus)
	p.resetWRR()
}

// --- Internal selection strategies ---

func (p *KiroAccountPool) selectFixed() *shared.KiroAccount {
	active := p.config.FindAccountByRefreshToken(p.config.ActiveRefreshToken)
	if active == nil {
		return nil
	}
	if _, isFailed := p.failed[active.RefreshToken]; isFailed {
		return nil
	}
	return cloneAccount(active)
}

func (p *KiroAccountPool) selectFailover() *shared.KiroAccount {
	active := p.config.FindAccountByRefreshToken(p.config.ActiveRefreshToken)
	if active != nil {
		if _, isFailed := p.failed[active.RefreshToken]; !isFailed {
			return cloneAccount(active)
		}
	}
	// Active is failed or nil; find next available
	next := p.findNextAvailable(p.config.ActiveRefreshToken)
	if next != nil {
		p.config.ActiveRefreshToken = next.RefreshToken
		_ = shared.SaveKiroMultiConfig(p.configPath, p.config)
	}
	return cloneAccount(next)
}

func (p *KiroAccountPool) selectLoadBalance() *shared.KiroAccount {
	avail := p.availableAccounts()
	if len(avail) == 0 {
		return nil
	}

	// Ensure WRR counters match available set
	if len(p.wrrCounters) != len(avail) {
		p.resetWRR()
		avail = p.availableAccounts()
		if len(avail) == 0 {
			return nil
		}
	}

	// Smooth WRR (Nginx-style)
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

// --- Helpers ---

func (p *KiroAccountPool) availableAccounts() []*shared.KiroAccount {
	var result []*shared.KiroAccount
	for i := range p.config.Accounts {
		a := &p.config.Accounts[i]
		if strings.TrimSpace(a.RefreshToken) == "" {
			continue
		}
		if _, isFailed := p.failed[a.RefreshToken]; isFailed {
			continue
		}
		if a.Status == shared.KiroStatusBanned || a.Status == shared.KiroStatusExhausted {
			continue
		}
		result = append(result, a)
	}
	return result
}

func (p *KiroAccountPool) findNextAvailable(currentRT string) *shared.KiroAccount {
	accounts := p.availableAccounts()
	if len(accounts) == 0 {
		return nil
	}
	// Find position after current, wrapping around
	startIdx := 0
	for i, a := range accounts {
		if a.RefreshToken == currentRT {
			startIdx = i + 1
			break
		}
	}
	return accounts[startIdx%len(accounts)]
}

func cloneAccount(a *shared.KiroAccount) *shared.KiroAccount {
	if a == nil {
		return nil
	}
	cp := *a
	return &cp
}

func (p *KiroAccountPool) resetWRR() {
	avail := p.availableAccounts()
	p.wrrCounters = make([]int, len(avail))
}
