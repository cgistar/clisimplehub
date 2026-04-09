package entclawruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Session struct {
	SessionID   string            `json:"sessionId"`
	Channel     Channel           `json:"channel"`
	Format      RequestFormat     `json:"requestFormat"`
	Model       string            `json:"model"`
	Messages    []json.RawMessage `json:"messages"`
	ToolHistory []ToolRound       `json:"toolHistory"`
	Status      string            `json:"status"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

type SessionSeed struct {
	Channel     Channel
	Format      RequestFormat
	Model       string
	Messages    []json.RawMessage
	ToolHistory []ToolRound
	Status      string
}

type SessionStore struct {
	root string
}

func NewSessionStore(dataDir string) SessionStore {
	return SessionStore{
		root: filepath.Join(dataDir, "entclaw", "sessions"),
	}
}

func (s SessionStore) LoadOrCreate(ctx context.Context, sessionID string, seed SessionSeed) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	path, err := s.sessionPath(sessionID)
	if err != nil {
		return nil, err
	}

	session, err := s.read(path)
	if err == nil {
		return session, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	now := time.Now().UTC()
	session = &Session{
		SessionID:   strings.TrimSpace(sessionID),
		Channel:     seed.Channel,
		Format:      seed.Format,
		Model:       strings.TrimSpace(seed.Model),
		Messages:    cloneMessages(seed.Messages),
		ToolHistory: cloneToolHistory(seed.ToolHistory),
		Status:      normalizeSessionStatus(seed.Status),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.persist(path, session, false); err != nil {
		return nil, err
	}
	return session, nil
}

func (s SessionStore) Save(ctx context.Context, session *Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if session == nil {
		return fmt.Errorf("session is nil")
	}

	path, err := s.sessionPath(session.SessionID)
	if err != nil {
		return err
	}
	return s.persist(path, session, true)
}

func (s SessionStore) read(path string) (*Session, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var session Session
	if err := json.Unmarshal(body, &session); err != nil {
		return nil, fmt.Errorf("decode session file: %w", err)
	}
	session.Messages = cloneMessages(session.Messages)
	session.ToolHistory = cloneToolHistory(session.ToolHistory)
	return &session, nil
}

func (s SessionStore) sessionPath(sessionID string) (string, error) {
	name := strings.TrimSpace(sessionID)
	if name == "" {
		return "", fmt.Errorf("session_id is required")
	}
	if filepath.Base(name) != name {
		return "", fmt.Errorf("invalid session_id %q", sessionID)
	}
	return filepath.Join(s.root, name+".json"), nil
}

func cloneMessages(messages []json.RawMessage) []json.RawMessage {
	if len(messages) == 0 {
		return []json.RawMessage{}
	}

	cloned := make([]json.RawMessage, len(messages))
	for i, message := range messages {
		cloned[i] = append(json.RawMessage(nil), message...)
	}
	return cloned
}

func cloneToolHistory(history []ToolRound) []ToolRound {
	if len(history) == 0 {
		return []ToolRound{}
	}

	cloned := make([]ToolRound, len(history))
	for i, round := range history {
		cloned[i] = ToolRound{
			Call: ToolCall{
				ID:        round.Call.ID,
				Name:      round.Call.Name,
				Arguments: append(json.RawMessage(nil), round.Call.Arguments...),
			},
			Result: ToolResult{
				Content: append(json.RawMessage(nil), round.Result.Content...),
				IsError: round.Result.IsError,
			},
		}
	}
	return cloned
}

func (s SessionStore) persist(path string, session *Session, touchUpdatedAt bool) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return fmt.Errorf("create session root: %w", err)
	}

	copySession := *session
	copySession.SessionID = strings.TrimSpace(copySession.SessionID)
	copySession.Model = strings.TrimSpace(copySession.Model)
	copySession.Status = normalizeSessionStatus(copySession.Status)
	copySession.Messages = cloneMessages(copySession.Messages)
	copySession.ToolHistory = cloneToolHistory(copySession.ToolHistory)

	now := time.Now().UTC()
	if copySession.CreatedAt.IsZero() {
		copySession.CreatedAt = now
	}
	if touchUpdatedAt || copySession.UpdatedAt.IsZero() {
		copySession.UpdatedAt = now
	}

	body, err := json.MarshalIndent(copySession, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	body = append(body, '\n')

	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("write session file: %w", err)
	}

	*session = copySession
	return nil
}

func normalizeSessionStatus(status string) string {
	if trimmed := strings.TrimSpace(status); trimmed != "" {
		return trimmed
	}
	return "active"
}
