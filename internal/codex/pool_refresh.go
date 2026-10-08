package codex

import (
	"context"
	"fmt"

	"clisimplehub/internal/codex/shared"
)

// RefreshAccounts 只同步账号资料；切换时先保存配置，所有可失败步骤完成后再发布运行态。
func (p *CodexAccountPool) RefreshAccounts(ctx context.Context, configPath, activeAccountID string) ([]shared.CodexAccount, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.store == nil {
		return nil, fmt.Errorf("account store not initialized")
	}
	accounts, err := p.store.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	normalizeAccountIDs(accounts)
	config := p.config
	if activeAccountID != "" {
		found := false
		for i := range accounts {
			if accounts[i].ID == activeAccountID {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("account not found: %s", activeAccountID)
		}
		config, err = shared.LoadCodexMultiConfig(configPath)
		if err != nil {
			return nil, err
		}
		config.ActiveAccountID = activeAccountID
		if err := shared.SaveCodexMultiConfig(configPath, config); err != nil {
			return nil, err
		}
	}

	previousAvailable := p.availableAccounts(nil)
	previousByID := make(map[string]*shared.CodexAccount, len(p.accounts))
	for i := range p.accounts {
		previousByID[p.accounts[i].ID] = &p.accounts[i]
	}
	next := make([]shared.CodexAccount, len(accounts))
	present := make(map[string]bool, len(accounts))
	for i := range accounts {
		next[i] = *cloneAccount(&accounts[i])
		account := &next[i]
		present[account.ID] = true
		if old := previousByID[account.ID]; old != nil {
			// 保留尚未落库的运行态健康与用量更新，避免列表刷新使账号提前恢复。
			if old.CooldownUntil.After(account.CooldownUntil) {
				account.CooldownUntil = old.CooldownUntil
				account.CooldownReason = old.CooldownReason
			}
			if old.CodexUsage != nil && (account.CodexUsage == nil || old.CodexUsage.UpdatedAt.After(account.CodexUsage.UpdatedAt)) {
				usage := *old.CodexUsage
				account.CodexUsage = &usage
			}
		}
	}
	SortAccounts(next)
	for id := range p.failed {
		if !present[id] {
			delete(p.failed, id)
		}
	}
	p.accounts = next
	p.config = config
	if activeAccountID != "" {
		p.activeAccountID = activeAccountID
	}
	nextAvailable := p.availableAccounts(nil)
	if !sameWeightedAccounts(previousAvailable, nextAvailable) {
		p.resetWRR()
	}
	// 返回数据库原顺序，供列表和第一页使用同一快照；不暴露池内可变数据。
	return accounts, nil
}

func sameWeightedAccounts(left, right []*shared.CodexAccount) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].ID != right[i].ID || left[i].EffectiveWeight() != right[i].EffectiveWeight() {
			return false
		}
	}
	return true
}
