package codexplugin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	codex "clisimplehub/internal/codex"
	codexBackend "clisimplehub/internal/codex/backend"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/executor"
	"clisimplehub/internal/plugin"

	"github.com/tidwall/gjson"
)

const defaultCodexImageGenerationModel = "gpt-image-2"

type codexUsageReporter struct {
	account        *codexShared.CodexAccount
	store          codexShared.CodexAccountStore
	observer       plugin.CodexUsageObserver
	executorType   string
	source         string
	model          string
	requestedModel string
	path           string
	reasoning      string
	serviceTier    string
	requestBody    []byte
	requestedAt    time.Time

	ttftMu    sync.Mutex
	ttftStart time.Time
	ttft      time.Duration
	ttftSet   bool

	usageMu             sync.Mutex
	observedTokens      *executor.TokenUsage
	responseServiceTier string
	additionalModels    []plugin.CodexAdditionalModelUsage
	usageRefresh        func()

	once sync.Once
}

type codexUsageReporterRequest struct {
	Account        *codexShared.CodexAccount
	ExecutorType   string
	Source         string
	Model          string
	RequestedModel string
	Path           string
	Body           []byte
}

func newCodexUsageReporter(ctx context.Context, service *CodexService, req codexUsageReporterRequest) *codexUsageReporter {
	model := codexBackend.BaseModelName(req.Model)
	if model == "" {
		model = codexBackend.BaseModelName(gjson.GetBytes(req.Body, "model").String())
	}
	requestedModel := strings.TrimSpace(req.RequestedModel)
	if requestedModel == "" {
		requestedModel = strings.TrimSpace(req.Model)
	}
	var store codexShared.CodexAccountStore
	if service != nil {
		store = service.getAccountStore()
	}
	return &codexUsageReporter{
		account:        req.Account,
		store:          store,
		observer:       plugin.CodexUsageObserverFromContext(ctx),
		executorType:   strings.TrimSpace(req.ExecutorType),
		source:         strings.TrimSpace(req.Source),
		model:          model,
		requestedModel: requestedModel,
		path:           strings.TrimSpace(req.Path),
		reasoning:      strings.TrimSpace(gjson.GetBytes(req.Body, "reasoning.effort").String()),
		serviceTier:    strings.TrimSpace(gjson.GetBytes(req.Body, "service_tier").String()),
		requestBody:    append([]byte(nil), req.Body...),
		requestedAt:    time.Now(),
	}
}

func newCodexWebsocketUsageReporter(ctx context.Context, service *CodexService, req codexWebsocketTurnRequest) *codexUsageReporter {
	requestedModel := strings.TrimSpace(gjson.GetBytes(req.OriginalBody, "model").String())
	return newCodexUsageReporter(ctx, service, codexUsageReporterRequest{
		Account:        req.Account,
		ExecutorType:   "CodexWebsocketsExecutor",
		Source:         codexBackend.SourceCodex,
		Model:          req.Model,
		RequestedModel: requestedModel,
		Path:           req.Path,
		Body:           req.Body,
	})
}

func newCodexHTTPUsageReporter(ctx context.Context, service *CodexService, account *codexShared.CodexAccount, req *executor.UpstreamRequest) *codexUsageReporter {
	if req == nil {
		return newCodexUsageReporter(ctx, service, codexUsageReporterRequest{Account: account, ExecutorType: "CodexExecutor"})
	}
	path := strings.TrimSpace(req.OriginalPath)
	if path == "" {
		path = strings.TrimSpace(req.TargetPath)
	}
	requestedModel := strings.TrimSpace(req.RequestModel)
	source := strings.TrimSpace(req.TargetInterfaceType)
	if req.TransformContext != nil && len(req.TransformContext.OriginalRequestBody) > 0 {
		if originalModel := strings.TrimSpace(gjson.GetBytes(req.TransformContext.OriginalRequestBody, "model").String()); originalModel != "" {
			requestedModel = originalModel
		}
	}
	if req.TransformContext != nil && req.TransformContext.Metadata != nil {
		if sourceType, _ := req.TransformContext.Metadata["source_type"].(string); strings.TrimSpace(sourceType) != "" {
			source = strings.TrimSpace(sourceType)
		}
	}
	return newCodexUsageReporter(ctx, service, codexUsageReporterRequest{
		Account:        account,
		ExecutorType:   "CodexExecutor",
		Source:         source,
		Model:          req.RequestModel,
		RequestedModel: requestedModel,
		Path:           path,
		Body:           req.Body,
	})
}

