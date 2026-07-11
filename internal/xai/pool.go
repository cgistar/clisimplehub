package xai

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"clisimplehub/internal/xai/shared"
)

type XaiAccountPool struct {
	mu              sync.RWMutex
	config          *shared.XaiMultiConfig
	configPath      string
	activeAccountID string
	wrrCounters     []int
	failed          map[string]shared.XaiAccountStatus
}

var (
	globalPool   *XaiAccountPool
	globalPoolMu sync.Mutex
)

func InitPool(xaiJsonPath string) error {
	globalPoolMu.Lock()
	defer globalPoolMu.Unlock()

	if strings.TrimSpace(xaiJsonPath) == "" {
		return fmt.Errorf("xaiJsonPath is empty")
	}

	cfg, err := shared.LoadXaiMultiConfig(xaiJsonPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		cfg = &shared.XaiMultiConfig{
			RotationMode: shared.RotationFixed,
			Config:       shared.DefaultXaiConfig(),
			Accounts:     []shared.XaiAccount{},
		}
	}

	SortAccounts(cfg.Accounts)
	pool := &XaiAccountPool{
		config:          cfg,
		configPath:      xaiJsonPath,
		activeAccountID: strings.TrimSpace(cfg.ActiveAccountID),
		failed:          make(map[string]shared.XaiAccountStatus),
	}
	pool.activeAccountID = resolveConfiguredActiveID(pool.activeAccountID, cfg.Accounts)
	pool.config.ActiveAccountID = pool.activeAccountID
	pool.resetWRR()
	globalPool = pool
	return nil
}

func GetPool() *XaiAccountPool {
	globalPoolMu.Lock()
	defer globalPoolMu.Unlock()
	return globalPool
}

func SortAccounts(accounts []shared.XaiAccount) {
	sort.Slice(accounts, func(i, j int) bool {
		left := accounts[i]
		right := accounts[j]
		if left.EffectiveWeight() != right.EffectiveWeight() {
			return left.EffectiveWeight() > right.EffectiveWeight()
		}
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return strings.TrimSpace(left.ID) < strings.TrimSpace(right.ID)
	})
}

func resolveConfiguredActiveID(activeID string, accounts []shared.XaiAccount) string {
	activeID = strings.TrimSpace(activeID)
	if len(accounts) == 0 {
		return ""
	}
	if activeID != "" {
		for i := range accounts {
			if strings.TrimSpace(accounts[i].ID) == activeID {
				return activeID
			}
		}
	}
	return strings.TrimSpace(accounts[0].ID)
}

