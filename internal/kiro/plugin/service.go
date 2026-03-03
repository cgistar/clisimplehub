package kiroplugin

import (
	"sync"

	amq "clisimplehub/internal/kiro/amq"
	kiro_claude "clisimplehub/internal/kiro/claude"
)

// KiroService is the central Kiro service used by the plugin.
type KiroService struct {
	transformer     *kiro_claude.Transformer
	storageAccessor StorageAccessor
	mu              sync.RWMutex
	amqClient       *amq.AMQHTTPClient
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
	s.amqClient = nil
	return s.transformer.Reload()
}

// Transformer returns the underlying kiro/claude transformer.
func (s *KiroService) Transformer() *kiro_claude.Transformer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.transformer
}

func (s *KiroService) getOrCreateAMQClient(tr *kiro_claude.Transformer) *amq.AMQHTTPClient {
	s.mu.RLock()
	c := s.amqClient
	s.mu.RUnlock()
	if c != nil {
		return c
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.amqClient != nil {
		return s.amqClient
	}
	s.amqClient = amq.NewHTTPClient(amq.AMQClientConfig{
		TokenProvider: &amqTokenProvider{tr: tr},
		Runtime:       &amqRuntimeProvider{tr: tr},
	})
	return s.amqClient
}
