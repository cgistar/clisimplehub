package entclawruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

type ProcessRequest struct {
	Action    string `json:"action"`
	SessionID string `json:"sessionId"`
	Data      string `json:"data"`
	Offset    int    `json:"offset"`
	Limit     int    `json:"limit"`
	Timeout   int    `json:"timeout"`
}

type ProcessStore struct {
	mu      sync.Mutex
	nextID  uint64
	entries map[string]*managedProcess
}

type managedProcess struct {
	id      string
	command string
	workDir string

	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan struct{}

	stdout *lockedBuffer
	stderr *lockedBuffer
	log    *lockedBuffer

	mu         sync.Mutex
	running    bool
	exitCode   int
	errorText  string
	startedAt  time.Time
	finishedAt time.Time
}

type ProcessSnapshot struct {
	SessionID  string    `json:"sessionId"`
	Command    string    `json:"command"`
	WorkDir    string    `json:"workdir"`
	Running    bool      `json:"running"`
	ExitCode   int       `json:"exitCode"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func NewProcessStore() *ProcessStore {
	return &ProcessStore{
		entries: make(map[string]*managedProcess),
	}
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (s *ProcessStore) Start(command string, request CommandRequest) (ProcessSnapshot, error) {
	if len(request.Args) == 0 {
		return ProcessSnapshot{}, fmt.Errorf("background command args are required")
	}

	runCtx := context.Background()
	var cancel context.CancelFunc
	if request.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(runCtx, request.Timeout)
	} else {
		runCtx, cancel = context.WithCancel(runCtx)
	}

	cmd := exec.CommandContext(runCtx, request.Args[0], request.Args[1:]...)
	cmd.Dir = request.WorkDir
	if len(request.Env) > 0 {
		cmd.Env = mergeCommandEnv(request.Env)
	}

	stdout := &lockedBuffer{}
	stderr := &lockedBuffer{}
	log := &lockedBuffer{}
	cmd.Stdout = io.MultiWriter(stdout, log)
	cmd.Stderr = io.MultiWriter(stderr, log)

	if err := cmd.Start(); err != nil {
		cancel()
		return ProcessSnapshot{}, err
	}

	s.mu.Lock()
	s.nextID++
	sessionID := fmt.Sprintf("proc_%d", s.nextID)
	process := &managedProcess{
		id:        sessionID,
		command:   command,
		workDir:   request.WorkDir,
		cmd:       cmd,
		cancel:    cancel,
		done:      make(chan struct{}),
		stdout:    stdout,
		stderr:    stderr,
		log:       log,
		running:   true,
		exitCode:  0,
		startedAt: time.Now(),
	}
	s.entries[sessionID] = process
	s.mu.Unlock()

	go process.wait()

	return process.snapshot(), nil
}

func (p *managedProcess) wait() {
	err := p.cmd.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()

	if err == nil {
		p.exitCode = 0
	} else {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			p.exitCode = exitErr.ExitCode()
		} else {
			p.exitCode = -1
		}
		p.errorText = err.Error()
	}
	p.running = false
	p.finishedAt = time.Now()
	close(p.done)
	p.cancel()
}

func (p *managedProcess) snapshot() ProcessSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return ProcessSnapshot{
		SessionID:  p.id,
		Command:    p.command,
		WorkDir:    p.workDir,
		Running:    p.running,
		ExitCode:   p.exitCode,
		Error:      p.errorText,
		StartedAt:  p.startedAt,
		FinishedAt: p.finishedAt,
	}
}

func (s *ProcessStore) get(sessionID string) (*managedProcess, error) {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil, fmt.Errorf("sessionId is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	process, ok := s.entries[trimmed]
	if !ok {
		return nil, fmt.Errorf("process session %q not found", trimmed)
	}
	return process, nil
}

func (s *ProcessStore) List() []ProcessSnapshot {
	s.mu.Lock()
	processes := make([]*managedProcess, 0, len(s.entries))
	for _, process := range s.entries {
		processes = append(processes, process)
	}
	s.mu.Unlock()

	snapshots := make([]ProcessSnapshot, 0, len(processes))
	for _, process := range processes {
		snapshots = append(snapshots, process.snapshot())
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].SessionID < snapshots[j].SessionID
	})
	return snapshots
}

func (s *ProcessStore) Poll(sessionID string, wait time.Duration) (ProcessSnapshot, error) {
	process, err := s.get(sessionID)
	if err != nil {
		return ProcessSnapshot{}, err
	}
	if wait > 0 {
		snapshot := process.snapshot()
		if snapshot.Running {
			select {
			case <-process.done:
			case <-time.After(wait):
			}
		}
	}
	return process.snapshot(), nil
}

func (s *ProcessStore) Log(sessionID string, offset, limit int) (ProcessSnapshot, string, error) {
	process, err := s.get(sessionID)
	if err != nil {
		return ProcessSnapshot{}, "", err
	}
	return process.snapshot(), sliceReadContent(process.log.String(), offset, limit), nil
}

func (s *ProcessStore) Kill(sessionID string) (ProcessSnapshot, bool, error) {
	process, err := s.get(sessionID)
	if err != nil {
		return ProcessSnapshot{}, false, err
	}

	snapshot := process.snapshot()
	if !snapshot.Running {
		return snapshot, false, nil
	}
	if err := process.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return ProcessSnapshot{}, false, err
	}
	select {
	case <-process.done:
	case <-time.After(500 * time.Millisecond):
	}
	return process.snapshot(), true, nil
}

func (r *ToolRuntime) executeProcess(raw json.RawMessage) (ToolResult, error) {
	var input ProcessRequest
	if err := json.Unmarshal(rawJSONObjectOrEmpty(raw), &input); err != nil {
		return ToolResult{}, err
	}

	switch strings.TrimSpace(input.Action) {
	case "list":
		return marshalToolPayload(map[string]any{
			"processes": r.process.List(),
		}, nil)
	case "poll":
		snapshot, err := r.process.Poll(input.SessionID, durationFromMillis(input.Timeout))
		if err != nil {
			return errorToolResult(err), nil
		}
		return marshalToolPayload(snapshot, nil)
	case "log":
		snapshot, content, err := r.process.Log(input.SessionID, input.Offset, input.Limit)
		if err != nil {
			return errorToolResult(err), nil
		}
		return marshalToolPayload(map[string]any{
			"sessionId": snapshot.SessionID,
			"content":   content,
		}, nil)
	case "kill":
		snapshot, killed, err := r.process.Kill(input.SessionID)
		if err != nil {
			return errorToolResult(err), nil
		}
		return marshalToolPayload(map[string]any{
			"sessionId": snapshot.SessionID,
			"killed":    killed,
			"running":   snapshot.Running,
			"exitCode":  snapshot.ExitCode,
		}, nil)
	default:
		return errorToolResult(fmt.Errorf("unsupported process action %q", input.Action)), nil
	}
}
