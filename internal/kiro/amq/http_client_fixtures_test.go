package converters

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"clisimplehub/internal/kiro/streaming"

	"github.com/google/uuid"
)

func splitFixtureHeaderBody(data []byte) (string, []byte) {
	if idx := bytes.Index(data, []byte("\r\n\r\n")); idx >= 0 {
		return string(data[:idx]), data[idx+4:]
	}
	if idx := bytes.Index(data, []byte("\n\n")); idx >= 0 {
		return string(data[:idx]), data[idx+2:]
	}
	return string(data), nil
}

func getHeaderValue(headerText, key string) string {
	for _, line := range strings.Split(headerText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		prefix := strings.ToLower(key) + ":"
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return ""
}

func parseChunkedBody(t *testing.T, body []byte) []byte {
	t.Helper()
	r := bufio.NewReader(bytes.NewReader(body))
	var out bytes.Buffer

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("read chunk size failed: %v", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sz, err := strconv.ParseInt(line, 16, 64)
		if err != nil {
			t.Fatalf("invalid chunk size %q: %v", line, err)
		}
		if sz == 0 {
			break
		}
		buf := make([]byte, sz)
		if _, err := io.ReadFull(r, buf); err != nil {
			t.Fatalf("read chunk payload failed: %v", err)
		}
		out.Write(buf)

		b, err := r.ReadByte()
		if err != nil {
			t.Fatalf("read chunk newline failed: %v", err)
		}
		if b == '\r' {
			next, err := r.ReadByte()
			if err != nil {
				t.Fatalf("read chunk newline second byte failed: %v", err)
			}
			if next != '\n' {
				t.Fatalf("invalid chunk newline second byte: %q", next)
			}
		} else if b != '\n' {
			t.Fatalf("invalid chunk newline first byte: %q", b)
		}
	}

	return out.Bytes()
}

func TestRequestFixturesCoveredByAMQClient(t *testing.T) {
	requestFiles, err := filepath.Glob(filepath.Join("..", "..", "..", "test", "request", "*request*.txt"))
	if err != nil {
		t.Fatalf("glob request fixtures: %v", err)
	}
	if len(requestFiles) == 0 {
		t.Fatalf("no request fixtures found")
	}

	seen := map[string]bool{}

	for _, file := range requestFiles {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read fixture %s: %v", file, err)
		}
		headerText, _ := splitFixtureHeaderBody(data)
		lines := strings.Split(strings.TrimSpace(headerText), "\n")
		if len(lines) == 0 {
			t.Fatalf("empty header fixture: %s", file)
		}

		requestLine := strings.TrimSpace(lines[0])
		parts := strings.Split(requestLine, " ")
		if len(parts) < 2 {
			t.Fatalf("invalid request line in %s: %s", file, requestLine)
		}
		path := parts[1]
		host := getHeaderValue(headerText, "host")
		target := getHeaderValue(headerText, "x-amz-target")

		switch {
		case strings.Contains(host, "auth.desktop.kiro.dev"):
			if path != "/refreshToken" {
				t.Fatalf("unexpected auth path in %s: %s", file, path)
			}
			seen["refreshToken"] = true
		case strings.Contains(host, "q.us-east-1.amazonaws.com"):
			switch target {
			case amqTargetGenerateAssistantResponse:
				seen[amqTargetGenerateAssistantResponse] = true
			case amqTargetListAvailableModels:
				seen[amqTargetListAvailableModels] = true
			case amqTargetGetUsageLimits:
				seen[amqTargetGetUsageLimits] = true
			case amqTargetSendTelemetryEvent:
				seen[amqTargetSendTelemetryEvent] = true
			default:
				t.Fatalf("unknown q target in %s: %s", file, target)
			}
		default:
			t.Fatalf("unknown host in %s: %s", file, host)
		}
	}

	required := []string{
		"refreshToken",
		amqTargetGenerateAssistantResponse,
		amqTargetListAvailableModels,
		amqTargetGetUsageLimits,
		amqTargetSendTelemetryEvent,
	}
	for _, key := range required {
		if !seen[key] {
			t.Fatalf("fixture coverage missing operation: %s", key)
		}
	}
}