func (p *XaiAccountPool) ConfigPath() string {
	if p == nil {
		return ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.configPath
}

func (p *XaiAccountPool) Snapshot() *shared.XaiMultiConfig {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.config == nil {
		return &shared.XaiMultiConfig{Config: shared.DefaultXaiConfig(), Accounts: []shared.XaiAccount{}}
	}
	cp := *p.config
	cp.Accounts = append([]shared.XaiAccount(nil), p.config.Accounts...)
	return &cp
}

func (p *XaiAccountPool) ListAccounts() []shared.XaiAccount {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.config == nil {
		return nil
	}
	out := make([]shared.XaiAccount, len(p.config.Accounts))
	copy(out, p.config.Accounts)
	return out
}

func (p *XaiAccountPool) ActiveAccountID() string {
	if p == nil {
		return ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.activeAccountID
}

func (p *XaiAccountPool) SetActiveAccount(accountID string) error {
	if p == nil {
		return fmt.Errorf("pool is nil")
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return fmt.Errorf("accountId is required")
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.config == nil {
		return fmt.Errorf("pool config is nil")
	}
	found := false
	for i := range p.config.Accounts {
		if strings.TrimSpace(p.config.Accounts[i].ID) == accountID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("account not found: %s", accountID)
	}
	p.activeAccountID = accountID
	p.config.ActiveAccountID = accountID
	return shared.SaveXaiMultiConfig(p.configPath, p.config)
}

func (p *XaiAccountPool) UpsertAccount(account shared.XaiAccount) (*shared.XaiAccount, error) {
	if p == nil {
		return nil, fmt.Errorf("pool is nil")
	}
	shared.NormalizeAccount(&account)
	if strings.TrimSpace(account.ID) == "" {
		return nil, fmt.Errorf("account id is empty")
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.config == nil {
		p.config = &shared.XaiMultiConfig{Config: shared.DefaultXaiConfig()}
	}

	now := time.Now()
	for i := range p.config.Accounts {
		if strings.TrimSpace(p.config.Accounts[i].ID) == account.ID {
			if account.CreatedAt.IsZero() {
				account.CreatedAt = p.config.Accounts[i].CreatedAt
			}
			account.UpdatedAt = now
			p.config.Accounts[i] = account
			SortAccounts(p.config.Accounts)
			if err := shared.SaveXaiMultiConfig(p.configPath, p.config); err != nil {
				return nil, err
			}
			out := p.config.Accounts[i]
			return &out, nil
		}
	}

	if account.CreatedAt.IsZero() {
		account.CreatedAt = now
	}
	account.UpdatedAt = now
	p.config.Accounts = append(p.config.Accounts, account)
	if strings.TrimSpace(p.config.ActiveAccountID) == "" {
		p.config.ActiveAccountID = account.ID
		p.activeAccountID = account.ID
	}
	SortAccounts(p.config.Accounts)
	p.resetWRR()
	if err := shared.SaveXaiMultiConfig(p.configPath, p.config); err != nil {
		return nil, err
	}
	return &account, nil
}

func (p *XaiAccountPool) DeleteAccounts(accountIDs []string) (int, error) {
	if p == nil {
		return 0, fmt.Errorf("pool is nil")
	}
	targets := make(map[string]struct{}, len(accountIDs))
	for _, id := range accountIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			targets[id] = struct{}{}
		}
	}
	if len(targets) == 0 {
		return 0, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.config == nil {
		return 0, nil
	}

	next := make([]shared.XaiAccount, 0, len(p.config.Accounts))
	deleted := 0
	activeDeleted := false
	for i := range p.config.Accounts {
		id := strings.TrimSpace(p.config.Accounts[i].ID)
		if _, ok := targets[id]; ok {
			deleted++
			if id == p.activeAccountID {
				activeDeleted = true
			}
			delete(p.failed, id)
			continue
		}
		next = append(next, p.config.Accounts[i])
	}
	if deleted == 0 {
		return 0, nil
	}
	p.config.Accounts = next
	if len(next) == 0 {
		p.activeAccountID = ""
		p.config.ActiveAccountID = ""
	} else if activeDeleted || strings.TrimSpace(p.activeAccountID) == "" {
		p.activeAccountID = strings.TrimSpace(next[0].ID)
		p.config.ActiveAccountID = p.activeAccountID
	}
	p.resetWRR()
	if err := shared.SaveXaiMultiConfig(p.configPath, p.config); err != nil {
		return deleted, err
	}
	return deleted, nil
}

func (p *XaiAccountPool) SaveGlobalConfig(rotationMode, proxyURL string, cfg shared.XaiConfig) error {
	if p == nil {
		return fmt.Errorf("pool is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.config == nil {
		p.config = &shared.XaiMultiConfig{}
	}
	mode := strings.ToLower(strings.TrimSpace(rotationMode))
	switch mode {
	case shared.RotationFixed, shared.RotationFailover, shared.RotationLoadBalance:
		p.config.RotationMode = mode
	default:
		p.config.RotationMode = shared.RotationFixed
	}
	p.config.ProxyUrl = strings.TrimSpace(proxyURL)
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = shared.DefaultXaiConfig().BaseURL
	}
	p.config.Config = cfg
	p.resetWRR()
	return shared.SaveXaiMultiConfig(p.configPath, p.config)
}

func (p *XaiAccountPool) Reload() error {
	if p == nil {
		return fmt.Errorf("pool is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	cfg, err := shared.LoadXaiMultiConfig(p.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			p.config = &shared.XaiMultiConfig{
				RotationMode: shared.RotationFixed,
				Config:       shared.DefaultXaiConfig(),
				Accounts:     []shared.XaiAccount{},
			}
			p.activeAccountID = ""
			p.failed = make(map[string]shared.XaiAccountStatus)
			p.resetWRR()
			return nil
		}
		return err
	}
	SortAccounts(cfg.Accounts)
	p.config = cfg
	p.activeAccountID = resolveConfiguredActiveID(cfg.ActiveAccountID, cfg.Accounts)
	p.config.ActiveAccountID = p.activeAccountID
	p.failed = make(map[string]shared.XaiAccountStatus)
	p.resetWRR()
	return nil
}

func (p *XaiAccountPool) Select() *shared.XaiAccount {
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

func (p *XaiAccountPool) selectFixed() *shared.XaiAccount {
	activeID := strings.TrimSpace(p.activeAccountID)
	for i := range p.config.Accounts {
		acc := &p.config.Accounts[i]
		acc.ClearCooldownIfExpired()
		if strings.TrimSpace(acc.ID) == activeID && acc.IsEnabled() && !acc.IsCoolingDown() {
			return acc
		}
	}
	for i := range p.config.Accounts {
		acc := &p.config.Accounts[i]
		acc.ClearCooldownIfExpired()
		if acc.IsEnabled() && !acc.IsCoolingDown() {
			return acc
		}
	}
	return nil
}

func (p *XaiAccountPool) selectFailover() *shared.XaiAccount {
	if acc := p.selectFixed(); acc != nil {
		if _, failed := p.failed[strings.TrimSpace(acc.ID)]; !failed {
			return acc
		}
	}
	for i := range p.config.Accounts {
		acc := &p.config.Accounts[i]
		acc.ClearCooldownIfExpired()
		id := strings.TrimSpace(acc.ID)
		if !acc.IsEnabled() || acc.IsCoolingDown() {
			continue
		}
		if _, failed := p.failed[id]; failed {
			continue
		}
		return acc
	}
	return nil
}

func (p *XaiAccountPool) selectLoadBalance() *shared.XaiAccount {
	available := make([]*shared.XaiAccount, 0, len(p.config.Accounts))
	for i := range p.config.Accounts {
		acc := &p.config.Accounts[i]
		acc.ClearCooldownIfExpired()
		id := strings.TrimSpace(acc.ID)
		if !acc.IsEnabled() || acc.IsCoolingDown() {
			continue
		}
		if _, failed := p.failed[id]; failed {
			continue
		}
		available = append(available, acc)
	}
	if len(available) == 0 {
		return nil
	}
	if len(p.wrrCounters) != len(available) {
		p.wrrCounters = make([]int, len(available))
	}
	totalWeight := 0
	selected := 0
	max := -1
	for i, acc := range available {
		w := acc.EffectiveWeight()
		totalWeight += w
		p.wrrCounters[i] += w
		if p.wrrCounters[i] > max {
			max = p.wrrCounters[i]
			selected = i
		}
	}
	if totalWeight <= 0 {
		return available[0]
	}
	p.wrrCounters[selected] -= totalWeight
	return available[selected]
}

func (p *XaiAccountPool) resetWRR() {
	p.wrrCounters = nil
}
