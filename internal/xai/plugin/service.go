package xaiplugin

import (
	"sync"
	"sync/atomic"

	xaiAuth "clisimplehub/internal/xai/auth"
	"clisimplehub/internal/storage"
)

type StorageAccessor interface {
	GetStorage() storage.Storage
	TriggerReload()
}

type XaiService struct {
	storageAccessor StorageAccessor

	loginMu      sync.Mutex
	loginWaitFn  func() (*xaiAuth.LoginResult, error)
	loginCleanup func()
	loginID      uint64
}

func NewXaiService() *XaiService {
	return &XaiService{}
}

func (s *XaiService) SetStorageAccessor(sa StorageAccessor) {
	s.storageAccessor = sa
}

func (s *XaiService) storeLoginSession(waitFn func() (*xaiAuth.LoginResult, error), cleanupFn func()) uint64 {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if s.loginCleanup != nil {
		s.loginCleanup()
	}
	id := atomic.AddUint64(&s.loginID, 1)
	s.loginWaitFn = waitFn
	s.loginCleanup = cleanupFn
	return id
}

func (s *XaiService) popLoginSession() (waitFn func() (*xaiAuth.LoginResult, error), cleanupFn func(), sessionID uint64) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	waitFn = s.loginWaitFn
	cleanupFn = s.loginCleanup
	sessionID = s.loginID
	s.loginWaitFn = nil
	s.loginCleanup = nil
	return waitFn, cleanupFn, sessionID
}

func (s *XaiService) clearLoginSession(sessionID uint64) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if s.loginID == sessionID {
		s.loginWaitFn = nil
		s.loginCleanup = nil
	}
}

func (s *XaiService) cancelLoginSession() {
	s.loginMu.Lock()
	cleanup := s.loginCleanup
	s.loginWaitFn = nil
	s.loginCleanup = nil
	s.loginMu.Unlock()
	if cleanup != nil {
		cleanup()
	}
}
