package entclawruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxLoopbackProbeRounds = 12

type RunResult struct {
	Response   *http.Response
	Session    *Session
	ResponseID string
	Events     []OrchestrationEvent
}

type LoopbackStatusError struct {
	StatusCode int
	Body       []byte
	Message    string
}

func (e *LoopbackStatusError) Error() string {
	if e == nil {
		return ""
	}
	if len(e.Body) == 0 {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Message, string(e.Body))
}

type Orchestrator struct {
	client   LoopbackClient
	tools    *ToolRuntime
	sessions SessionStore
	OnEvent  func(OrchestrationEvent)
}

func NewOrchestrator(client LoopbackClient, tools *ToolRuntime, sessions SessionStore) *Orchestrator {
	return &Orchestrator{
		client:   client,
		tools:    tools,
		sessions: sessions,
	}
}

func (o Orchestrator) Run(ctx context.Context, source *http.Request, task *TaskRequest) (result *RunResult, runErr error) {
	progressStarted := false
	terminalEventEmitted := false
	responseID := ""
	events := make([]OrchestrationEvent, 0, 8)
	var session *Session
	emit := func(event OrchestrationEvent) {
		progressStarted = true
		events = append(events, event)
		switch event.Type {
		case OrchestrationFailed, OrchestrationCompleted:
			terminalEventEmitted = true
		}
		o.emit(event)
	}
	emitTurn := func(turn AssistantTurn) {
		for _, part := range turn.Parts {
			switch part.Type {
			case assistantTurnPartText:
				if strings.TrimSpace(part.Text) == "" {
					continue
				}
				emit(NewAssistantMessageEvent(part.Text))
			case assistantTurnPartToolCall:
				emit(NewAssistantToolCallEvent(part.Call.ID, part.Call.Name, part.Call.Arguments))
			}
		}
	}
	defer func() {
		if runErr != nil && progressStarted && !terminalEventEmitted {
			emit(NewFailureEvent("", runErr))
		}
		if runErr != nil && progressStarted && result == nil {
			result = &RunResult{
				Session:    session,
				ResponseID: responseID,
				Events:     cloneOrchestrationEvents(events),
			}
		}
	}()

	if task == nil {
		return nil, fmt.Errorf("task is nil")
	}

	adapter := adapterForFormat(task.Format)
	if adapter == nil {
		return nil, fmt.Errorf("unsupported request format %q", task.Format)
	}
	if o.client == nil {
		o.client = HTTPClientLoopback{}
	}
	if o.tools == nil {
		return nil, fmt.Errorf("tool runtime is nil")
	}

	sessionID := resolveSessionID(task.SessionID)
	var err error

	session, err = o.sessions.LoadOrCreate(ctx, sessionID, SessionSeed{
		Channel: task.Channel,
		Format:  task.Format,
		Model:   task.Model,
	})
	if err != nil {
		return nil, err
	}

	currentBody, err := buildInitialLoopbackBody(task, o.tools)
	if err != nil {
		return nil, fmt.Errorf("build initial loopback body: %w", err)
	}
	path := adapter.LoopbackPath()
	streamProbe := task.Format == FormatResponses

	for round := 0; round < maxLoopbackProbeRounds; round++ {
		probeBody, err := adapter.WithStreamFlag(currentBody, streamProbe)
		if err != nil {
			return nil, fmt.Errorf("set probe stream flag: %w", err)
		}

		probeResponse, err := o.client.Do(ctx, source, path, probeBody)
		if err != nil {
			return nil, fmt.Errorf("probe loopback round %d: %w", round+1, err)
		}

		raw, err := readLoopbackBody(probeResponse)
		if err != nil {
			return nil, fmt.Errorf("read probe response round %d: %w", round+1, err)
		}
		if probeResponse.StatusCode >= http.StatusBadRequest {
			return nil, &LoopbackStatusError{
				StatusCode: probeResponse.StatusCode,
				Body:       append([]byte(nil), raw...),
				Message:    fmt.Sprintf("probe loopback round %d returned %s", round+1, responseStatus(probeResponse)),
			}
		}

		turn, err := adapter.ParseToolCalls(raw)
		if err != nil {
			return nil, fmt.Errorf("parse probe response round %d: %w", round+1, err)
		}
		if strings.TrimSpace(turn.ResponseID) != "" {
			responseID = strings.TrimSpace(turn.ResponseID)
		}
		emitTurn(turn)

		if streamProbe {
			if failedResponseID, err := responsesSSEFailure(raw); err != nil {
				if strings.TrimSpace(failedResponseID) != "" {
					responseID = strings.TrimSpace(failedResponseID)
				}
				emit(NewFailureEvent("", err))
				return &RunResult{
					Response:   rebuildLoopbackResponse(probeResponse, raw),
					Session:    session,
					ResponseID: responseID,
					Events:     cloneOrchestrationEvents(events),
				}, nil
			}
		}

		calls := turn.ToolCalls()
		if len(calls) == 0 {
			if streamProbe {
				emit(NewCompletionEvent())
				return &RunResult{
					Response:   rebuildLoopbackResponse(probeResponse, raw),
					Session:    session,
					ResponseID: responseID,
					Events:     cloneOrchestrationEvents(events),
				}, nil
			}

			streamBody, err := adapter.WithStreamFlag(currentBody, true)
			if err != nil {
				return nil, fmt.Errorf("set final stream flag: %w", err)
			}

			finalResponse, err := o.client.Do(ctx, source, path, streamBody)
			if err != nil {
				return nil, fmt.Errorf("final stream loopback: %w", err)
			}
			if finalResponse.StatusCode >= http.StatusBadRequest {
				raw, readErr := readLoopbackBody(finalResponse)
				if readErr != nil {
					return nil, fmt.Errorf("read final stream error response: %w", readErr)
				}
				return nil, &LoopbackStatusError{
					StatusCode: finalResponse.StatusCode,
					Body:       append([]byte(nil), raw...),
					Message:    fmt.Sprintf("final stream loopback returned %s", responseStatus(finalResponse)),
				}
			}
			emit(NewCompletionEvent())
			return &RunResult{
				Response:   finalResponse,
				Session:    session,
				ResponseID: responseID,
				Events:     cloneOrchestrationEvents(events),
			}, nil
		}

		rounds := make([]ToolRound, 0, len(calls))
		for _, call := range calls {
			emit(NewToolStartedEvent(call.ID))
			result, err := o.tools.Execute(ctx, session.SessionID, call)
			if err != nil {
				execErr := fmt.Errorf("execute tool %q: %w", call.Name, err)
				emit(NewFailureEvent(call.ID, execErr))
				return nil, execErr
			}
			emit(NewToolCompletedEvent(call.ID, result.Content, result.IsError))
			rounds = append(rounds, ToolRound{
				Call: ToolCall{
					ID:        call.ID,
					Name:      call.Name,
					Arguments: append([]byte(nil), call.Arguments...),
				},
				Result: ToolResult{
					Content: append([]byte(nil), result.Content...),
					IsError: result.IsError,
				},
			})
		}

		nextBody, err := adapter.AppendToolResults(currentBody, turn, rounds)
		if err != nil {
			return nil, fmt.Errorf("append tool results round %d: %w", round+1, err)
		}

		session.ToolHistory = append(session.ToolHistory, cloneToolHistory(rounds)...)
		if err := o.sessions.Save(ctx, session); err != nil {
			return nil, fmt.Errorf("save session tool history: %w", err)
		}
		currentBody = nextBody
	}

	return nil, fmt.Errorf("exceeded maximum loopback probe rounds (%d)", maxLoopbackProbeRounds)
}