func (r *codexUsageReporter) StartResponseTTFT() {
	if r == nil {
		return
	}
	r.ttftMu.Lock()
	if !r.ttftSet && r.ttftStart.IsZero() {
		r.ttftStart = time.Now()
	}
	r.ttftMu.Unlock()
}

func (r *codexUsageReporter) SetTranslatedRequest(body []byte) {
	if r == nil || len(body) == 0 {
		return
	}
	r.requestBody = append(r.requestBody[:0], body...)
	if model := codexBackend.BaseModelName(gjson.GetBytes(body, "model").String()); model != "" {
		r.model = model
	}
	if reasoning := strings.TrimSpace(gjson.GetBytes(body, "reasoning.effort").String()); reasoning != "" {
		r.reasoning = reasoning
	}
	if serviceTier := strings.TrimSpace(gjson.GetBytes(body, "service_tier").String()); serviceTier != "" {
		r.serviceTier = serviceTier
	}
}

func (r *codexUsageReporter) SetUsageRefresh(refresh func()) {
	if r == nil {
		return
	}
	r.usageRefresh = refresh
}

func (r *codexUsageReporter) ObserveHeaders(headers http.Header) {
	if r == nil || r.account == nil {
		return
	}
	snapshot := extractCodexUsageHeaders(headers)
	if snapshot == nil {
		return
	}
	if pool := codex.GetPool(); pool != nil {
		pool.UpdateUsageSnapshot(r.account.ID, snapshot)
	}
}

func (r *codexUsageReporter) MarkFirstResponseByte() {
	if r == nil {
		return
	}
	r.ttftMu.Lock()
	if !r.ttftSet && !r.ttftStart.IsZero() {
		r.ttft = time.Since(r.ttftStart)
		r.ttftSet = true
		r.ttftStart = time.Time{}
	}
	r.ttftMu.Unlock()
}

func (r *codexUsageReporter) TrackHTTPClient(client *http.Client) *http.Client {
	if r == nil || client == nil {
		return client
	}
	tracked := *client
	transport := tracked.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	tracked.Transport = codexUsageTTFTRoundTripper{base: transport, reporter: r}
	return &tracked
}

type codexUsageTTFTRoundTripper struct {
	base     http.RoundTripper
	reporter *codexUsageReporter
}

func (t codexUsageTTFTRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	t.reporter.StartResponseTTFT()
	resp, err := t.base.RoundTrip(req)
	if resp != nil && resp.Body != nil {
		resp.Body = &codexUsageTTFTReadCloser{ReadCloser: resp.Body, reporter: t.reporter}
	}
	return resp, err
}

type codexUsageTTFTReadCloser struct {
	io.ReadCloser
	reporter *codexUsageReporter
}

func (r *codexUsageTTFTReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 {
		r.reporter.MarkFirstResponseByte()
	}
	return n, err
}

func (r *codexUsageReporter) PublishSuccess(payload []byte) {
	r.ObservePayload(payload)
	r.publish(http.StatusOK, nil, payload)
}

func (r *codexUsageReporter) PublishFailure(statusCode int, err error) {
	if err == nil {
		err = errors.New("codex usage reporter received an unspecified failure")
	}
	if statusCode <= 0 {
		statusCode = codexWebsocketErrorStatus(err)
	}
	r.publish(statusCode, err, nil)
}

func (r *codexUsageReporter) ObservePayload(payload []byte) {
	if r == nil || len(payload) == 0 {
		return
	}
	tokens := extractTokensFromBody(payload)
	responseTier := strings.TrimSpace(gjson.GetBytes(payload, "response.service_tier").String())
	if responseTier == "" {
		responseTier = strings.TrimSpace(gjson.GetBytes(payload, "service_tier").String())
	}
	additional := extractCodexAdditionalModelUsage(r.requestBody, payload)
	if tokens == nil && responseTier == "" && len(additional) == 0 {
		return
	}

	r.usageMu.Lock()
	if tokens != nil {
		if r.observedTokens == nil {
			copyTokens := *tokens
			r.observedTokens = &copyTokens
		} else {
			mergeCodexTokenUsage(r.observedTokens, tokens)
		}
	}
	if responseTier != "" {
		r.responseServiceTier = responseTier
	}
	if len(additional) > 0 {
		r.additionalModels = additional
	}
	r.usageMu.Unlock()
}

