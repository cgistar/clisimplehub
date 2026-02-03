package kiro

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	kiroShared "clisimplehub/internal/transformer/kiro/shared"
)

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type UsageHTTPError struct {
	StatusCode int
	Body       string
	Hint       string
}

func (e *UsageHTTPError) Error() string {
	payload := struct {
		StatusCode int    `json:"statusCode"`
		Body       string `json:"body"`
		Hint       string `json:"hint,omitempty"`
	}{
		StatusCode: e.StatusCode,
		Body:       e.Body,
		Hint:       strings.TrimSpace(e.Hint),
	}

	b, err := json.Marshal(payload)
	if err == nil {
		return "KIRO_USAGE_HTTP_ERROR: " + string(b)
	}

	msg := strings.TrimSpace(e.Hint)
	if msg == "" {
		msg = "usage request failed"
	}
	if e.StatusCode > 0 && strings.TrimSpace(e.Body) != "" {
		return fmt.Sprintf("%s (HTTP %d): %s", msg, e.StatusCode, strings.TrimSpace(e.Body))
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("%s (HTTP %d)", msg, e.StatusCode)
	}
	return msg
}

func newUsageHTTPError(statusCode int, body string, hint string) *UsageHTTPError {
	return &UsageHTTPError{
		StatusCode: statusCode,
		Body:       strings.TrimSpace(strings.ToValidUTF8(body, "�")),
		Hint:       strings.TrimSpace(hint),
	}
}

type UsageQuery struct {
	AccessToken   string
	ProfileArn    string
	Region        string
	MachineID     string
	UserAgentBase string
	Version       string
}

type UsageResult struct {
	SubscriptionTitle string
	UsageLimit        float64
	CurrentUsage      float64
	Balance           float64
	UsagePct          float64
	IsLowBalance      bool
	Details           *UsageLimitsResponse
}

func ComputeMachineID(refreshToken string) string {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("NativeAPI/" + refreshToken))
	return fmt.Sprintf("%x", sum[:])
}

type UsageLimitsResponse struct {
	SubscriptionInfo   SubscriptionInfo `json:"subscriptionInfo"`
	UsageBreakdownList []UsageBreakdown `json:"usageBreakdownList"`
}

type SubscriptionInfo struct {
	SubscriptionTitle string `json:"subscriptionTitle"`
	Type              string `json:"type"`
}

type UsageBreakdown struct {
	UsageLimitWithPrecision   float64        `json:"usageLimitWithPrecision"`
	CurrentUsageWithPrecision float64        `json:"currentUsageWithPrecision"`
	DisplayName               string         `json:"displayName"`
	FreeTrialInfo             *FreeTrialInfo `json:"freeTrialInfo"`
	Bonuses                   []Bonus        `json:"bonuses"`
}

type FreeTrialInfo struct {
	UsageLimitWithPrecision   float64 `json:"usageLimitWithPrecision"`
	CurrentUsageWithPrecision float64 `json:"currentUsageWithPrecision"`
	FreeTrialStatus           string  `json:"freeTrialStatus"`
}

type Bonus struct {
	BonusCode    string  `json:"bonusCode"`
	UsageLimit   float64 `json:"usageLimit"`
	CurrentUsage float64 `json:"currentUsage"`
	Status       string  `json:"status"`
}

type staticAuthSource struct {
	accessToken   string
	machineID     string
	userAgentBase string
	version       string
}

func (s *staticAuthSource) GetAccessToken() (string, error) { return s.accessToken, nil }
func (s *staticAuthSource) MachineID() string               { return s.machineID }
func (s *staticAuthSource) KiroUserAgentBase() string {
	return kiroShared.KiroUserAgentBaseOrDefault(s.userAgentBase)
}
func (s *staticAuthSource) KiroVersion() string { return kiroShared.KiroVersionOrDefault(s.version) }

