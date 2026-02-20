package codex

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"clisimplehub/internal/codex/shared"
)

type CodexAccountPool struct {
	mu              sync.RWMutex
	accounts        []shared.CodexAccount
	store           shared.CodexAccountStore
	config          *shared.CodexMultiConfig
	configPath      string
	activeAccountID string
	wrrCounters     []int
	failed          map[string]shared.CodexAccountStatus
}

var (
	globalPool   *CodexAccountPool
	globalPoolMu sync.Mutex
)

func InitPool(codexJsonPath string, store shared.CodexAccountStore) error {
	globalPoolMu.Lock()
	defer globalPoolMu.Unlock()

	if strings.TrimSpace(codexJsonPath) == "" {
		return fmt.Errorf("codexJsonPath is empty")
	}

	cfg, err := shared.LoadCodexMultiConfig(codexJsonPath)
	if err != nil {
		cfg = &shared.CodexMultiConfig{}
	}

	pool := &CodexAccountPool{
		config:          cfg,
		configPath:      codexJsonPath,
		store:           store,
		activeAccountID: cfg.ActiveAccountID,
		failed:          make(map[string]shared.CodexAccountStatus),
	}

	if store != nil {
		accounts, err := store.ListAccounts(context.Background())
		if err != nil {
			log.Printf("[codex-pool] failed to load accounts from store: %v", err)
		} else {
			pool.accounts = accounts
		}
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

func (p *CodexAccountPool) Store() shared.CodexAccountStore {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.store
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

func (p *CodexAccountPool) MarkFailed(accountId string, status shared.CodexAccountStatus, cooldownDuration time.Duration, cooldownReason string) error {
	if p == nil {
		return fmt.Errorf("pool is nil")
	}
	id := strings.TrimSpace(accountId)
	if id == "" {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.failed == nil {
		p.failed = make(map[string]shared.CodexAccountStatus)
	}

	if status == shared.CodexStatusBanned || status == shared.CodexStatusExhausted || status == shared.CodexStatusReused {
		p.failed[id] = status
	}

	var cooldownUntil time.Time
	if cooldownDuration > 0 {
		cooldownUntil = time.Now().Add(cooldownDuration)
	}

	for i := range p.accounts {
		if strings.TrimSpace(p.accounts[i].AccountID) == id {
			if status == shared.CodexStatusBanned || status == shared.CodexStatusExhausted || status == shared.CodexStatusReused {
				p.accounts[i].Status = status
			}
			if cooldownDuration > 0 {
				p.accounts[i].CooldownUntil = cooldownUntil
				p.accounts[i].CooldownReason = cooldownReason
			}
			break
		}
	}

	// Async persist to store
	if p.store != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if status == shared.CodexStatusBanned || status == shared.CodexStatusExhausted || status == shared.CodexStatusReused {
				_ = p.store.UpdateStatus(ctx, id, status)
			}
			if cooldownDuration > 0 {
				_ = p.store.UpdateCooldown(ctx, id, cooldownUntil, cooldownReason)
			}
		}()
	}

	mode := p.config.GetRotationMode()
	if mode == shared.RotationFailover {
		if p.activeAccountID == id {
			if next := p.findNextAvailable(id); next != nil {
				p.activeAccountID = next.AccountID
				p.config.ActiveAccountID = next.AccountID
				_ = shared.SaveCodexMultiConfig(p.configPath, p.config)
			}
		}
	}
	if mode == shared.RotationLoadBalance {
		p.resetWRR()
	}
	return nil
}

func (p *CodexAccountPool) ReportSuccess(accountId string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.failed, accountId)
}

func (p *CodexAccountPool) UpdateUsageSnapshot(accountId string, snapshot *shared.CodexUsageSnapshot) {
	if p == nil || snapshot == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	for i := range p.accounts {
		if p.accounts[i].AccountID == accountId {
			p.accounts[i].CodexUsage = snapshot
			break
		}
	}

	if p.store != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = p.store.UpdateUsageSnapshot(ctx, accountId, snapshot)
		}()
	}
}

func (p *CodexAccountPool) Reload() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if strings.TrimSpace(p.configPath) != "" {
		if cfg, err := shared.LoadCodexMultiConfig(p.configPath); err == nil && cfg != nil {
			p.config = cfg
			p.activeAccountID = cfg.ActiveAccountID
		}
	}

	if p.store != nil {
		if accounts, err := p.store.ListAccounts(context.Background()); err == nil {
			p.accounts = accounts
		}
	}

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

func (p *CodexAccountPool) ActiveAccountID() string {
	if p == nil {
		return ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.activeAccountID
}

// --- Internal selection strategies ---

func (p *CodexAccountPool) resolveActiveAccount() *shared.CodexAccount {
	activeID := strings.TrimSpace(p.activeAccountID)
	if activeID != "" {
		for i := range p.accounts {
			if strings.TrimSpace(p.accounts[i].AccountID) == activeID {
				return &p.accounts[i]
			}
		}
	}
	next := p.findNextAvailable("")
	if next == nil {
		return nil
	}
	p.activeAccountID = next.AccountID
	p.config.ActiveAccountID = next.AccountID
	_ = shared.SaveCodexMultiConfig(p.configPath, p.config)
	return next
}

func (p *CodexAccountPool) selectFixed() *shared.CodexAccount {
	active := p.resolveActiveAccount()
	if active == nil {
		return nil
	}

	active.ClearCooldownIfExpired()
	if active.IsCoolingDown() {
		return nil
	}
	if _, isFailed := p.failed[active.AccountID]; isFailed {
		return nil
	}
	return cloneAccount(active)
}

func (p *CodexAccountPool) selectFailover() *shared.CodexAccount {
	active := p.resolveActiveAccount()
	if active != nil {
		active.ClearCooldownIfExpired()
		if !active.IsCoolingDown() {
			if _, isFailed := p.failed[active.AccountID]; !isFailed {
				return cloneAccount(active)
			}
		}
	}

	currentID := ""
	if active != nil {
		currentID = active.AccountID
	}
	next := p.findNextAvailable(currentID)
	if next != nil && (active == nil || next.AccountID != active.AccountID) {
		p.activeAccountID = next.AccountID
		p.config.ActiveAccountID = next.AccountID
		_ = shared.SaveCodexMultiConfig(p.configPath, p.config)
	}
	return cloneAccount(next)
}

func (p *CodexAccountPool) selectLoadBalance() *shared.CodexAccount {
	avail := p.availableAccounts()
	if len(avail) == 0 {
		return nil
	}

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
	for i := range p.accounts {
		a := &p.accounts[i]
		if strings.TrimSpace(a.AccountID) == "" {
			continue
		}
		if _, isFailed := p.failed[a.AccountID]; isFailed {
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

func (p *CodexAccountPool) findNextAvailable(currentAccountID string) *shared.CodexAccount {
	accounts := p.availableAccounts()
	if len(accounts) == 0 {
		return nil
	}
	startIdx := 0
	for i, a := range accounts {
		if a.AccountID == currentAccountID {
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