func (r *codexUsageReporter) publish(statusCode int, publishErr error, payload []byte) {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.ttftMu.Lock()
		ttft := r.ttft
		r.ttftMu.Unlock()
		r.usageMu.Lock()
		observedTokens := codexPluginTokens(r.observedTokens)
		responseServiceTier := r.responseServiceTier
		additionalModels := append([]plugin.CodexAdditionalModelUsage(nil), r.additionalModels...)
		r.usageMu.Unlock()

		event := plugin.CodexUsageRecord{
			Provider:            "codex",
			ExecutorType:        r.executorType,
			Source:              r.source,
			Model:               r.model,
			RequestedModel:      r.requestedModel,
			Path:                r.path,
			RequestedAt:         r.requestedAt,
			Duration:            time.Since(r.requestedAt),
			TTFT:                ttft,
			StatusCode:          statusCode,
			Status:              "success",
			ReasoningEffort:     r.reasoning,
			ServiceTier:         r.serviceTier,
			ResponseServiceTier: responseServiceTier,
		}
		if r.account != nil {
			event.AccountID = strings.TrimSpace(r.account.ID)
			event.UpstreamAccountID = strings.TrimSpace(r.account.AccountID)
			event.AccountEmail = strings.TrimSpace(r.account.Email)
			event.AuthType = "oauth"
			event.PlanType = strings.TrimSpace(r.account.PlanType)
		}
		if publishErr != nil {
			event.Status = "error"
			if errors.Is(publishErr, context.Canceled) {
				event.Status = "canceled"
			}
			event.Error = publishErr.Error()
		} else {
			event.Tokens = observedTokens
			event.AdditionalModels = additionalModels
		}

		r.insertAccountStats(event)
		if r.observer != nil {
			r.observer(event)
		}
		if publishErr == nil && r.usageRefresh != nil {
			r.usageRefresh()
		}
	})
}

func mergeCodexTokenUsage(current, observed *executor.TokenUsage) {
	if current == nil || observed == nil {
		return
	}
	if observed.InputTokens > current.InputTokens {
		current.InputTokens = observed.InputTokens
	}
	if observed.OutputTokens > current.OutputTokens {
		current.OutputTokens = observed.OutputTokens
	}
	if observed.TotalTokens > current.TotalTokens {
		current.TotalTokens = observed.TotalTokens
	}
	if observed.CachedCreate > current.CachedCreate {
		current.CachedCreate = observed.CachedCreate
	}
	if observed.CachedRead > current.CachedRead {
		current.CachedRead = observed.CachedRead
	}
	if observed.Reasoning > current.Reasoning {
		current.Reasoning = observed.Reasoning
	}
}

func (r *codexUsageReporter) insertAccountStats(event plugin.CodexUsageRecord) {
	if r == nil || r.store == nil || r.account == nil {
		return
	}
	now := time.Now()
	stat := &codexShared.CodexAccountStat{
		AccountID:           event.AccountID,
		AccountEmail:        event.AccountEmail,
		Model:               event.Model,
		Date:                now.Format("2006-01-02"),
		Hour:                now.Hour(),
		StatusCode:          event.StatusCode,
		Status:              event.Status,
		ErrorType:           event.Error,
		DurationMs:          event.Duration.Milliseconds(),
		TTFTMs:              event.TTFT.Milliseconds(),
		ExecutorType:        event.ExecutorType,
		RequestedModel:      event.RequestedModel,
		Source:              event.Source,
		ReasoningEffort:     event.ReasoningEffort,
		ServiceTier:         event.ServiceTier,
		ResponseServiceTier: event.ResponseServiceTier,
		RequestPath:         event.Path,
	}
	applyCodexAccountStatTokens(stat, codexExecutorTokens(event.Tokens))
	insertCodexAccountStatAsync(r.store, stat)

	for _, additional := range event.AdditionalModels {
		additionalStat := *stat
		additionalStat.Model = additional.Model
		additionalStat.AdditionalModel = true
		additionalStat.InputTokens = 0
		additionalStat.OutputTokens = 0
		additionalStat.TotalTokens = 0
		additionalStat.CachedTokens = 0
		additionalStat.CacheReadTokens = 0
		additionalStat.CacheCreationTokens = 0
		additionalStat.ReasoningTokens = 0
		applyCodexAccountStatTokens(&additionalStat, codexExecutorTokens(additional.Tokens))
		insertCodexAccountStatAsync(r.store, &additionalStat)
	}
}