func (o Orchestrator) emit(event OrchestrationEvent) {
	if o.OnEvent != nil {
		o.OnEvent(event)
	}
}

func (o Orchestrator) emitTurn(turn AssistantTurn) {
	for _, part := range turn.Parts {
		switch part.Type {
		case assistantTurnPartText:
			if strings.TrimSpace(part.Text) == "" {
				continue
			}
			o.emit(NewAssistantMessageEvent(part.Text))
		case assistantTurnPartToolCall:
			o.emit(NewAssistantToolCallEvent(part.Call.ID, part.Call.Name, part.Call.Arguments))
		}
	}
}

func rebuildLoopbackResponse(response *http.Response, body []byte) *http.Response {
	if response == nil {
		return nil
	}

	cloned := *response
	cloned.Header = response.Header.Clone()
	cloned.Body = io.NopCloser(bytes.NewReader(body))
	cloned.ContentLength = int64(len(body))
	return &cloned
}

func readLoopbackBody(response *http.Response) ([]byte, error) {
	if response == nil {
		return nil, fmt.Errorf("loopback response is nil")
	}
	if response.Body == nil {
		return []byte{}, nil
	}

	defer response.Body.Close()
	return io.ReadAll(response.Body)
}

func responseStatus(response *http.Response) string {
	if response == nil {
		return ""
	}
	if strings.TrimSpace(response.Status) != "" {
		return response.Status
	}
	if response.StatusCode <= 0 {
		return ""
	}
	if text := http.StatusText(response.StatusCode); text != "" {
		return fmt.Sprintf("%d %s", response.StatusCode, text)
	}
	return fmt.Sprintf("%d", response.StatusCode)
}

func cloneOrchestrationEvents(events []OrchestrationEvent) []OrchestrationEvent {
	if len(events) == 0 {
		return nil
	}

	cloned := make([]OrchestrationEvent, 0, len(events))
	for _, event := range events {
		cloned = append(cloned, OrchestrationEvent{
			Type:      event.Type,
			CallID:    event.CallID,
			Name:      event.Name,
			Text:      event.Text,
			Arguments: cloneOrchestrationRawMessage(event.Arguments),
			Output:    cloneOrchestrationRawMessage(event.Output),
			IsError:   event.IsError,
		})
	}
	return cloned
}

func resolveSessionID(sessionID string) string {
	if sessionID != "" {
		return sessionID
	}

	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err == nil {
		return "entclaw-session-" + hex.EncodeToString(buf)
	}
	return fmt.Sprintf("entclaw-session-%p", &buf)
}