func FetchUsage(ctx context.Context, doer HTTPDoer, query UsageQuery) (*UsageResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	region := strings.TrimSpace(query.Region)
	if region == "" {
		region = "us-east-1"
	}

	if doer == nil {
		doer = http.DefaultClient
	}

	accessToken := strings.TrimSpace(query.AccessToken)
	if accessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}

	parsed, status, err := fetchUsageOnce(ctx, doer, query, accessToken, region)
	if err != nil {
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			var httpErr *UsageHTTPError
			if errors.As(err, &httpErr) {
				return nil, newUsageHTTPError(httpErr.StatusCode, httpErr.Body, "accessToken expired or unauthorized; please click Test to refresh accessToken")
			}
			return nil, newUsageHTTPError(status, "", "accessToken expired or unauthorized; please click Test to refresh accessToken")
		}
		return nil, err
	}

	var totalLimit float64
	var totalUsed float64
	for _, b := range parsed.UsageBreakdownList {
		totalLimit += b.UsageLimitWithPrecision
		totalUsed += b.CurrentUsageWithPrecision
		if b.FreeTrialInfo != nil {
			totalLimit += b.FreeTrialInfo.UsageLimitWithPrecision
			totalUsed += b.FreeTrialInfo.CurrentUsageWithPrecision
		}
		for _, bonus := range b.Bonuses {
			totalLimit += bonus.UsageLimit
			totalUsed += bonus.CurrentUsage
		}
	}

	balance := totalLimit - totalUsed
	usagePct := 0.0
	if totalLimit > 0 {
		usagePct = (totalUsed / totalLimit) * 100
	}
	isLowBalance := false
	if totalLimit > 0 && balance/totalLimit < 0.2 {
		isLowBalance = true
	}

	return &UsageResult{
		SubscriptionTitle: strings.TrimSpace(parsed.SubscriptionInfo.SubscriptionTitle),
		UsageLimit:        totalLimit,
		CurrentUsage:      totalUsed,
		Balance:           balance,
		UsagePct:          usagePct,
		IsLowBalance:      isLowBalance,
		Details:           &parsed,
	}, nil
}

func fetchUsageOnce(ctx context.Context, doer HTTPDoer, query UsageQuery, accessToken string, region string) (UsageLimitsResponse, int, error) {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return UsageLimitsResponse{}, 0, fmt.Errorf("accessToken is required")
	}
	if strings.ContainsAny(accessToken, "\r\n") {
		return UsageLimitsResponse{}, 0, fmt.Errorf("invalid accessToken")
	}

	usageURL, err := url.Parse(KiroUsageURL(region))
	if err != nil {
		return UsageLimitsResponse{}, 0, fmt.Errorf("failed to parse usage url: %w", err)
	}
	q := usageURL.Query()
	q.Set("origin", "AI_EDITOR")
	q.Set("resourceType", "AGENTIC_REQUEST")
	if v := strings.TrimSpace(query.ProfileArn); v != "" {
		q.Set("profileArn", v)
	}
	usageURL.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL.String(), nil)
	if err != nil {
		return UsageLimitsResponse{}, 0, fmt.Errorf("failed to create request: %w", err)
	}

	authApplier := NewAuthApplier(&staticAuthSource{
		accessToken:   accessToken,
		machineID:     strings.TrimSpace(query.MachineID),
		userAgentBase: strings.TrimSpace(query.UserAgentBase),
		version:       strings.TrimSpace(query.Version),
	})
	if err := authApplier.Apply(req); err != nil {
		return UsageLimitsResponse{}, 0, err
	}
	// log.Printf("[kiro] fetchUsageOnce req.Header=%v", req.Header)

	resp, err := doer.Do(req)
	if err != nil {
		return UsageLimitsResponse{}, 0, fmt.Errorf("usage request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return UsageLimitsResponse{}, resp.StatusCode, newUsageHTTPError(resp.StatusCode, string(body), "")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return UsageLimitsResponse{}, 0, fmt.Errorf("failed to read usage response: %w", err)
	}

	var parsed UsageLimitsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return UsageLimitsResponse{}, 0, fmt.Errorf("failed to parse usage response: %w", err)
	}
	return parsed, http.StatusOK, nil
}