func TestGenerateAssistantResponseFixtureCanDecodeEvents(t *testing.T) {
	file := filepath.Join("..", "..", "..", "test", "request", "[4] response_q.us-east-1.amazonaws.com_message.txt")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	headerText, body := splitFixtureHeaderBody(data)
	ct := getHeaderValue(headerText, "content-type")
	if !strings.Contains(strings.ToLower(ct), amqContentTypeStream) {
		t.Fatalf("unexpected fixture content-type: %s", ct)
	}

	rawStream := parseChunkedBody(t, body)
	if len(rawStream) == 0 {
		t.Fatalf("empty decoded stream payload")
	}

	decoder := streaming.NewEventStreamDecoder()
	if err := decoder.Feed(rawStream); err != nil {
		t.Fatalf("decoder feed: %v", err)
	}
	frames, err := decoder.DecodeAll()
	if err != nil {
		t.Fatalf("decoder decode: %v", err)
	}
	if len(frames) == 0 {
		t.Fatalf("no eventstream frames decoded")
	}

	foundAssistant := false
	for _, frame := range frames {
		ev, err := streaming.EventFromFrame(frame)
		if err != nil {
			continue
		}
		if ev.Type == streaming.EventAssistantResponse && strings.TrimSpace(ev.Content) != "" {
			foundAssistant = true
			break
		}
	}
	if !foundAssistant {
		t.Fatalf("no assistant response event decoded from fixture")
	}
}

type fixtureRequestMessage struct {
	Index        int
	Method       string
	PathAndQuery string
	Host         string
	Target       string
	Headers      http.Header
	Body         []byte
}

type fixtureResponseMessage struct {
	Index      int
	StatusCode int
	Headers    http.Header
	Body       []byte
}

func loadFixtureRaw(t *testing.T, index int, kind string) []byte {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "test", "request")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read fixture dir: %v", err)
	}
	prefix := fmt.Sprintf("[%d] ", index)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.Contains(name, kind) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		return data
	}
	t.Fatalf("fixture not found: index=%d kind=%s", index, kind)
	return nil
}

func loadFixtureRequestMessage(t *testing.T, index int) fixtureRequestMessage {
	t.Helper()
	data := loadFixtureRaw(t, index, "request")
	headerText, body := splitFixtureHeaderBody(data)
	lines := strings.Split(strings.TrimSpace(headerText), "\n")
	if len(lines) == 0 {
		t.Fatalf("empty request header fixture for index %d", index)
	}
	fields := strings.Fields(strings.TrimSpace(lines[0]))
	if len(fields) < 2 {
		t.Fatalf("invalid request line for index %d: %s", index, lines[0])
	}

	headers := make(http.Header)
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sep := strings.Index(line, ":")
		if sep <= 0 {
			continue
		}
		key := http.CanonicalHeaderKey(strings.TrimSpace(line[:sep]))
		val := strings.TrimSpace(line[sep+1:])
		headers.Add(key, val)
	}

	return fixtureRequestMessage{
		Index:        index,
		Method:       fields[0],
		PathAndQuery: fields[1],
		Host:         getHeaderValue(headerText, "host"),
		Target:       getHeaderValue(headerText, "x-amz-target"),
		Headers:      headers,
		Body:         body,
	}
}

func parseFixtureStatusCode(t *testing.T, firstLine string) int {
	t.Helper()
	for _, token := range strings.Fields(strings.TrimSpace(firstLine)) {
		if n, err := strconv.Atoi(token); err == nil && n >= 100 && n <= 599 {
			return n
		}
	}
	t.Fatalf("invalid status line: %s", firstLine)
	return 0
}

