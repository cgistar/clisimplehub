package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

const (
	UsageAPIURL               = "https://chatgpt.com/backend-api/wham/usage"
	CODEX_RESET_CREDITS_URL   = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	CODEX_CONSUME_CREDITS_URL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume"
)

// UsageRateLimit represents rate limit information
type UsageRateLimit struct {
	Allowed         bool                  `json:"allowed"`
	LimitReached    bool                  `json:"limit_reached"`
	PrimaryWindow   *UsageRateLimitWindow `json:"primary_window"`
	SecondaryWindow *UsageRateLimitWindow `json:"secondary_window"`
}

// UsageRateLimitWindow represents a rate limit time window
type UsageRateLimitWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int     `json:"limit_window_seconds"`
	ResetAfterSeconds  int     `json:"reset_after_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

// UsageCredits 表示账户额度和近似消息容量。
type UsageCredits struct {
	HasCredits          bool     `json:"has_credits"`
	Unlimited           bool     `json:"unlimited"`
	OverageLimitReached bool     `json:"overage_limit_reached"`
	Balance             *float64 `json:"balance"`
	ApproxLocalMessages *int     `json:"approx_local_messages"`
	ApproxCloudMessages *int     `json:"approx_cloud_messages"`
}

func (c *UsageCredits) UnmarshalJSON(data []byte) error {
	var raw struct {
		HasCredits          bool            `json:"has_credits"`
		Unlimited           bool            `json:"unlimited"`
		OverageLimitReached bool            `json:"overage_limit_reached"`
		Balance             json.RawMessage `json:"balance"`
		ApproxLocalMessages json.RawMessage `json:"approx_local_messages"`
		ApproxCloudMessages json.RawMessage `json:"approx_cloud_messages"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	balance, err := optionalFloat64(raw.Balance)
	if err != nil {
		return fmt.Errorf("credits.balance: %w", err)
	}
	approxLocalMessages, err := optionalInt(raw.ApproxLocalMessages)
	if err != nil {
		return fmt.Errorf("credits.approx_local_messages: %w", err)
	}
	approxCloudMessages, err := optionalInt(raw.ApproxCloudMessages)
	if err != nil {
		return fmt.Errorf("credits.approx_cloud_messages: %w", err)
	}

	c.HasCredits = raw.HasCredits
	c.Unlimited = raw.Unlimited
	c.OverageLimitReached = raw.OverageLimitReached
	c.Balance = balance
	c.ApproxLocalMessages = approxLocalMessages
	c.ApproxCloudMessages = approxCloudMessages
	return nil
}

// UsageSpendControl 表示消费限制状态。
type UsageSpendControl struct {
	Reached         bool     `json:"reached"`
	IndividualLimit *float64 `json:"individual_limit"`
}

