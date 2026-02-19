package codexplugin

import (
	"sync"

	codexAuth "clisimplehub/internal/codex/auth"
	codexShared "clisimplehub/internal/codex/shared"
)

type CodexService struct {
	authManagers map[string]*codexAuth.CodexAuthManager
	mu           sync.RWMutex
}

func NewCodexService() *CodexService {
	return &CodexService{
		authManagers: make(map[string]*codexAuth.CodexAuthManager),
	}
}

func (s *CodexService) GetOrCreateAuthManager(refreshToken, configPath, proxyURL string) *codexAuth.CodexAuthManager {
	s.mu.Lock()
	defer s.mu.Unlock()

	if m, ok := s.authManagers[refreshToken]; ok {
		m.SetProxyURL(proxyURL)
		return m
	}

	m := codexAuth.NewCodexAuthManager(&codexShared.CodexAccount{
		RefreshToken: refreshToken,
		ProxyUrl:     proxyURL,
	}, configPath)
	s.authManagers[refreshToken] = m
	return m
}

func (s *CodexService) RemoveAuthManager(refreshToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.authManagers, refreshToken)
}
