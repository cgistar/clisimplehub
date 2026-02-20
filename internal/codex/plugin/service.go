package codexplugin

import (
	"sync"

	codex "clisimplehub/internal/codex"
	codexAuth "clisimplehub/internal/codex/auth"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/storage"
)

// StorageAccessor provides access to storage and reload functionality.
type StorageAccessor interface {
	GetStorage() storage.Storage
	TriggerReload()
}

type CodexService struct {
	authManagers    map[string]*codexAuth.CodexAuthManager
	storageAccessor StorageAccessor
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

func (s *CodexService) GetOrCreateAuthManager(accountId, configPath, proxyURL string) *codexAuth.CodexAuthManager {
	// Fast path: check if manager exists with read lock
	s.mu.RLock()
	if m, ok := s.authManagers[accountId]; ok {
		s.mu.RUnlock()
		m.SetProxyURL(proxyURL)
		return m
	}
	s.mu.RUnlock()

	// Slow path: create new manager with write lock
	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock
	if m, ok := s.authManagers[accountId]; ok {
		m.SetProxyURL(proxyURL)
		return m
	}

	// Load account from config to get full details
	cfg, err := codexShared.LoadCodexMultiConfig(configPath)
	if err != nil {
		// Fallback: create minimal manager
		m := codexAuth.NewCodexAuthManager(&codexShared.CodexAccount{
			AccountID: accountId,
			ProxyUrl:  proxyURL,
		}, configPath)
		s.authManagers[accountId] = m
		return m
	}

	account := cfg.FindAccountByAccountID(accountId)
	if account == nil {
		// Fallback: create minimal manager
		m := codexAuth.NewCodexAuthManager(&codexShared.CodexAccount{
			AccountID: accountId,
			ProxyUrl:  proxyURL,
		}, configPath)
		s.authManagers[accountId] = m
		return m
	}

	m := codexAuth.NewCodexAuthManager(account, configPath)
	m.SetProxyURL(proxyURL)
	s.authManagers[accountId] = m
	return m
}

func (s *CodexService) RemoveAuthManager(accountId string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.authManagers, accountId)
}

// SaveConfigAndEnsureEndpoint saves the config and ensures endpoint exists.
// This is a wrapper around SaveCodexMultiConfig that also triggers endpoint creation.
func (s *CodexService) SaveConfigAndEnsureEndpoint(configPath string, mc *codexShared.CodexMultiConfig) error {
	if err := codexShared.SaveCodexMultiConfig(configPath, mc); err != nil {
		return err
	}

	// Reload pool
	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}

	// Ensure endpoint exists if there are accounts
	if len(mc.Accounts) > 0 {
		s.ensureCodexEndpoint()
	}

	return nil
}

// ensureCodexEndpoint ensures a openai/codex endpoint exists.
func (s *CodexService) ensureCodexEndpoint() {
	s.mu.RLock()
	sa := s.storageAccessor
	s.mu.RUnlock()
	if sa == nil {
		return
	}
	store := sa.GetStorage()
	if store == nil {
		return
	}

	endpoints, err := store.GetEndpoints()
	if err != nil {
		return
	}

	// Check if a codex endpoint with openai/codex transformer already exists
	for _, ep := range endpoints {
		if ep.Transformer == "openai/codex" && ep.InterfaceType == "codex" {
			return
		}
	}

	// Create new endpoint
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

	// If no other codex endpoints exist, make this one active
	sameTypeCount := 0
	for _, ep := range endpoints {
		if ep.InterfaceType == "codex" {
			sameTypeCount++
		}
	}
	if sameTypeCount == 0 {
		newEndpoint.Active = true
	}

	if err := store.SaveEndpoint(newEndpoint); err != nil {
		// Silent failure - don't block account creation
		return
	}

	sa.TriggerReload()
}
