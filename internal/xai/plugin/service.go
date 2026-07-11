package xaiplugin

import (
	"context"
	"sync"
	"sync/atomic"

	"clisimplehub/internal/storage"
	xaiAuth "clisimplehub/internal/xai/auth"
	xaiBackend "clisimplehub/internal/xai/backend"
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

	deviceMu       sync.Mutex
	deviceCancel   context.CancelFunc
	deviceWaitFn   func() (*xaiAuth.LoginResult, error)
	deviceSession  uint64
	deviceCodeInfo *xaiAuth.DeviceCodeResponse
}

func NewXaiService() *XaiService {
	return &XaiService{}
}

func (s *XaiService) SetStorageAccessor(sa StorageAccessor) {
	s.storageAccessor = sa
}

// ensureXaiEndpoints 有账号时在 config.json endpoints 注册 openai/xai 转换器（对齐 codex）。
// 端点 Models / Routes 支持 oauth-model-alias 与按模型路由（Routes 白名单即“排除”未列出模型）。
func (s *XaiService) ensureXaiEndpoints() {
	if s == nil || s.storageAccessor == nil {
		return
	}
	st := s.storageAccessor.GetStorage()
	if st == nil {
		return
	}
	endpoints, err := st.GetEndpoints()
	if err != nil {
		return
	}

	// defaultModels := defaultXaiEndpointModels()
	defaultModels := []storage.ModelMapping{
		{Name: "grok-4.5", Alias: "claude-opus-4-8"},
		{Name: "grok-4.5", Alias: "claude-sonnet-5"},
	}
	defaultRoutes := []string{"claude-opus-4-8", "claude-sonnet-5"}

	type want struct {
		interfaceType string
		name          string
		priority      int
	}
	wants := []want{
		{interfaceType: "codex", name: "xAI Provider", priority: 9},
		{interfaceType: "chat", name: "xAI Chat Provider", priority: 9},
		{interfaceType: "claude", name: "xAI Claude Provider", priority: 9},
	}
	changed := false
	for _, w := range wants {
		if hasXaiEndpoint(endpoints, w.interfaceType) {
			continue
		}
		ep := &storage.Endpoint{
			Name:          w.name,
			APIURL:        "http://127.0.0.1:5600/xai/v1",
			APIKey:        "-",
			Active:        false,
			Enabled:       true,
			InterfaceType: w.interfaceType,
			Transformer:   "openai/xai",
			Priority:      w.priority,
			Models:        append([]storage.ModelMapping(nil), defaultModels...),
			Routes:        append([]string(nil), defaultRoutes...),
		}
		sameTypeCount := 0
		for _, existing := range endpoints {
			if existing != nil && existing.InterfaceType == w.interfaceType {
				sameTypeCount++
			}
		}
		if sameTypeCount == 0 {
			ep.Active = true
		}
		if err := st.SaveEndpoint(ep); err != nil {
			return
		}
		endpoints = append(endpoints, ep)
		changed = true
	}
	if changed {
		s.storageAccessor.TriggerReload()
	}
}

func hasXaiEndpoint(endpoints []*storage.Endpoint, interfaceType string) bool {
	for _, ep := range endpoints {
		if ep != nil && ep.Transformer == "openai/xai" && ep.InterfaceType == interfaceType {
			return true
		}
	}
	return false
}

func defaultXaiEndpointModels() []storage.ModelMapping {
	ids := xaiBackend.StaticModelIDs()
	out := make([]storage.ModelMapping, 0, len(ids))
	for _, id := range ids {
		out = append(out, storage.ModelMapping{Name: id, Alias: id})
	}
	return out
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

func (s *XaiService) storeDeviceSession(info *xaiAuth.DeviceCodeResponse, waitFn func() (*xaiAuth.LoginResult, error), cancel context.CancelFunc) uint64 {
	s.deviceMu.Lock()
	defer s.deviceMu.Unlock()
	if s.deviceCancel != nil {
		s.deviceCancel()
	}
	id := atomic.AddUint64(&s.deviceSession, 1)
	s.deviceCodeInfo = info
	s.deviceWaitFn = waitFn
	s.deviceCancel = cancel
	return id
}

func (s *XaiService) popDeviceSession() (info *xaiAuth.DeviceCodeResponse, waitFn func() (*xaiAuth.LoginResult, error), cancel context.CancelFunc, sessionID uint64) {
	s.deviceMu.Lock()
	defer s.deviceMu.Unlock()
	info = s.deviceCodeInfo
	waitFn = s.deviceWaitFn
	cancel = s.deviceCancel
	sessionID = s.deviceSession
	s.deviceCodeInfo = nil
	s.deviceWaitFn = nil
	s.deviceCancel = nil
	return info, waitFn, cancel, sessionID
}

func (s *XaiService) cancelDeviceSession() {
	s.deviceMu.Lock()
	cancel := s.deviceCancel
	s.deviceCodeInfo = nil
	s.deviceWaitFn = nil
	s.deviceCancel = nil
	s.deviceMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