func loadFixtureResponseMessage(t *testing.T, index int) fixtureResponseMessage {
	t.Helper()
	data := loadFixtureRaw(t, index, "response")
	headerText, body := splitFixtureHeaderBody(data)
	lines := strings.Split(strings.TrimSpace(headerText), "\n")
	if len(lines) == 0 {
		t.Fatalf("empty response header fixture for index %d", index)
	}

	statusCode := parseFixtureStatusCode(t, lines[0])
	headers := make(http.Header)
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sep := strings.Index(line, ":")
		if sep <= 0 {
			continue
		}
		key := http.CanonicalHeaderKey(strings.TrimSpace(line[:sep]))
		val := strings.TrimSpace(line[sep+1:])
		headers.Add(key, val)
	}

	if strings.Contains(strings.ToLower(headers.Get("Transfer-Encoding")), "chunked") {
		body = parseChunkedBody(t, body)
	}

	return fixtureResponseMessage{
		Index:      index,
		StatusCode: statusCode,
		Headers:    headers,
		Body:       body,
	}
}

func jsonEqualOrRawEqual(expected, got []byte) bool {
	expected = bytes.TrimSpace(expected)
	got = bytes.TrimSpace(got)
	var a any
	var b any
	if len(expected) > 0 && len(got) > 0 && json.Unmarshal(expected, &a) == nil && json.Unmarshal(got, &b) == nil {
		return reflect.DeepEqual(a, b)
	}
	return bytes.Equal(expected, got)
}

func assertPathAndQueryEqual(t *testing.T, expectedPathAndQuery string, got *url.URL) {
	t.Helper()
	wantURL, err := url.ParseRequestURI(expectedPathAndQuery)
	if err != nil {
		t.Fatalf("parse expected path/query failed: %v", err)
	}
	if got.Path != wantURL.Path {
		t.Fatalf("unexpected path: got=%s want=%s", got.Path, wantURL.Path)
	}
	if got.Query().Encode() != wantURL.Query().Encode() {
		t.Fatalf("unexpected query: got=%s want=%s", got.Query().Encode(), wantURL.Query().Encode())
	}
}

type fixtureDoer struct {
	t        *testing.T
	expected fixtureRequestMessage
	response fixtureResponseMessage
}

func (d *fixtureDoer) Do(req *http.Request) (*http.Response, error) {
	d.t.Helper()
	if req.Method != d.expected.Method {
		d.t.Fatalf("fixture[%d] unexpected method: got=%s want=%s", d.expected.Index, req.Method, d.expected.Method)
	}
	if req.URL.Host != d.expected.Host {
		d.t.Fatalf("fixture[%d] unexpected host: got=%s want=%s", d.expected.Index, req.URL.Host, d.expected.Host)
	}
	if req.Host != d.expected.Host {
		d.t.Fatalf("fixture[%d] unexpected req.Host: got=%s want=%s", d.expected.Index, req.Host, d.expected.Host)
	}
	assertPathAndQueryEqual(d.t, d.expected.PathAndQuery, req.URL)
	if d.expected.Target != "" {
		if got := strings.TrimSpace(req.Header.Get("x-amz-target")); got != d.expected.Target {
			d.t.Fatalf("fixture[%d] unexpected x-amz-target: got=%s want=%s", d.expected.Index, got, d.expected.Target)
		}

		exactKeys := []string{
			"Content-Type",
			"x-amz-target",
			"User-Agent",
			"x-amz-user-agent",
			"x-amzn-codewhisperer-optout",
			"Authorization",
			"amz-sdk-request",
			"Accept",
			"Accept-Encoding",
		}
		for _, key := range exactKeys {
			want := strings.TrimSpace(d.expected.Headers.Get(key))
			got := strings.TrimSpace(req.Header.Get(key))
			if want == "" {
				continue
			}
			if got != want {
				d.t.Fatalf("fixture[%d] header mismatch %s: got=%q want=%q", d.expected.Index, key, got, want)
			}
		}

		invocationID := strings.TrimSpace(req.Header.Get("amz-sdk-invocation-id"))
		if invocationID == "" {
			d.t.Fatalf("fixture[%d] missing required header: amz-sdk-invocation-id", d.expected.Index)
		}
		if _, err := uuid.Parse(invocationID); err != nil {
			d.t.Fatalf("fixture[%d] invalid amz-sdk-invocation-id: %v", d.expected.Index, err)
		}
	} else {
		exactKeys := []string{"Content-Type", "User-Agent", "Accept", "Accept-Encoding"}
		for _, key := range exactKeys {
			want := strings.TrimSpace(d.expected.Headers.Get(key))
			got := strings.TrimSpace(req.Header.Get(key))
			if want == "" {
				continue
			}
			if got != want {
				d.t.Fatalf("fixture[%d] refresh header mismatch %s: got=%q want=%q", d.expected.Index, key, got, want)
			}
		}
	}

	gotBody, err := io.ReadAll(req.Body)
	if err != nil {
		d.t.Fatalf("fixture[%d] read request body failed: %v", d.expected.Index, err)
	}
	if !jsonEqualOrRawEqual(d.expected.Body, gotBody) {
		d.t.Fatalf("fixture[%d] request body mismatch", d.expected.Index)
	}

	return &http.Response{
		StatusCode: d.response.StatusCode,
		Header:     d.response.Headers.Clone(),
		Body:       io.NopCloser(bytes.NewReader(d.response.Body)),
		Status:     fmt.Sprintf("%d %s", d.response.StatusCode, http.StatusText(d.response.StatusCode)),
	}, nil
}

