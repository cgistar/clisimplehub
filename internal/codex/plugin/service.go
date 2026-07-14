package codexplugin

import (
	"context"
	"sync"
	"time"

	codex "clisimplehub/internal/codex"
	codexAuth "clisimplehub/internal/codex/auth"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/logger"
	"clisimplehub/internal/storage"
)

const codexUsageRefreshMinInterval = 5 * time.Second

type codexUsageFetcher func(context.Context, string, string, string, *codexShared.CodexMultiConfig) (*codexShared.CodexUsageSnapshot, string, error)

type StorageAccessor interface {
	GetStorage() storage.Storage
	TriggerReload()
}

type CodexService struct {
	authManagers    map[string]*codexAuth.CodexAuthManager
	websocketExec   *CodexWebsocketsExecutor
	storageAccessor StorageAccessor
	store           codexShared.CodexAccountStore
	ctx             context.Context
	cancel          context.CancelFunc
	usageFetcher    codexUsageFetcher
	usageRefreshing map[string]struct{}
	usageRefreshed  map[string]time.Time
	mu              sync.RWMutex
}

func NewCodexService() *CodexService {
	ctx, cancel := context.WithCancel(context.Background())
	service := &CodexService{
		authManagers:    make(map[string]*codexAuth.CodexAuthManager),
		ctx:             ctx,
		cancel:          cancel,
		usageRefreshing: make(map[string]struct{}),
		usageRefreshed:  make(map[string]time.Time),
	}
	service.websocketExec = NewCodexWebsocketsExecutor(service)
	return service
}

func (s *CodexService) Close() {
	if s == nil {
		return
	}
	s.mu.RLock()
	exec := s.websocketExec
	cancel := s.cancel
	s.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	if exec != nil {
		exec.Close()
	}
}

func (s *CodexService) enableUsageRefresh() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.usageFetcher = fetchCodexUsage
	s.mu.Unlock()
}

func (s *CodexService) scheduleUsageSnapshotRefresh(account *codexShared.CodexAccount, accessToken, upstreamAccountID, proxyURL string, config *codexShared.CodexMultiConfig) {
	if s == nil || account == nil {
		return
	}
	accountID := account.ID
	if accountID == "" || accessToken == "" || upstreamAccountID == "" {
		return
	}

	now := time.Now()
	s.mu.Lock()
	fetcher := s.usageFetcher
	if fetcher == nil || s.ctx == nil {
		s.mu.Unlock()
		return
	}
	if _, ok := s.usageRefreshing[accountID]; ok || now.Sub(s.usageRefreshed[accountID]) < codexUsageRefreshMinInterval {
		s.mu.Unlock()
		return
	}
	s.usageRefreshing[accountID] = struct{}{}
	s.usageRefreshed[accountID] = now
	ctx := s.ctx
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.usageRefreshing, accountID)
			s.mu.Unlock()
		}()

		refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		snapshot, _, err := fetcher(refreshCtx, accessToken, upstreamAccountID, proxyURL, config)
		if err != nil {
			if refreshCtx.Err() == nil {
				logger.Warn("[Codex] refresh websocket usage for account %s failed: %v", accountID, err)
			}
			return
		}
		if snapshot != nil {
			if pool := codex.GetPool(); pool != nil {
				pool.UpdateUsageSnapshot(accountID, snapshot)
			}
		}
	}()
}

func (s *CodexService) SetStorageAccessor(sa StorageAccessor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.storageAccessor = sa
}

func (s *CodexService) SetAccountStore(store codexShared.CodexAccountStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store = store
}

func (s *CodexService) getAccountStore() codexShared.CodexAccountStore {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store
}

func (s *CodexService) GetOrCreateAuthManager(accountId, configPath, proxyURL string) *codexAuth.CodexAuthManager {
	s.mu.RLock()
	if m, ok := s.authManagers[accountId]; ok {
		s.mu.RUnlock()
		m.SetProxyURL(proxyURL)
		return m
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	if m, ok := s.authManagers[accountId]; ok {
		m.SetProxyURL(proxyURL)
		return m
	}

	// Load account from store
	var account *codexShared.CodexAccount
	if s.store != nil {
		account, _ = s.store.GetByID(context.Background(), accountId)
	}

	if account == nil {
		m := codexAuth.NewCodexAuthManager(&codexShared.CodexAccount{
			ID:       accountId,
			ProxyUrl: proxyURL,
		}, s.store)
		s.authManagers[accountId] = m
		return m
	}

	m := codexAuth.NewCodexAuthManager(account, s.store)
	m.SetProxyURL(proxyURL)
	s.authManagers[accountId] = m
	return m
}

func (s *CodexService) RemoveAuthManager(accountId string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.authManagers, accountId)
}

func (s *CodexService) ClearAuthManagers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authManagers = make(map[string]*codexAuth.CodexAuthManager)
}

func (s *CodexService) SaveConfigAndReload(configPath string, mc *codexShared.CodexMultiConfig) error {
	if err := codexShared.SaveCodexMultiConfig(configPath, mc); err != nil {
		return err
	}
	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}
	return nil
}

func (s *CodexService) ensureCodexEndpoint() {
	s.mu.RLock()
	sa := s.storageAccessor
	s.mu.RUnlock()
	if sa == nil {
		return
	}
	st := sa.GetStorage()
	if st == nil {
		return
	}

	endpoints, err := st.GetEndpoints()
	if err != nil {
		return
	}

	hasCodexEndpoint := false
	for _, ep := range endpoints {
		if ep.Transformer == "openai/codex" && ep.InterfaceType == "codex" {
			hasCodexEndpoint = true
			break
		}
	}

	if !hasCodexEndpoint {
		newEndpoint := &storage.Endpoint{
			Name:          "Codex Provider",
			APIURL:        "http://127.0.0.1:5600/codex/v1",
			APIKey:        "-",
			Active:        false,
			Enabled:       true,
			InterfaceType: "codex",
			Transformer:   "openai/codex",
			Priority:      8,
		}

		sameTypeCount := 0
		for _, ep := range endpoints {
			if ep.InterfaceType == "codex" {
				sameTypeCount++
			}
		}
		if sameTypeCount == 0 {
			newEndpoint.Active = true
		}

		if err := st.SaveEndpoint(newEndpoint); err != nil {
			return
		}
		sa.TriggerReload()
	}

	s.ensureCodexChatEndpoint()
}

func (s *CodexService) ensureCodexChatEndpoint() {
	s.mu.RLock()
	sa := s.storageAccessor
	s.mu.RUnlock()
	if sa == nil {
		return
	}
	st := sa.GetStorage()
	if st == nil {
		return
	}

	endpoints, err := st.GetEndpoints()
	if err != nil {
		return
	}

	for _, ep := range endpoints {
		if ep.Transformer == "openai/codex" && ep.InterfaceType == "chat" {
			return
		}
	}

	newEndpoint := &storage.Endpoint{
		Name:          "Codex Chat Provider",
		APIURL:        "http://127.0.0.1:5600/codex/v1",
		APIKey:        "-",
		Active:        false,
		Enabled:       true,
		InterfaceType: "chat",
		Transformer:   "openai/codex",
		Priority:      8,
	}

	sameTypeCount := 0
	for _, ep := range endpoints {
		if ep.InterfaceType == "chat" {
			sameTypeCount++
		}
	}
	if sameTypeCount == 0 {
		newEndpoint.Active = true
	}

	if err := st.SaveEndpoint(newEndpoint); err != nil {
		return
	}
	sa.TriggerReload()
}