func codexPluginTokens(tokens *executor.TokenUsage) plugin.CodexTokenUsage {
	if tokens == nil {
		return plugin.CodexTokenUsage{}
	}
	return plugin.CodexTokenUsage{
		InputTokens:  tokens.InputTokens,
		OutputTokens: tokens.OutputTokens,
		TotalTokens:  tokenUsageTotal(tokens),
		CachedCreate: tokens.CachedCreate,
		CachedRead:   tokens.CachedRead,
		Reasoning:    tokens.Reasoning,
	}
}

func codexExecutorTokens(tokens plugin.CodexTokenUsage) *executor.TokenUsage {
	return &executor.TokenUsage{
		InputTokens:  tokens.InputTokens,
		OutputTokens: tokens.OutputTokens,
		TotalTokens:  tokens.TotalTokens,
		CachedCreate: tokens.CachedCreate,
		CachedRead:   tokens.CachedRead,
		Reasoning:    tokens.Reasoning,
	}
}

func extractCodexAdditionalModelUsage(requestBody, payload []byte) []plugin.CodexAdditionalModelUsage {
	usageNode := gjson.GetBytes(payload, "response.tool_usage.image_gen")
	if !usageNode.Exists() || !usageNode.IsObject() {
		return nil
	}
	wrapper := append([]byte(`{"usage":`), []byte(usageNode.Raw)...)
	wrapper = append(wrapper, '}')
	tokens := extractTokensFromBody(wrapper)
	if tokens == nil {
		return nil
	}
	return []plugin.CodexAdditionalModelUsage{{
		Model:  codexImageGenerationModel(requestBody),
		Tokens: codexPluginTokens(tokens),
	}}
}

type codexUsageReadCloser struct {
	upstream io.ReadCloser
	reporter *codexUsageReporter
	lineBuf  []byte
}

func (r *codexUsageReporter) TrackSSEStream(upstream io.ReadCloser) io.ReadCloser {
	if r == nil || upstream == nil {
		return upstream
	}
	return &codexUsageReadCloser{upstream: upstream, reporter: r}
}

func (r *codexUsageReadCloser) Read(p []byte) (int, error) {
	n, err := r.upstream.Read(p)
	if n > 0 {
		r.reporter.MarkFirstResponseByte()
		r.feed(p[:n])
	}
	if err != nil {
		r.finish(err)
	}
	return n, err
}

func (r *codexUsageReadCloser) Close() error {
	err := r.upstream.Close()
	r.finish(err)
	return err
}

func (r *codexUsageReadCloser) feed(chunk []byte) {
	r.lineBuf = append(r.lineBuf, chunk...)
	for {
		index := bytes.IndexByte(r.lineBuf, '\n')
		if index < 0 {
			return
		}
		line := bytes.TrimSpace(r.lineBuf[:index])
		r.lineBuf = r.lineBuf[index+1:]
		r.observeLine(line)
	}
}

func (r *codexUsageReadCloser) observeLine(line []byte) {
	payload := bytes.TrimSpace(line)
	if bytes.HasPrefix(payload, []byte("data:")) {
		payload = bytes.TrimSpace(payload[len("data:"):])
	}
	if len(payload) == 0 {
		return
	}
	if bytes.Equal(payload, []byte("[DONE]")) {
		r.reporter.PublishSuccess(nil)
		return
	}
	if !gjson.ValidBytes(payload) {
		return
	}
	r.reporter.ObservePayload(payload)
	switch strings.TrimSpace(gjson.GetBytes(payload, "type").String()) {
	case "response.completed", "response.done":
		r.reporter.PublishSuccess(payload)
	case "error":
		r.reporter.PublishFailure(http.StatusBadGateway, newCodexWebsocketUpstreamError(payload))
	}
}

func (r *codexUsageReadCloser) finish(readErr error) {
	if len(bytes.TrimSpace(r.lineBuf)) > 0 {
		r.observeLine(bytes.TrimSpace(r.lineBuf))
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		r.reporter.PublishFailure(codexWebsocketErrorStatus(readErr), readErr)
		return
	}
	r.reporter.PublishSuccess(nil)
}

func codexImageGenerationModel(body []byte) string {
	for _, tool := range gjson.GetBytes(body, "tools").Array() {
		if strings.TrimSpace(tool.Get("type").String()) != "image_generation" {
			continue
		}
		if model := strings.TrimSpace(tool.Get("model").String()); model != "" {
			return model
		}
	}
	return defaultCodexImageGenerationModel
}
