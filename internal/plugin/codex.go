package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"clisimplehub/internal/storage"
)

type CodexDesktopProvider interface {
	DefaultMultiConfigBasename() string
	GetCodexGlobalConfig(configPath string) (json.RawMessage, error)
	SaveCodexGlobalConfig(configPath string, dto json.RawMessage) error
	GetCodexModelPrices() (json.RawMessage, error)
	SaveCodexModelPrices(dto json.RawMessage) (json.RawMessage, error)
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
	HandleResponsesWebsocket(w http.ResponseWriter, r *http.Request, endpoint *storage.Endpoint)
}

// CodexTokenUsage 使用与 executor.TokenUsage 相同的字段，但避免 plugin 包反向依赖 executor。
type CodexTokenUsage struct {
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	CachedCreate int64
	CachedRead   int64
	Reasoning    int64
}

type CodexAdditionalModelUsage struct {
	Model  string
	Tokens CodexTokenUsage
}

// CodexUsageRecord 描述一次上游账号执行；故障转移的每次账号尝试各发布一条。
type CodexUsageRecord struct {
	Provider            string
	ExecutorType        string
	AccountID           string
	UpstreamAccountID   string
	AccountEmail        string
	AuthType            string
	PlanType            string
	Source              string
	Model               string
	RequestedModel      string
	Path                string
	RequestedAt         time.Time
	Duration            time.Duration
	TTFT                time.Duration
	StatusCode          int
	Status              string
	Error               string
	ReasoningEffort     string
	ServiceTier         string
	ResponseServiceTier string
	Tokens              CodexTokenUsage
	AdditionalModels    []CodexAdditionalModelUsage
}

type CodexUsageObserver func(CodexUsageRecord)

type codexUsageObserverContextKey struct{}

func WithCodexUsageObserver(ctx context.Context, observer CodexUsageObserver) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, codexUsageObserverContextKey{}, observer)
}

func CodexUsageObserverFromContext(ctx context.Context) CodexUsageObserver {
	if ctx == nil {
		return nil
	}
	observer, _ := ctx.Value(codexUsageObserverContextKey{}).(CodexUsageObserver)
	return observer
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
