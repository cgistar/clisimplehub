package plugin

import (
	"context"
	"encoding/json"
	"sync"
)

type CodexDesktopProvider interface {
	DefaultMultiConfigBasename() string
	GetCodexGlobalConfig(configPath string) (json.RawMessage, error)
	SaveCodexGlobalConfig(configPath string, dto json.RawMessage) error
	GetAccounts(configPath string) (json.RawMessage, error)
	GetActiveAccount(configPath string) (json.RawMessage, error)
	SetActiveAccount(configPath, refreshToken string) error
	AddAccount(configPath string, dto json.RawMessage) (json.RawMessage, error)
	UpdateAccount(configPath string, dto json.RawMessage) error
	DeleteAccount(configPath, refreshToken string) error
	StartLogin(ctx context.Context, proxyURL string) (json.RawMessage, error)
	StartLoginWithURL(ctx context.Context, proxyURL string) (authURL string, err error)
	WaitForLoginCallback(ctx context.Context) (json.RawMessage, error)
	TestAccount(configPath, refreshToken string) (json.RawMessage, error)
}

func GetCodexDesktopProvider() CodexDesktopProvider {
	p := ByName("codex-accounts")
	if p == nil {
		return nil
	}
	if cp, ok := p.(CodexDesktopProvider); ok {
		return cp
	}
	return nil
}

var (
	codexDesktopProviderOnce sync.Once
	codexDesktopProviderInst CodexDesktopProvider
)

func GetCodexDesktopProviderCached() CodexDesktopProvider {
	codexDesktopProviderOnce.Do(func() {
		codexDesktopProviderInst = GetCodexDesktopProvider()
	})
	return codexDesktopProviderInst
}