func (s *UsageSpendControl) UnmarshalJSON(data []byte) error {
	var raw struct {
		Reached         bool            `json:"reached"`
		IndividualLimit json.RawMessage `json:"individual_limit"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	individualLimit, err := optionalFloat64(raw.IndividualLimit)
	if err != nil {
		return fmt.Errorf("spend_control.individual_limit: %w", err)
	}

	s.Reached = raw.Reached
	s.IndividualLimit = individualLimit
	return nil
}

func optionalFloat64(raw json.RawMessage) (*float64, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}

	var num float64
	if err := json.Unmarshal(raw, &num); err == nil {
		return &num, nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	num, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nil, err
	}
	return &num, nil
}

func optionalInt(raw json.RawMessage) (*int, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}

	var num int
	if err := json.Unmarshal(raw, &num); err == nil {
		return &num, nil
	}

	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err == nil {
		for _, value := range values {
			parsed, err := optionalInt(value)
			if err != nil {
				return nil, err
			}
			if parsed != nil {
				return parsed, nil
			}
		}
		return nil, nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	parsed, err := strconv.Atoi(text)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

// UsageRateLimitResetCredits 表示可用的限流重置次数。
type UsageRateLimitResetCredits struct {
	AvailableCount int `json:"available_count"`
}

// UsagePromo represents promotional information
type UsagePromo struct {
	CampaignID string `json:"campaign_id"`
	Message    string `json:"message"`
}

// UsageResponse represents the full response from the usage API
type UsageResponse struct {
	UserID                string                      `json:"user_id"`
	AccountID             string                      `json:"account_id"`
	Email                 string                      `json:"email"`
	PlanType              string                      `json:"plan_type"`
	RateLimit             *UsageRateLimit             `json:"rate_limit"`
	CodeReviewRateLimit   *UsageRateLimit             `json:"code_review_rate_limit"`
	AdditionalRateLimits  json.RawMessage             `json:"additional_rate_limits"`
	Credits               *UsageCredits               `json:"credits"`
	SpendControl          *UsageSpendControl          `json:"spend_control"`
	RateLimitReachedType  *json.RawMessage            `json:"rate_limit_reached_type"`
	Promo                 *UsagePromo                 `json:"promo"`
	ReferralBeacon        json.RawMessage             `json:"referral_beacon"`
	RateLimitResetCredits *UsageRateLimitResetCredits `json:"rate_limit_reset_credits"`
}

// UsageQuery contains parameters for fetching usage
type UsageQuery struct {
	AccessToken string
	AccountID   string
	UserAgent   string
	Originator  string
	SessionID   string
	ProxyURL    string
}

type ResetQuery struct {
	AccessToken string
	AccountID   string
	UserAgent   string
	Originator  string
	RedeemID    string
	CreditID    string
	ProxyURL    string
}

// CodexResetResponse represents the response from the reset credits API
type CodexResetResponse struct {
	Code         string            `json:"code"`
	Credit       *CodexResetCredit `json:"credit"`
	WindowsReset int               `json:"windows_reset"`
}

type CodexResetCredit struct {
	ID                string  `json:"id"`
	ResetType         string  `json:"reset_type"`
	IsSupportedByPlan bool    `json:"is_supported_by_plan"`
	Status            string  `json:"status"`
	GrantedAt         string  `json:"granted_at"`
	ExpiresAt         string  `json:"expires_at"`
	RedeemStartedAt   string  `json:"redeem_started_at"`
	RedeemedAt        string  `json:"redeemed_at"`
	ProfileImageURL   *string `json:"profile_image_url"`
	ProfileUserID     *string `json:"profile_user_id"`
	Title             *string `json:"title"`
	Description       *string `json:"description"`
}

// CodexResetCreditsListResponse 对应 GET /wham/rate-limit-reset-credits 响应。
type CodexResetCreditsListResponse struct {
	Credits          []CodexResetCredit `json:"credits"`
	AvailableCount   int                `json:"available_count"`
	TotalEarnedCount int                `json:"total_earned_count"`
}

func PostCodexReset(ctx context.Context, client *http.Client, query ResetQuery) (*CodexResetResponse, error) {
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
	redeemID := strings.TrimSpace(query.RedeemID)
	if redeemID == "" {
		redeemID = uuid.NewString()
	}

	creditID := strings.TrimSpace(query.CreditID)
	if creditID == "" {
		return nil, fmt.Errorf("creditId is required")
	}
	bodyBytes, err := json.Marshal(map[string]any{
		"redeem_request_id": redeemID,
		"credit_id":         creditID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, CODEX_CONSUME_CREDITS_URL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	builder := NewHeaderBuilder(accessToken, query.AccountID)
	if query.UserAgent != "" {
		builder.WithUserAgent(query.UserAgent)
	}
	if query.Originator != "" {
		builder.WithOriginator(query.Originator)
	}
	builder.ApplyTo(req)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		snippet := strings.TrimSpace(string(bodyBytes))
		if snippet == "" {
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncateRunes(snippet, 200))
	}

	var resetResp CodexResetResponse
	if err := json.Unmarshal(bodyBytes, &resetResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &resetResp, nil
}

// ListResetCredits 拉取账号当前可用的限制重置次数列表。
// 对应 GET /wham/rate-limit-reset-credits，未授权时返回 (nil, error)。
func ListResetCredits(ctx context.Context, client *http.Client, query ResetQuery) (*CodexResetCreditsListResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		client = http.DefaultClient
	}

	accessToken := strings.TrimSpace(query.AccessToken)
	if accessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CODEX_RESET_CREDITS_URL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	builder := NewHeaderBuilder(accessToken, query.AccountID)
	if query.UserAgent != "" {
		builder.WithUserAgent(query.UserAgent)
	}
	if query.Originator != "" {
		builder.WithOriginator(query.Originator)
	}
	builder.ApplyTo(req)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		snippet := strings.TrimSpace(string(bodyBytes))
		if snippet == "" {
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncateRunes(snippet, 200))
	}

	var listResp CodexResetCreditsListResponse
	if err := json.Unmarshal(bodyBytes, &listResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return &listResp, nil
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
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
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if query.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", query.AccountID)
	}
	if query.UserAgent != "" {
		req.Header.Set("User-Agent", query.UserAgent)
	}
	// builder := NewHeaderBuilder(accessToken, query.AccountID)
	// if query.UserAgent != "" {
	// 	builder.WithUserAgent(query.UserAgent)
	// }
	// if query.Originator != "" {
	// 	builder.WithOriginator(query.Originator)
	// }
	// if query.SessionID != "" {
	// 	builder.WithSessionID(query.SessionID)
	// }
	// builder.ApplyTo(req)

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
	UsedPercent      float64 `json:"usedPercent"`
	RemainingSeconds int     `json:"remainingSeconds"`
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
