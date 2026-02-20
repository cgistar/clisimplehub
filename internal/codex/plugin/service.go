package codexplugin

import (
	"context"
	"sync"

	codex "clisimplehub/internal/codex"
	codexAuth "clisimplehub/internal/codex/auth"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/storage"
)

type StorageAccessor interface {
	GetStorage() storage.Storage
	TriggerReload()
}

type CodexService struct {
	authManagers    map[string]*codexAuth.CodexAuthManager
	storageAccessor StorageAccessor
	store           codexShared.CodexAccountStore
	mu              sync.RWMutex
}

func NewCodexService() *CodexService {
	return &CodexService{
		authManagers: make(map[string]*codexAuth.CodexAuthManager),
	}
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
			AccountID: accountId,
			ProxyUrl:  proxyURL,
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

	for _, ep := range endpoints {
		if ep.Transformer == "openai/codex" && ep.InterfaceType == "codex" {
			return
		}
	}

	newEndpoint := &storage.Endpoint{
		Name:          "Codex Provider",
		APIURL:        "https://chatgpt.com/backend-api/codex",
		APIKey:        "-",
		Active:        false,
		Enabled:       true,
		InterfaceType: "codex",
		Transformer:   "openai/codex",
		Priority:      9,
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
