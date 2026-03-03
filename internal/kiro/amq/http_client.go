package converters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	kiroapi "clisimplehub/internal/kiro"

	"github.com/google/uuid"
	"golang.org/x/net/proxy"
)

const (
	amqContentTypeJSON   = "application/x-amz-json-1.0"
	amqContentTypeStream = "application/vnd.amazon.eventstream"

	amqTargetGenerateAssistantResponse = "AmazonCodeWhispererStreamingService.GenerateAssistantResponse"
	amqTargetListAvailableModels       = "AmazonCodeWhispererService.ListAvailableModels"
	amqTargetGetUsageLimits            = "AmazonCodeWhispererService.GetUsageLimits"
	amqTargetSendTelemetryEvent        = "AmazonCodeWhispererService.SendTelemetryEvent"

	// 以 test/request 实际抓包为基准，确保 AWS 请求头形态与线上观测一致。
	amqObservedUserAgentBase = "aws-sdk-rust/1.3.12 ua/2.1 api/codewhispererruntime/0.1.13922 os/macos lang/rust/1.92.0 md/appVersion-1.26.2 app/AmazonQ-For-CLI"
)

// AMQTokenProvider provides access token lifecycle for AMQ requests.
type AMQTokenProvider interface {
	AccessToken(ctx context.Context) (string, error)
	RefreshAccessToken(ctx context.Context) (string, error)
}

// AMQRuntimeProvider provides minimal runtime config for AMQ requests.
type AMQRuntimeProvider interface {
	Region() string
	ProxyURL() string
}

// AMQHTTPDoer is the minimal http client abstraction.
type AMQHTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// AMQClientConfig configures the reusable AMQ HTTP client.
type AMQClientConfig struct {
	HTTPDoer       AMQHTTPDoer
	TokenProvider  AMQTokenProvider
	Runtime        AMQRuntimeProvider
	Region         string
	ProxyURL       string
	Timeout        time.Duration
	MaxAttempts    int
	OptOutOverride *bool
}

// AMQHTTPClient is a reusable client for Amazon Q (q.<region>.amazonaws.com) APIs.
type AMQHTTPClient struct {
	doer           AMQHTTPDoer
	tokenProvider  AMQTokenProvider
	regionValue    string
	maxAttempts    int
	optOutOverride *bool
}

// AMQHTTPError is returned for non-2xx upstream responses.
type AMQHTTPError struct {
	StatusCode  int
	Body        []byte
	RequestID   string
	Target      string
	ContentType string
}

func (e *AMQHTTPError) Error() string {
	if e == nil {
		return "amq http error"
	}
	body := strings.TrimSpace(string(e.Body))
	if len(body) > 512 {
		body = body[:512] + "...(truncated)"
	}
	if body != "" {
		return fmt.Sprintf("amq request failed: status=%d target=%s requestId=%s body=%s", e.StatusCode, e.Target, e.RequestID, body)
	}
	return fmt.Sprintf("amq request failed: status=%d target=%s requestId=%s", e.StatusCode, e.Target, e.RequestID)
}

// NewHTTPClient creates a reusable AMQ HTTP client.
func NewHTTPClient(cfg AMQClientConfig) *AMQHTTPClient {
	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}

	doer := cfg.HTTPDoer
	if doer == nil {
		proxyURL := strings.TrimSpace(cfg.ProxyURL)
		if proxyURL == "" && cfg.Runtime != nil {
			proxyURL = strings.TrimSpace(cfg.Runtime.ProxyURL())
		}
		doer = newAMQNativeHTTPClient(proxyURL, cfg.Timeout)
	}

	regionValue := strings.TrimSpace(cfg.Region)
	if regionValue == "" && cfg.Runtime != nil {
		regionValue = strings.TrimSpace(cfg.Runtime.Region())
	}

	return &AMQHTTPClient{
		doer:           doer,
		tokenProvider:  cfg.TokenProvider,
		regionValue:    regionValue,
		maxAttempts:    maxAttempts,
		optOutOverride: cfg.OptOutOverride,
	}
}

