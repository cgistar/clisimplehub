package kiroplugin

import (
	"sync"

	kiro_claude "clisimplehub/internal/kiro/claude"
)

// KiroService is the central Kiro service used by the plugin.
type KiroService struct {
	transformer     *kiro_claude.Transformer
	storageAccessor StorageAccessor
	mu              sync.RWMutex
}

// NewKiroService creates a new KiroService.
func NewKiroService() *KiroService {
	return &KiroService{
		transformer: kiro_claude.NewTransformer(),
	}
}

// Reload resets and reloads the service state.
func (s *KiroService) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transformer.Reload()
}

// Transformer returns the underlying kiro/claude transformer.
func (s *KiroService) Transformer() *kiro_claude.Transformer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.transformer
}
