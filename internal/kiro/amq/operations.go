package converters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// AMQRefreshTokenRequest represents /refreshToken request payload.
type AMQRefreshTokenRequest struct {
	RefreshToken string `json:"refreshToken"`
	Region       string `json:"region,omitempty"`
}

// AMQRefreshTokenResponse mirrors auth.desktop.kiro.dev refresh response.
type AMQRefreshTokenResponse struct {
	AccessToken  string `json:"accessToken"`
	ExpiresIn    int    `json:"expiresIn"`
	ProfileArn   string `json:"profileArn"`
	RefreshToken string `json:"refreshToken"`
}

// AMQListAvailableModelsRequest corresponds to ListAvailableModels call.
type AMQListAvailableModelsRequest struct {
	Origin string `json:"origin"`
}

// AMQListAvailableModelsResponse keeps response extensible.
type AMQListAvailableModelsResponse struct {
	DefaultModel map[string]any   `json:"defaultModel"`
	Models       []map[string]any `json:"models"`
}

// AMQGetUsageLimitsRequest corresponds to GetUsageLimits call.
type AMQGetUsageLimitsRequest struct {
	Origin          string `json:"origin"`
	IsEmailRequired bool   `json:"isEmailRequired"`
}

// AMQGetUsageLimitsResponse keeps response extensible.
type AMQGetUsageLimitsResponse map[string]any

// AMQSendTelemetryEventRequest represents SendTelemetryEvent payload.
type AMQSendTelemetryEventRequest map[string]any

func (c *AMQHTTPClient) RefreshToken(ctx context.Context, req AMQRefreshTokenRequest) (*AMQRefreshTokenResponse, error) {
	if c == nil || c.doer == nil {
		return nil, fmt.Errorf("amq client not initialized")
	}
	refreshToken := strings.TrimSpace(req.RefreshToken)
	if refreshToken == "" {
		return nil, fmt.Errorf("refreshToken is required")
	}

	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = c.region()
	}

	payload, err := json.Marshal(map[string]string{
		"refreshToken": refreshToken,
	})
	if err != nil {
		return nil, err
	}

	u := kiroRefreshURL(region)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "*/*")
	httpReq.Header.Set("Accept-Encoding", "gzip")
	if ua := strings.TrimSpace(c.refreshUserAgent()); ua != "" {
		httpReq.Header.Set("User-Agent", ua)
	}
	if parsedURL, parseErr := url.Parse(u); parseErr == nil {
		httpReq.Host = parsedURL.Host
	}

	resp, err := c.doer.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &AMQHTTPError{
			StatusCode:  resp.StatusCode,
			Body:        body,
			RequestID:   strings.TrimSpace(resp.Header.Get("x-amzn-requestid")),
			Target:      "refreshToken",
			ContentType: resp.Header.Get("Content-Type"),
		}
	}

	var out AMQRefreshTokenResponse
	if err := decodeJSONBody(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func kiroRefreshURL(region string) string {
	region = strings.TrimSpace(region)
	if region == "" {
		region = "us-east-1"
	}
	return "https://prod." + region + ".auth.desktop.kiro.dev/refreshToken"
}

func (c *AMQHTTPClient) ListAvailableModels(ctx context.Context, req AMQListAvailableModelsRequest) (*AMQListAvailableModelsResponse, error) {
	origin := strings.TrimSpace(req.Origin)
	if origin == "" {
		origin = "KIRO_CLI"
	}
	payload, err := json.Marshal(map[string]string{"origin": origin})
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("origin", origin)
	_, body, err := c.doQPOST(ctx, amqTargetListAvailableModels, q, payload, false)
	if err != nil {
		return nil, err
	}
	var out AMQListAvailableModelsResponse
	if err := decodeJSONBody(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *AMQHTTPClient) GetUsageLimits(ctx context.Context, req AMQGetUsageLimitsRequest) (*AMQGetUsageLimitsResponse, error) {
	origin := strings.TrimSpace(req.Origin)
	if origin == "" {
		origin = "KIRO_CLI"
	}
	payload, err := json.Marshal(map[string]any{
		"origin":          origin,
		"isEmailRequired": req.IsEmailRequired,
	})
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("origin", origin)
	if req.IsEmailRequired {
		q.Set("isEmailRequired", "true")
	} else {
		q.Set("isEmailRequired", "false")
	}
	_, body, err := c.doQPOST(ctx, amqTargetGetUsageLimits, q, payload, false)
	if err != nil {
		return nil, err
	}
	var out AMQGetUsageLimitsResponse
	if err := decodeJSONBody(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *AMQHTTPClient) SendTelemetryEvent(ctx context.Context, req AMQSendTelemetryEventRequest) error {
	if req == nil {
		req = AMQSendTelemetryEventRequest{}
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, _, err = c.doQPOST(ctx, amqTargetSendTelemetryEvent, nil, payload, false)
	return err
}

// GenerateAssistantResponseStream sends GenerateAssistantResponse request and returns structured event stream.
func (c *AMQHTTPClient) GenerateAssistantResponseStream(ctx context.Context, payload []byte) (*AMQGenerateStream, error) {
	resp, _, err := c.doQPOST(ctx, amqTargetGenerateAssistantResponse, nil, payload, true)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("nil response for generate stream")
	}
	ct := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if !strings.Contains(ct, amqContentTypeStream) {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected generate response content-type: %s body=%s", resp.Header.Get("Content-Type"), string(body))
	}
	return newAMQGenerateStream(resp), nil
}