func (c *AMQHTTPClient) region() string {
	return kiroapi.ResolveRegion(c.regionValue)
}

func (c *AMQHTTPClient) qBaseURL() string {
	return "https://" + kiroapi.KiroQHost(c.region())
}

func amqAPINameByTarget(target string) string {
	switch strings.TrimSpace(target) {
	case amqTargetGenerateAssistantResponse:
		return "codewhispererstreaming"
	default:
		return "codewhispererruntime"
	}
}

func amqXAmzModeByTarget(target string) string {
	switch strings.TrimSpace(target) {
	case amqTargetListAvailableModels:
		return "m/F,C"
	default:
		return "m/F"
	}
}

func rewriteUserAgentAPI(base, apiName string) string {
	base = strings.TrimSpace(base)
	if base == "" || apiName == "" {
		return base
	}

	parts := strings.Fields(base)
	for i, part := range parts {
		if !strings.HasPrefix(part, "api/") {
			continue
		}
		rest := strings.TrimPrefix(part, "api/")
		if idx := strings.IndexAny(rest, "#/"); idx >= 0 {
			parts[i] = "api/" + apiName + rest[idx:]
		} else {
			parts[i] = "api/" + apiName
		}
		return strings.Join(parts, " ")
	}
	return strings.Join(append(parts, "api/"+apiName), " ")
}

func buildXAmzUserAgentFromUserAgent(userAgent, target string) string {
	if strings.TrimSpace(userAgent) == "" {
		return userAgent
	}

	mode := amqXAmzModeByTarget(target)
	parts := strings.Fields(userAgent)

	inserted := false
	for i, part := range parts {
		if strings.HasPrefix(part, "md/appVersion-") || strings.HasPrefix(part, "m/") {
			parts[i] = mode
			inserted = true
			break
		}
	}
	if !inserted {
		for i, part := range parts {
			if strings.HasPrefix(part, "app/") {
				withMode := append([]string{}, parts[:i]...)
				withMode = append(withMode, mode)
				parts = append(withMode, parts[i:]...)
				inserted = true
				break
			}
		}
	}
	if !inserted {
		parts = append(parts, mode)
	}

	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.HasPrefix(part, "md/appVersion-") {
			continue
		}
		out = append(out, part)
	}
	return strings.Join(out, " ")
}

func (c *AMQHTTPClient) buildUserAgents(target string) (string, string) {
	uaBase := amqObservedUserAgentBase
	userAgent := rewriteUserAgentAPI(uaBase, amqAPINameByTarget(target))
	xAmzUserAgent := buildXAmzUserAgentFromUserAgent(userAgent, target)
	return userAgent, xAmzUserAgent
}

func (c *AMQHTTPClient) refreshUserAgent() string {
	return "Kiro-CLI"
}

func (c *AMQHTTPClient) defaultHeaders(target string, token string) http.Header {
	h := make(http.Header)
	h.Set("Content-Type", amqContentTypeJSON)
	h.Set("x-amz-target", target)
	h.Set("Accept", "*/*")
	h.Set("amz-sdk-invocation-id", uuid.NewString())
	h.Set("amz-sdk-request", fmt.Sprintf("attempt=1; max=%d", c.maxAttempts))

	if token != "" {
		h.Set("Authorization", "Bearer "+token)
	}

	optOut := false
	if c.optOutOverride != nil {
		optOut = *c.optOutOverride
	}
	h.Set("x-amzn-codewhisperer-optout", fmt.Sprintf("%t", optOut))

	ua, xAmzUA := c.buildUserAgents(target)
	h.Set("User-Agent", ua)
	h.Set("x-amz-user-agent", xAmzUA)
	return h
}

func isRetryableNetworkError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "EOF") || strings.Contains(s, "connection reset")
}

