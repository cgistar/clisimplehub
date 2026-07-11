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
	// rrCursor：loadbalance
	rrCursor int
	failed   map[string]shared.XaiAccountStatus
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
	pool.resetRR()
	// 迁移：账号级 baseURL/tokenEndpoint/redirectURI 已废弃，启动时写回以剔除旧字段
	_ = shared.SaveXaiMultiConfig(xaiJsonPath, pool.config)
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

func (p *XaiAccountPool) ProxyURL() string {
	if p == nil {
		return ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.config == nil {
		return ""
	}
	return strings.TrimSpace(p.config.ProxyUrl)
}

func (p *XaiAccountPool) Mode() string {
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

func (p *XaiAccountPool) MarkFailed(accountID string, status shared.XaiAccountStatus, cooldown time.Duration, reason string) {
	if p == nil {
		return
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.config == nil {
		return
	}
	if p.failed == nil {
		p.failed = make(map[string]shared.XaiAccountStatus)
	}
	if status == "" {
		status = shared.XaiStatusUnknown
	}
	p.failed[accountID] = status
	for i := range p.config.Accounts {
		if strings.TrimSpace(p.config.Accounts[i].ID) != accountID {
			continue
		}
		p.config.Accounts[i].Status = status
		p.config.Accounts[i].CooldownReason = strings.TrimSpace(reason)
		if cooldown > 0 {
			p.config.Accounts[i].CooldownUntil = time.Now().Add(cooldown)
		}
		break
	}
	_ = shared.SaveXaiMultiConfig(p.configPath, p.config)
}

func (p *XaiAccountPool) ReportSuccess(accountID string) {
	if p == nil {
		return
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.failed, accountID)
	for i := range p.config.Accounts {
		if strings.TrimSpace(p.config.Accounts[i].ID) != accountID {
			continue
		}
		if p.config.Accounts[i].Status != shared.XaiStatusValid {
			p.config.Accounts[i].Status = shared.XaiStatusValid
			_ = shared.SaveXaiMultiConfig(p.configPath, p.config)
		}
		break
	}
}

func (p *XaiAccountPool) SelectExcluding(excluded map[string]bool) *shared.XaiAccount {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.config == nil {
		return nil
	}
	for i := range p.config.Accounts {
		acc := &p.config.Accounts[i]
		acc.ClearCooldownIfExpired()
		id := strings.TrimSpace(acc.ID)
		if id == "" || !acc.IsEnabled() || acc.IsCoolingDown() {
			continue
		}
		if excluded[id] {
			continue
		}
		if _, failed := p.failed[id]; failed {
			continue
		}
		return acc
	}
	return nil
}

func (p *XaiAccountPool) SelectWebsocket() *shared.XaiAccount {
	return p.SelectWebsocketStrict()
}

// SelectWebsocketStrict 仅选择 websockets=true 且可用的账号（无回退）。
func (p *XaiAccountPool) SelectWebsocketStrict() *shared.XaiAccount {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.config == nil {
		return nil
	}
	for i := range p.config.Accounts {
		acc := &p.config.Accounts[i]
		if !acc.WebsocketsEnabled() || !accountSelectable(acc, p.failed) {
			continue
		}
		return acc
	}
	return nil
}

func (p *XaiAccountPool) UpdateTokens(accountID string, accessToken, refreshToken, idToken string, expiresAt time.Time) error {
	if p == nil {
		return fmt.Errorf("pool is nil")
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return fmt.Errorf("accountId is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.config.Accounts {
		if strings.TrimSpace(p.config.Accounts[i].ID) != accountID {
			continue
		}
		if accessToken != "" {
			p.config.Accounts[i].AccessToken = accessToken
		}
		if refreshToken != "" {
			p.config.Accounts[i].RefreshToken = refreshToken
		}
		if idToken != "" {
			p.config.Accounts[i].IDToken = idToken
		}
		if !expiresAt.IsZero() {
			p.config.Accounts[i].ExpiresAt = expiresAt
		}
		p.config.Accounts[i].LastRefresh = time.Now().UTC()
		p.config.Accounts[i].Status = shared.XaiStatusValid
		p.config.Accounts[i].UpdatedAt = time.Now()
		return shared.SaveXaiMultiConfig(p.configPath, p.config)
	}
	return fmt.Errorf("account not found: %s", accountID)
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
	deletedIDs := make([]string, 0, len(targets))
	for i := range p.config.Accounts {
		id := strings.TrimSpace(p.config.Accounts[i].ID)
		if _, ok := targets[id]; ok {
			deleted++
			deletedIDs = append(deletedIDs, id)
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
	// 锁外回调关 WS（由 plugin 注册，避免包循环）
	if onAccountsDeleted != nil {
		ids := append([]string(nil), deletedIDs...)
		go onAccountsDeleted(ids)
	}
	return deleted, nil
}

// OnAccountsDeleted 注册账号删除后的钩子（如关闭 WebSocket）。
var onAccountsDeleted func(accountIDs []string)

func SetOnAccountsDeleted(fn func(accountIDs []string)) {
	onAccountsDeleted = fn
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
	// 保留未在本次写入中提供的字段（如 customHeaders）
	existing := p.config.Config
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = shared.DefaultXaiConfig().BaseURL
	}
	existing.BaseURL = baseURL
	existing.ClientVersion = strings.TrimSpace(cfg.ClientVersion)
	existing.UserAgent = strings.TrimSpace(cfg.UserAgent)
	existing.TokenAuth = strings.TrimSpace(cfg.TokenAuth)
	existing.ClientSurface = strings.TrimSpace(cfg.ClientSurface)
	if cfg.DynamicStatsig != nil {
		existing.DynamicStatsig = cfg.DynamicStatsig
	}
	if cfg.CustomHeaders != nil {
		existing.CustomHeaders = cfg.CustomHeaders
	}
	p.config.Config = existing
	p.resetRR()
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
	p.resetRR()
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
		// failover
		return p.selectFillFirst()
	case shared.RotationLoadBalance:
		// loadbalance
		return p.selectRoundRobin()
	default:
		return p.selectFixed()
	}
}

func accountSelectable(acc *shared.XaiAccount, failed map[string]shared.XaiAccountStatus) bool {
	if acc == nil {
		return false
	}
	acc.ClearCooldownIfExpired()
	if !acc.IsEnabled() || acc.IsCoolingDown() {
		return false
	}
	switch acc.Status {
	case shared.XaiStatusBanned, shared.XaiStatusExhausted:
		return false
	}
	id := strings.TrimSpace(acc.ID)
	if id == "" {
		return false
	}
	if failed != nil {
		if _, ok := failed[id]; ok {
			return false
		}
	}
	return true
}

func (p *XaiAccountPool) collectAvailable() []*shared.XaiAccount {
	available := make([]*shared.XaiAccount, 0, len(p.config.Accounts))
	for i := range p.config.Accounts {
		acc := &p.config.Accounts[i]
		if accountSelectable(acc, p.failed) {
			available = append(available, acc)
		}
	}
	if len(available) > 1 {
		sort.SliceStable(available, func(i, j int) bool {
			return strings.TrimSpace(available[i].ID) < strings.TrimSpace(available[j].ID)
		})
	}
	return available
}

func (p *XaiAccountPool) selectFixed() *shared.XaiAccount {
	activeID := strings.TrimSpace(p.activeAccountID)
	for i := range p.config.Accounts {
		acc := &p.config.Accounts[i]
		if strings.TrimSpace(acc.ID) == activeID && accountSelectable(acc, p.failed) {
			return acc
		}
	}
	// active 不可用时回退第一个可用账号（不切换 active，保持 fixed 语义）
	for i := range p.config.Accounts {
		acc := &p.config.Accounts[i]
		if accountSelectable(acc, p.failed) {
			return acc
		}
	}
	return nil
}

// 始终选择当前可用集合中的第一个（按 ID 排序），把一个账号“用尽/冷却”后再用下一个。
func (p *XaiAccountPool) selectFillFirst() *shared.XaiAccount {
	available := p.collectAvailable()
	if len(available) == 0 {
		return nil
	}
	return available[0]
}

// 在可用账号间简单轮询（等权），不使用加权 WRR。
func (p *XaiAccountPool) selectRoundRobin() *shared.XaiAccount {
	available := p.collectAvailable()
	if len(available) == 0 {
		return nil
	}
	if p.rrCursor < 0 || p.rrCursor >= 2_147_483_640 {
		p.rrCursor = 0
	}
	idx := p.rrCursor % len(available)
	p.rrCursor++
	return available[idx]
}

// 兼容旧调用名
func (p *XaiAccountPool) selectFailover() *shared.XaiAccount {
	return p.selectFillFirst()
}

func (p *XaiAccountPool) selectLoadBalance() *shared.XaiAccount {
	return p.selectRoundRobin()
}

func (p *XaiAccountPool) resetRR() {
	p.rrCursor = 0
}

// Deprecated: use resetRR
func (p *XaiAccountPool) resetWRR() {
	p.resetRR()
}
