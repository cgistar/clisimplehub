package entclawruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
)

const maxLoopbackProbeRounds = 12

type RunResult struct {
	Response *http.Response
	Session  *Session
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
}

func NewOrchestrator(client LoopbackClient, tools *ToolRuntime, sessions SessionStore) *Orchestrator {
	return &Orchestrator{
		client:   client,
		tools:    tools,
		sessions: sessions,
	}
}

func (o Orchestrator) Run(ctx context.Context, source *http.Request, task *TaskRequest) (*RunResult, error) {
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

	session, err := o.sessions.LoadOrCreate(ctx, sessionID, SessionSeed{
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
				Message:    fmt.Sprintf("probe loopback round %d returned %s", round+1, probeResponse.Status),
			}
		}

		turn, err := adapter.ParseToolCalls(raw)
		if err != nil {
			return nil, fmt.Errorf("parse probe response round %d: %w", round+1, err)
		}

		calls := turn.ToolCalls()
		if len(calls) == 0 {
			if streamProbe {
				return &RunResult{
					Response: rebuildLoopbackResponse(probeResponse, raw),
					Session:  session,
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
					Message:    fmt.Sprintf("final stream loopback returned %s", finalResponse.Status),
				}
			}
			return &RunResult{
				Response: finalResponse,
				Session:  session,
			}, nil
		}

		rounds := make([]ToolRound, 0, len(calls))
		for _, call := range calls {
			result, err := o.tools.Execute(ctx, session.SessionID, call)
			if err != nil {
				return nil, fmt.Errorf("execute tool %q: %w", call.Name, err)
			}
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
