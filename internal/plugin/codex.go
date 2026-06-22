package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
)

type CodexDesktopProvider interface {
	DefaultMultiConfigBasename() string
	GetCodexGlobalConfig(configPath string) (json.RawMessage, error)
	SaveCodexGlobalConfig(configPath string, dto json.RawMessage) error
	GetAccounts(configPath string) (json.RawMessage, error)
	GetAccountsPage(configPath string, offset, limit int) (json.RawMessage, error)
	GetActiveAccount(configPath string) (json.RawMessage, error)
	SetActiveAccount(configPath, refreshToken string) error
	AddAccount(configPath string, dto json.RawMessage) (json.RawMessage, error)
	UpdateAccount(configPath string, dto json.RawMessage) error
	RestoreAccount(configPath, accountId string) error
	DeleteAccount(configPath, refreshToken string) error
	DeleteAccounts(configPath string, accountIDs []string) error
	StartLogin(ctx context.Context, proxyURL string) (json.RawMessage, error)
	StartLoginWithURL(ctx context.Context, proxyURL string) (authURL string, err error)
	SubmitLoginCallbackURL(ctx context.Context, callbackURL string) error
	WaitForLoginCallback(ctx context.Context) (json.RawMessage, error)
	CancelLogin() error
	TestAccount(configPath, refreshToken string) (json.RawMessage, error)
	GetAccountUsage(ctx context.Context, configPath, accountId string) (json.RawMessage, error)
	GetAccountPrimaryUsage(ctx context.Context, configPath, accountId string) (json.RawMessage, error)
	ConsumeAccountResetCredit(ctx context.Context, configPath, accountId string) (json.RawMessage, error)
	GetCodexAccountStats(ctx context.Context, timeRange string) (json.RawMessage, error)
	StartHeadlessLogin(ctx context.Context, email, password, clientID, proxyURL string, onStep func(string)) (json.RawMessage, error)
	StartHeadlessLoginWithProvider(ctx context.Context, req json.RawMessage, onStep func(string)) (json.RawMessage, error)
	SubmitHeadlessOTP(ctx context.Context, code string) (json.RawMessage, error)
	CancelHeadlessLogin() error
	StartSignup(ctx context.Context, req json.RawMessage, onStep func(string)) (json.RawMessage, error)
	SubmitSignupOTP(ctx context.Context, code string) (json.RawMessage, error)
	CancelSignup() error
	GetEmailProviders() (json.RawMessage, error)
	GenerateRandomEmail(provider string, params json.RawMessage) (json.RawMessage, error)
	FetchVerificationCode(ctx context.Context, req json.RawMessage) (json.RawMessage, error)
}

type CodexResponsesWebsocketProvider interface {
	HandleResponsesWebsocket(w http.ResponseWriter, r *http.Request)
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
