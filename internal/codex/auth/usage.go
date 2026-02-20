package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	UsageAPIURL = "https://chatgpt.com/backend-api/wham/usage"
)

// UsageRateLimit represents rate limit information
type UsageRateLimit struct {
	Allowed       bool                  `json:"allowed"`
	LimitReached  bool                  `json:"limit_reached"`
	PrimaryWindow *UsageRateLimitWindow `json:"primary_window"`
	SecondaryWindow *UsageRateLimitWindow `json:"secondary_window"`
}

// UsageRateLimitWindow represents a rate limit time window
type UsageRateLimitWindow struct {
	UsedPercent         float64 `json:"used_percent"`
	LimitWindowSeconds  int     `json:"limit_window_seconds"`
	ResetAfterSeconds   int     `json:"reset_after_seconds"`
	ResetAt             int64   `json:"reset_at"`
}

// UsagePromo represents promotional information
type UsagePromo struct {
	CampaignID string `json:"campaign_id"`
	Message    string `json:"message"`
}

// UsageResponse represents the full response from the usage API
type UsageResponse struct {
	UserID              string          `json:"user_id"`
	AccountID           string          `json:"account_id"`
	Email               string          `json:"email"`
	PlanType            string          `json:"plan_type"`
	RateLimit           *UsageRateLimit `json:"rate_limit"`
	CodeReviewRateLimit *UsageRateLimit `json:"code_review_rate_limit"`
	AdditionalRateLimits json.RawMessage `json:"additional_rate_limits"`
	Credits             json.RawMessage `json:"credits"`
	Promo               *UsagePromo     `json:"promo"`
}

// UsageQuery contains parameters for fetching usage
type UsageQuery struct {
	AccessToken string
	AccountID   string
	UserAgent   string
	ProxyURL    string
}

// FetchUsage fetches usage information from the Codex API
func FetchUsage(ctx context.Context, client *http.Client, query UsageQuery) (*UsageResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if client == nil {
		client = http.DefaultClient
	}

	accessToken := query.AccessToken
	if accessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, UsageAPIURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Use HeaderBuilder for consistent header construction
	builder := NewHeaderBuilder(accessToken, query.AccountID)
	if query.UserAgent != "" {
		builder.WithUserAgent(query.UserAgent)
	}
	builder.ApplyTo(req)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var usageResp UsageResponse
	if err := json.Unmarshal(body, &usageResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &usageResp, nil
}

// UsageResult represents simplified usage information for frontend
type UsageResult struct {
	Primary   *UsageWindow `json:"primary,omitempty"`
	Secondary *UsageWindow `json:"secondary,omitempty"`
}

// UsageWindow represents a simplified rate limit window
type UsageWindow struct {
	UsedPercent       float64 `json:"usedPercent"`
	RemainingSeconds  int     `json:"remainingSeconds"`
}

// SimplifyUsageResponse converts the full API response to a simplified format
func SimplifyUsageResponse(resp *UsageResponse) *UsageResult {
	if resp == nil || resp.RateLimit == nil {
		return nil
	}

	result := &UsageResult{}

	if resp.RateLimit.PrimaryWindow != nil {
		result.Primary = &UsageWindow{
			UsedPercent:      resp.RateLimit.PrimaryWindow.UsedPercent,
			RemainingSeconds: resp.RateLimit.PrimaryWindow.ResetAfterSeconds,
		}
	}

	if resp.RateLimit.SecondaryWindow != nil {
		result.Secondary = &UsageWindow{
			UsedPercent:      resp.RateLimit.SecondaryWindow.UsedPercent,
			RemainingSeconds: resp.RateLimit.SecondaryWindow.ResetAfterSeconds,
		}
	}

	return result
}
