package kiroplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"clisimplehub/internal/executor"
	kiroapi "clisimplehub/internal/kiro"
	amq "clisimplehub/internal/kiro/amq"
	kiro_claude "clisimplehub/internal/kiro/claude"
	"clisimplehub/internal/kiro/streaming"
	transformShared "clisimplehub/internal/transformer/shared"
)

type amqTokenProvider struct {
	tr *kiro_claude.Transformer
}

func (p *amqTokenProvider) AccessToken(ctx context.Context) (string, error) {
	if p == nil || p.tr == nil {
		return "", fmt.Errorf("nil amq token provider")
	}
	return p.tr.GetAccessToken()
}

func (p *amqTokenProvider) RefreshAccessToken(ctx context.Context) (string, error) {
	if p == nil || p.tr == nil {
		return "", fmt.Errorf("nil amq token provider")
	}
	if err := p.tr.ForceRefreshKiroToken(); err != nil {
		return "", err
	}
	return p.tr.GetAccessToken()
}

type amqRuntimeProvider struct {
	tr *kiro_claude.Transformer
}

func (p *amqRuntimeProvider) Region() string {
	if p == nil || p.tr == nil {
		return ""
	}
	return p.tr.GetRegion()
}

func (p *amqRuntimeProvider) ProxyURL() string {
	if p == nil || p.tr == nil {
		return ""
	}
	return p.tr.KiroProxyURL()
}

func parseThinkingEnabled(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	var req map[string]any
	if err := json.Unmarshal(raw, &req); err != nil {
		return false
	}
	thinking := req["thinking"]
	switch v := thinking.(type) {
	case map[string]any:
		t := strings.ToLower(strings.TrimSpace(fmt.Sprint(v["type"])))
		return t == "enabled" || t == "adaptive"
	case string:
		t := strings.ToLower(strings.TrimSpace(v))
		return t == "enabled" || t == "adaptive"
	default:
		return false
	}
}

func amqTargetURLByRegion(region string) string {
	return "https://" + kiroapi.KiroQHost(region) + "/"
}

func (s *KiroService) tryForwardViaAMQ(
	ctx context.Context,
	w http.ResponseWriter,
	tr *kiro_claude.Transformer,
	upstreamModel string,
	isStreaming bool,
	originalRequestRawJSON []byte,
	requestRawJSON []byte,
) *executor.ForwardResult {
	if tr == nil || !tr.UseAmqHTTPClient() {
		return nil
	}

	result := &executor.ForwardResult{TargetURL: amqTargetURLByRegion(tr.GetRegion())}
	debugLogger := executor.DebugLoggerFromContext(ctx)

	client := s.getOrCreateAMQClient(tr)

	stream, err := client.GenerateAssistantResponseStream(ctx, requestRawJSON)
	if err != nil {
		if httpErr, ok := err.(*amq.AMQHTTPError); ok {
			if debugLogger != nil {
				debugLogger.Log("AMQ 上游返回错误: status=%d", httpErr.StatusCode)
				debugLogger.SetSection("UpstreamResponseBody", string(httpErr.Body))
				debugLogger.SetRawSection("UpstreamResponseRaw", httpErr.Body)
			}
			result.StatusCode = httpErr.StatusCode
			result.Headers = http.Header{"Content-Type": []string{httpErr.ContentType}}
			result.Body = httpErr.Body
			_, canFailover := handleKiroErrorStatus(httpErr.StatusCode, httpErr.Body, tr)
			if canFailover {
				if out := s.tryFailoverRetry(ctx, tr, originalRequestRawJSON, upstreamModel, isStreaming, w); out != nil {
					return out
				}
			}
			return result
		}
		result.Error = fmt.Errorf("amq request failed: %w", err)
		return result
	}
	defer stream.Close()

	statusCode := stream.StatusCode()
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	result.StatusCode = statusCode
	result.Headers = stream.Header()

	var flusher http.Flusher
	if isStreaming {
		for key, values := range result.Headers {
			switch strings.ToLower(key) {
			case "content-length", "content-encoding", "content-type":
				continue
			}
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
		w.Header().Set("Content-Type", tr.OutputContentType(true))
		w.WriteHeader(statusCode)

		var ok bool
		flusher, ok = w.(http.Flusher)
		if !ok {
			result.Error = fmt.Errorf("response writer does not support flushing")
			return result
		}
	}

	bridge := amq.NewAMQSSEBridge(amq.AMQSSEBridgeConfig{
		Model:           upstreamModel,
		InputTokens:     kiro_claude.EstimateClaudeInputTokens(originalRequestRawJSON),
		ThinkingEnabled: parseThinkingEnabled(originalRequestRawJSON),
		Buffered:        isStreaming && kiro_claude.GetCachedBufferedStream(),
	})

	var capture strings.Builder
	var capturedEvents []*streaming.SseEvent

	for {
		env, recvErr := stream.Recv(ctx)
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			result.Error = recvErr
			break
		}

		if isStreaming {
			outs, convErr := bridge.Consume(env)
			if convErr != nil {
				result.Error = convErr
				break
			}
			for _, out := range outs {
				if out == "" {
					continue
				}
				if _, writeErr := w.Write([]byte(out)); writeErr != nil {
					result.Error = context.Canceled
					break
				}
				capture.WriteString(out)
				flusher.Flush()
			}
			if result.Error != nil {
				break
			}
		} else {
			evs, convErr := bridge.ConsumeEvents(env)
			if convErr != nil {
				result.Error = convErr
				break
			}
			capturedEvents = append(capturedEvents, evs...)
		}
	}

	if isStreaming {
		finalOuts, finalErr := bridge.Finalize()
		if finalErr != nil && result.Error == nil {
			result.Error = finalErr
		}
		for _, out := range finalOuts {
			if out == "" {
				continue
			}
			if _, writeErr := w.Write([]byte(out)); writeErr != nil {
				result.Error = context.Canceled
				break
			}
			capture.WriteString(out)
			flusher.Flush()
		}
		result.Streamed = true
		result.ResponseStream = capture.String()

		if debugLogger != nil {
			debugLogger.Log("AMQ 流式响应完成，长度: %d bytes", len(result.ResponseStream))
			debugLogger.SetSection("TransformedResponse", result.ResponseStream)
		}
	} else if result.Error == nil {
		finalEvents, finalErr := bridge.FinalizeEvents()
		if finalErr != nil {
			result.Error = finalErr
		} else {
			capturedEvents = append(capturedEvents, finalEvents...)
			converted := kiro_claude.BuildMessageFromSSEEvents(capturedEvents, upstreamModel)
			body, marshalErr := transformShared.MarshalNoEscapeHTML(converted)
			if marshalErr != nil {
				result.Error = marshalErr
			} else {
				result.Body = body
				result.Headers.Set("Content-Type", tr.OutputContentType(false))
				result.Headers.Del("Content-Length")
				result.Headers.Del("Content-Encoding")
				if debugLogger != nil {
					debugLogger.Log("AMQ 非流式聚合完成，长度: %d bytes", len(body))
					debugLogger.SetSection("TransformedResponse", string(body))
				}
			}
		}
	}

	inTok, outTok := bridge.TokenUsage()
	if inTok != 0 || outTok != 0 {
		result.Tokens = &executor.TokenUsage{InputTokens: int64(inTok), OutputTokens: int64(outTok)}
	}

	return result
}