type captureTargetHeadersDoer struct {
	t         *testing.T
	responses map[string]fixtureResponseMessage
	captured  map[string][2]string
}

func (d *captureTargetHeadersDoer) Do(req *http.Request) (*http.Response, error) {
	d.t.Helper()
	target := strings.TrimSpace(req.Header.Get("x-amz-target"))
	if target == "" {
		return nil, fmt.Errorf("missing x-amz-target")
	}
	respFixture, ok := d.responses[target]
	if !ok {
		return nil, fmt.Errorf("missing fixture response for target %s", target)
	}
	if d.captured == nil {
		d.captured = make(map[string][2]string)
	}
	d.captured[target] = [2]string{
		strings.TrimSpace(req.Header.Get("User-Agent")),
		strings.TrimSpace(req.Header.Get("x-amz-user-agent")),
	}
	return &http.Response{
		StatusCode: respFixture.StatusCode,
		Header:     respFixture.Headers.Clone(),
		Body:       io.NopCloser(bytes.NewReader(respFixture.Body)),
		Status:     fmt.Sprintf("%d %s", respFixture.StatusCode, http.StatusText(respFixture.StatusCode)),
	}, nil
}

type fixtureTokenProvider struct {
	token string
}

func (p fixtureTokenProvider) AccessToken(context.Context) (string, error) {
	return p.token, nil
}

func (p fixtureTokenProvider) RefreshAccessToken(context.Context) (string, error) {
	return p.token, nil
}

type fixtureRuntimeProvider struct {
	region   string
	proxyURL string
}

func (p fixtureRuntimeProvider) Region() string   { return p.region }
func (p fixtureRuntimeProvider) ProxyURL() string { return p.proxyURL }

func newFixtureClientForCase(t *testing.T, reqFixture fixtureRequestMessage, respFixture fixtureResponseMessage) *AMQHTTPClient {
	t.Helper()
	authHeader := strings.TrimSpace(reqFixture.Headers.Get("Authorization"))
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	optOut := strings.EqualFold(strings.TrimSpace(reqFixture.Headers.Get("x-amzn-codewhisperer-optout")), "true")
	return NewHTTPClient(AMQClientConfig{
		HTTPDoer:      &fixtureDoer{t: t, expected: reqFixture, response: respFixture},
		TokenProvider: fixtureTokenProvider{token: token},
		Runtime: fixtureRuntimeProvider{
			region:   "us-east-1",
			proxyURL: "",
		},
		MaxAttempts:    3,
		OptOutOverride: &optOut,
	})
}

func decodeFixtureJSONMap(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	out := map[string]any{}
	if err := json.Unmarshal(bytes.TrimSpace(payload), &out); err != nil {
		t.Fatalf("decode fixture json map failed: %v", err)
	}
	return out
}

