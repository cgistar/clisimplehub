package plugin

import (
	"context"
	"encoding/json"
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
	TestAccount(configPath, accountID string) (json.RawMessage, error)
	RefreshAccountToken(ctx context.Context, configPath, accountID string) (json.RawMessage, error)
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
