package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
)

type XaiDesktopProvider interface {
	DefaultMultiConfigBasename() string
	GetXaiGlobalConfig(configPath string) (json.RawMessage, error)
	SaveXaiGlobalConfig(configPath string, dto json.RawMessage) error
	GetAccounts(configPath string) (json.RawMessage, error)
	GetAccountsPage(configPath string, offset, limit int) (json.RawMessage, error)
	GetActiveAccount(configPath string) (json.RawMessage, error)
	SetActiveAccount(configPath, accountID string) error
	AddAccount(configPath string, dto json.RawMessage) (json.RawMessage, error)
	UpdateAccount(configPath string, dto json.RawMessage) error
	DeleteAccount(configPath, accountID string) error
	DeleteAccounts(configPath string, accountIDs []string) error
	StartLoginWithURL(ctx context.Context, proxyURL string) (authURL string, err error)
	SubmitLoginCallbackURL(ctx context.Context, callbackURL string) error
	WaitForLoginCallback(ctx context.Context) (json.RawMessage, error)
	CancelLogin() error
	StartDeviceLogin(ctx context.Context, proxyURL string) (json.RawMessage, error)
	WaitForDeviceLogin(ctx context.Context) (json.RawMessage, error)
	TestAccount(configPath, accountID string) (json.RawMessage, error)
	// ProbeAccountStream 对该账号发一次 responses SSE 探测
	ProbeAccountStream(ctx context.Context, configPath, accountID string) (json.RawMessage, error)
	RefreshAccountToken(ctx context.Context, configPath, accountID string) (json.RawMessage, error)
	// RefreshAccountQuota 拉取 grok.com rate-limits，更新 pool + 额度。
	RefreshAccountQuota(ctx context.Context, configPath, accountID string) (json.RawMessage, error)
}

// XaiResponsesWebsocketProvider /v1/responses WebSocket 统一入口用。
type XaiResponsesWebsocketProvider interface {
	HandleResponsesWebsocket(w http.ResponseWriter, r *http.Request)
}

func GetXaiDesktopProvider() XaiDesktopProvider {
	p := ByName("xai-accounts")
	if p == nil {
		return nil
	}
	if xp, ok := p.(XaiDesktopProvider); ok {
		return xp
	}
	return nil
}

var (
	xaiDesktopProviderOnce sync.Once
	xaiDesktopProviderInst XaiDesktopProvider
)

func GetXaiDesktopProviderCached() XaiDesktopProvider {
	xaiDesktopProviderOnce.Do(func() {
		xaiDesktopProviderInst = GetXaiDesktopProvider()
	})
	return xaiDesktopProviderInst
}
