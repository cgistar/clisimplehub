package auth

import (
	"context"
	"fmt"
	"sort"
	"time"

	"clisimplehub/internal/codex/shared"
)

// RefreshManagers 要求调用方独占 managers；先锁凭据再读取快照，避免覆盖刚刷新的 token。
// refresh 不得再调用 manager 方法；失败时必须保留原运行态。
func RefreshManagers(managers map[string]*CodexAuthManager, refresh func() ([]shared.CodexAccount, error)) ([]shared.CodexAccount, error) {
	ids := make([]string, 0, len(managers))
	for id := range managers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		m := managers[id]
		m.mu.Lock()
		defer m.mu.Unlock()
	}

	accounts, err := refresh()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*shared.CodexAccount, len(accounts))
	for i := range accounts {
		byID[accounts[i].ID] = &accounts[i]
	}
	for _, id := range ids {
		m := managers[id]
		account := byID[id]
		m.syncAccountLocked(account)
		if account == nil {
			delete(managers, id)
		}
	}
	return accounts, nil
}

func (m *CodexAuthManager) ReloadFromStore(ctx context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store == nil {
		return false, fmt.Errorf("account store not initialized")
	}
	if m.invalidated {
		return false, nil
	}
	account, err := m.store.GetByID(ctx, m.localID)
	if err != nil {
		return false, err
	}
	m.syncAccountLocked(account)
	return account != nil, nil
}

func (m *CodexAuthManager) Invalidate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncAccountLocked(nil)
}

func (m *CodexAuthManager) syncAccountLocked(account *shared.CodexAccount) {
	if account == nil {
		m.invalidated = true
		m.accessToken, m.refreshToken, m.idToken = "", "", ""
		m.accountID, m.email, m.planType = "", "", ""
		m.expiresAt = time.Time{}
		return
	}
	// 代理地址由请求结合应用及账号配置解析，资料同步不覆盖已解析的地址。
	m.accessToken = account.AccessToken
	m.refreshToken = account.RefreshToken
	m.idToken = account.IDToken
	m.accountID = account.AccountID
	m.email = account.Email
	m.planType = account.PlanType
	m.expiresAt = account.ExpiresAt
}
