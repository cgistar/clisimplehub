package shared

import (
	"context"
	"time"
)

type CodexAccountStat struct {
	AccountID        string
	AccountEmail     string
	Model            string
	Date             string
	Hour             int
	InputTokens      int64
	OutputTokens     int64
	TotalTokens      int64
	StatusCode       int
	Status           string
	ErrorType        string
	DurationMs       int64
	PrimaryUsedPct   *float64
	SecondaryUsedPct *float64
	RequestPath      string
}

type CodexAccountStatsSummary struct {
	AccountID     string  `json:"accountId"`
	AccountEmail  string  `json:"accountEmail"`
	RequestCount  int64   `json:"requestCount"`
	InputTokens   int64   `json:"inputTokens"`
	OutputTokens  int64   `json:"outputTokens"`
	TotalTokens   int64   `json:"totalTokens"`
	ErrorCount    int64   `json:"errorCount"`
	AvgDurationMs float64 `json:"avgDurationMs"`
}

type CodexAccountStore interface {
	ListAccounts(ctx context.Context) ([]CodexAccount, error)
	ListAccountsPage(ctx context.Context, offset, limit int) ([]CodexAccount, error)
	CountAccounts(ctx context.Context) (int, error)
	GetByID(ctx context.Context, accountID string) (*CodexAccount, error)
	GetByRefreshToken(ctx context.Context, rt string) (*CodexAccount, error)
	Insert(ctx context.Context, account *CodexAccount) error
	Update(ctx context.Context, account *CodexAccount) error
	Delete(ctx context.Context, accountID string) error
	DeleteMany(ctx context.Context, accountIDs []string) error

	// Hot-path partial updates
	UpdateTokens(ctx context.Context, accountID, accessToken, idToken, refreshToken string, expiresAt time.Time) error
	UpdateStatus(ctx context.Context, accountID string, status CodexAccountStatus) error
	UpdateCooldown(ctx context.Context, accountID string, until time.Time, reason string) error
	UpdateUsageSnapshot(ctx context.Context, accountID string, snapshot *CodexUsageSnapshot) error

	// Stats
	InsertStat(ctx context.Context, stat *CodexAccountStat) error
	GetStatsSummary(ctx context.Context, accountID string, timeRange string) (*CodexAccountStatsSummary, error)
	GetStatsSummaryMap(ctx context.Context, accountIDs []string, timeRange string) (map[string]CodexAccountStatsSummary, error)
	GetAllStatsSummary(ctx context.Context, timeRange string) ([]CodexAccountStatsSummary, error)
	DeleteStats(ctx context.Context, accountID string) error

	Close() error
}