func TestAMQHTTPClient_FixtureRoundTrip_NonStreamingOperations(t *testing.T) {
	indices := []int{1, 3, 5, 30, 32, 60, 67, 68, 71}
	for _, index := range indices {
		reqFixture := loadFixtureRequestMessage(t, index)
		respFixture := loadFixtureResponseMessage(t, index)
		client := newFixtureClientForCase(t, reqFixture, respFixture)
		ctx := context.Background()

		switch {
		case strings.Contains(reqFixture.Host, "auth.desktop.kiro.dev"):
			var reqPayload AMQRefreshTokenRequest
			if err := json.Unmarshal(bytes.TrimSpace(reqFixture.Body), &reqPayload); err != nil {
				t.Fatalf("fixture[%d] decode refresh request body failed: %v", index, err)
			}
			reqPayload.Region = "us-east-1"
			got, err := client.RefreshToken(ctx, reqPayload)
			if err != nil {
				t.Fatalf("fixture[%d] refresh token failed: %v", index, err)
			}
			if strings.TrimSpace(got.AccessToken) == "" || strings.TrimSpace(got.RefreshToken) == "" {
				t.Fatalf("fixture[%d] refresh response missing tokens", index)
			}
		case reqFixture.Target == amqTargetListAvailableModels:
			reqBody := decodeFixtureJSONMap(t, reqFixture.Body)
			got, err := client.ListAvailableModels(ctx, AMQListAvailableModelsRequest{
				Origin: fmt.Sprint(reqBody["origin"]),
			})
			if err != nil {
				t.Fatalf("fixture[%d] list models failed: %v", index, err)
			}
			if got == nil {
				t.Fatalf("fixture[%d] list models returned nil", index)
			}
		case reqFixture.Target == amqTargetGetUsageLimits:
			reqBody := decodeFixtureJSONMap(t, reqFixture.Body)
			isEmailRequired, _ := reqBody["isEmailRequired"].(bool)
			got, err := client.GetUsageLimits(ctx, AMQGetUsageLimitsRequest{
				Origin:          fmt.Sprint(reqBody["origin"]),
				IsEmailRequired: isEmailRequired,
			})
			if err != nil {
				t.Fatalf("fixture[%d] get usage limits failed: %v", index, err)
			}
			if got == nil {
				t.Fatalf("fixture[%d] get usage limits returned nil", index)
			}
		case reqFixture.Target == amqTargetSendTelemetryEvent:
			reqBody := decodeFixtureJSONMap(t, reqFixture.Body)
			if err := client.SendTelemetryEvent(ctx, reqBody); err != nil {
				t.Fatalf("fixture[%d] send telemetry failed: %v", index, err)
			}
		default:
			t.Fatalf("fixture[%d] unsupported non-stream target: %s", index, reqFixture.Target)
		}
	}
}

func TestAMQHTTPClient_FixtureRoundTrip_GenerateAssistantResponseStream(t *testing.T) {
	indices := []int{4, 33, 34, 40, 45, 69, 70}
	for _, index := range indices {
		reqFixture := loadFixtureRequestMessage(t, index)
		respFixture := loadFixtureResponseMessage(t, index)
		client := newFixtureClientForCase(t, reqFixture, respFixture)
		ctx := context.Background()

		stream, err := client.GenerateAssistantResponseStream(ctx, reqFixture.Body)
		if err != nil {
			t.Fatalf("fixture[%d] generate stream failed: %v", index, err)
		}

		totalEvents := 0
		for {
			ev, recvErr := stream.Recv(ctx)
			if recvErr == io.EOF {
				break
			}
			if recvErr != nil {
				t.Fatalf("fixture[%d] stream recv failed: %v", index, recvErr)
			}
			if ev == nil || ev.Event == nil {
				continue
			}
			totalEvents++
		}
		if totalEvents == 0 {
			t.Fatalf("fixture[%d] stream decoded zero events", index)
		}
		if err := stream.Close(); err != nil {
			t.Fatalf("fixture[%d] close stream failed: %v", index, err)
		}
	}
}