func (c *AMQHTTPClient) doQPOST(
	ctx context.Context,
	target string,
	query url.Values,
	payload []byte,
	expectStream bool,
) (*http.Response, []byte, error) {
	if c == nil || c.doer == nil {
		return nil, nil, fmt.Errorf("amq client not initialized")
	}

	if payload == nil {
		payload = []byte("{}")
	}

	token := ""
	if c.tokenProvider != nil {
		var err error
		token, err = c.tokenProvider.AccessToken(ctx)
		if err != nil {
			return nil, nil, err
		}
		token = strings.TrimSpace(token)
	}

	buildReq := func(authToken string) (*http.Request, error) {
		u, err := url.Parse(c.qBaseURL())
		if err != nil {
			return nil, err
		}
		u.Path = "/"
		if len(query) > 0 {
			u.RawQuery = query.Encode()
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header = c.defaultHeaders(target, authToken)
		req.Host = u.Host
		return req, nil
	}

	attempts := c.maxAttempts
	if attempts < 1 {
		attempts = 1
	}

	var resp *http.Response
	var err error
	usedRefreshedToken := false

	for attempt := 1; attempt <= attempts; attempt++ {
		var req *http.Request
		req, err = buildReq(token)
		if err != nil {
			return nil, nil, err
		}

		resp, err = c.doer.Do(req)
		if err != nil {
			if attempt < attempts && isRetryableNetworkError(err) {
				continue
			}
			return nil, nil, err
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			if c.tokenProvider != nil && !usedRefreshedToken {
				_ = resp.Body.Close()
				refreshed, refreshErr := c.tokenProvider.RefreshAccessToken(ctx)
				if refreshErr == nil {
					refreshed = strings.TrimSpace(refreshed)
					if refreshed != "" {
						token = refreshed
						usedRefreshedToken = true
						attempt-- // refresh 不消耗重试次数
						continue
					}
				}
			}
		}

		if expectStream {
			if resp.StatusCode == http.StatusOK {
				return resp, nil, nil
			}
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil {
				return nil, nil, readErr
			}
			return nil, body, &AMQHTTPError{
				StatusCode:  resp.StatusCode,
				Body:        body,
				RequestID:   strings.TrimSpace(resp.Header.Get("x-amzn-RequestId")),
				Target:      target,
				ContentType: resp.Header.Get("Content-Type"),
			}
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, nil, readErr
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, body, &AMQHTTPError{
				StatusCode:  resp.StatusCode,
				Body:        body,
				RequestID:   strings.TrimSpace(resp.Header.Get("x-amzn-RequestId")),
				Target:      target,
				ContentType: resp.Header.Get("Content-Type"),
			}
		}
		return resp, body, nil
	}

	if err != nil {
		return nil, nil, err
	}
	if resp == nil {
		return nil, nil, fmt.Errorf("amq request failed: no response")
	}
	return nil, nil, fmt.Errorf("amq request failed after retries")
}

func newAMQNativeHTTPClient(proxyURL string, timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	// 不设 http.Client.Timeout——它覆盖整个响应生命周期（含流式 body 读取），
	// 长对话会中途超时。改用 Transport.ResponseHeaderTimeout 仅控制首字节等待，
	// 流式读取依赖 ctx 取消。
	client := &http.Client{}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = timeout

	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		client.Transport = transport
		return client
	}

	parsed, err := url.Parse(proxyURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: invalid AMQ proxy URL: %v\n", err)
		client.Transport = transport
		return client
	}

	switch parsed.Scheme {
	case "http", "https":
		transport.Proxy = http.ProxyURL(parsed)
	case "socks5":
		var auth *proxy.Auth
		if parsed.User != nil {
			username := parsed.User.Username()
			password, _ := parsed.User.Password()
			auth = &proxy.Auth{User: username, Password: password}
		}
		dialer, dialErr := proxy.SOCKS5("tcp", parsed.Host, auth, proxy.Direct)
		if dialErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: invalid AMQ SOCKS5 proxy: %v\n", dialErr)
			client.Transport = transport
			return client
		}
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		}
	default:
		fmt.Fprintf(os.Stderr, "Warning: unsupported AMQ proxy scheme: %s\n", parsed.Scheme)
	}

	client.Transport = transport
	return client
}

func decodeJSONBody(body []byte, out any) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}
	return nil
}