func TestAMQHTTPClient_XAmzUserAgentVariesByTarget(t *testing.T) {
	reqFixture := loadFixtureRequestMessage(t, 1)
	doer := &captureTargetHeadersDoer{
		t: t,
		responses: map[string]fixtureResponseMessage{
			amqTargetListAvailableModels:       loadFixtureResponseMessage(t, 1),
			amqTargetGenerateAssistantResponse: loadFixtureResponseMessage(t, 4),
		},
	}
	authHeader := strings.TrimSpace(reqFixture.Headers.Get("Authorization"))
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	client := NewHTTPClient(AMQClientConfig{
		HTTPDoer:      doer,
		TokenProvider: fixtureTokenProvider{token: token},
		Runtime: fixtureRuntimeProvider{
			region:   "us-east-1",
			proxyURL: "",
		},
		MaxAttempts: 3,
	})
	ctx := context.Background()

	if _, err := client.ListAvailableModels(ctx, AMQListAvailableModelsRequest{Origin: "KIRO_CLI"}); err != nil {
		t.Fatalf("list models failed: %v", err)
	}
	stream, err := client.GenerateAssistantResponseStream(ctx, []byte(`{"conversationState":{}}`))
	if err != nil {
		t.Fatalf("generate stream failed: %v", err)
	}
	_ = stream.Close()

	runtimeHeaders := doer.captured[amqTargetListAvailableModels]
	streamingHeaders := doer.captured[amqTargetGenerateAssistantResponse]
	if runtimeHeaders[0] == "" || runtimeHeaders[1] == "" || streamingHeaders[0] == "" || streamingHeaders[1] == "" {
		t.Fatalf("captured headers are incomplete: runtime=%v streaming=%v", runtimeHeaders, streamingHeaders)
	}
	if runtimeHeaders[1] == streamingHeaders[1] {
		t.Fatalf("x-amz-user-agent should vary by target: runtime=%s streaming=%s", runtimeHeaders[1], streamingHeaders[1])
	}
}

func TestAMQHTTPClient_FallbackToObservedAWSUserAgentShape(t *testing.T) {
	runtimeReqFixture := loadFixtureRequestMessage(t, 1)
	streamReqFixture := loadFixtureRequestMessage(t, 4)
	doer := &captureTargetHeadersDoer{
		t: t,
		responses: map[string]fixtureResponseMessage{
			amqTargetListAvailableModels:       loadFixtureResponseMessage(t, 1),
			amqTargetGenerateAssistantResponse: loadFixtureResponseMessage(t, 4),
		},
	}

	authHeader := strings.TrimSpace(runtimeReqFixture.Headers.Get("Authorization"))
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	client := NewHTTPClient(AMQClientConfig{
		HTTPDoer:      doer,
		TokenProvider: fixtureTokenProvider{token: token},
		Runtime: fixtureRuntimeProvider{
			region:   "us-east-1",
			proxyURL: "",
		},
		MaxAttempts: 3,
	})
	ctx := context.Background()

	if _, err := client.ListAvailableModels(ctx, AMQListAvailableModelsRequest{Origin: "KIRO_CLI"}); err != nil {
		t.Fatalf("list models failed: %v", err)
	}
	stream, err := client.GenerateAssistantResponseStream(ctx, []byte(`{"conversationState":{}}`))
	if err != nil {
		t.Fatalf("generate stream failed: %v", err)
	}
	_ = stream.Close()

	gotRuntime := doer.captured[amqTargetListAvailableModels]
	gotStreaming := doer.captured[amqTargetGenerateAssistantResponse]
	wantRuntimeUA := strings.TrimSpace(runtimeReqFixture.Headers.Get("User-Agent"))
	wantRuntimeXAmzUA := strings.TrimSpace(runtimeReqFixture.Headers.Get("x-amz-user-agent"))
	wantStreamingUA := strings.TrimSpace(streamReqFixture.Headers.Get("User-Agent"))
	wantStreamingXAmzUA := strings.TrimSpace(streamReqFixture.Headers.Get("x-amz-user-agent"))

	if gotRuntime[0] != wantRuntimeUA || gotRuntime[1] != wantRuntimeXAmzUA {
		t.Fatalf("runtime UA mismatch: got=%v want=[%s %s]", gotRuntime, wantRuntimeUA, wantRuntimeXAmzUA)
	}
	if gotStreaming[0] != wantStreamingUA || gotStreaming[1] != wantStreamingXAmzUA {
		t.Fatalf("streaming UA mismatch: got=%v want=[%s %s]", gotStreaming, wantStreamingUA, wantStreamingXAmzUA)
	}
}
