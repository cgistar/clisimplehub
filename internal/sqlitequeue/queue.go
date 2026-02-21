package sqlitequeue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var ErrClosed = errors.New("sqlite queue closed")

const (
	defaultQueueSize      = 1024
	busyRetryMaxAttempts  = 5
	busyRetryInitialDelay = 20 * time.Millisecond
	busyRetryMaxDelay     = 250 * time.Millisecond
	walCheckpointTimeout  = 5 * time.Second
)

type Manager struct {
	backend     *writeBackend
	registryKey string
	closeOnce   sync.Once
}

type registryEntry struct {
	backend *writeBackend
	refs    int
}

var (
	registryMu      sync.Mutex
	backendRegistry = map[string]*registryEntry{}
)

func Open(path string) (*Manager, error) {
	key, err := normalizePath(path)
	if err != nil {
		return nil, err
	}
	if dir := filepath.Dir(key); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create sqlite dir: %w", err)
		}
	}

	registryMu.Lock()
	defer registryMu.Unlock()

	if entry, ok := backendRegistry[key]; ok {
		entry.refs++
		return &Manager{backend: entry.backend, registryKey: key}, nil
	}

	backend, err := newWriteBackend(key)
	if err != nil {
		return nil, err
	}
	backendRegistry[key] = &registryEntry{
		backend: backend,
		refs:    1,
	}
	return &Manager{backend: backend, registryKey: key}, nil
}

func (m *Manager) DB() *sql.DB {
	if m == nil || m.backend == nil {
		return nil
	}
	return m.backend.db
}

func (m *Manager) ExecWrite(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if m == nil || m.backend == nil {
		return nil, errors.New("nil sqlite queue manager")
	}

	value, err := m.backend.submit(ctx, func(runCtx context.Context, db *sql.DB) (any, error) {
		return db.ExecContext(runCtx, query, args...)
	})
	if err != nil {
		return nil, err
	}
	result, ok := value.(sql.Result)
	if !ok {
		return nil, fmt.Errorf("unexpected sqlite write result type: %T", value)
	}
	return result, nil
}

func (m *Manager) WithTx(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
	if m == nil || m.backend == nil {
		return errors.New("nil sqlite queue manager")
	}
	if fn == nil {
		return errors.New("nil transaction function")
	}

	_, err := m.backend.submit(ctx, func(runCtx context.Context, db *sql.DB) (any, error) {
		tx, err := db.BeginTx(runCtx, nil)
		if err != nil {
			return nil, err
		}

		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()

		if err := fn(runCtx, tx); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		committed = true
		return nil, nil
	})
	return err
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}

	var err error
	m.closeOnce.Do(func() {
		err = releaseBackend(m.registryKey)
	})
	return err
}

func releaseBackend(key string) error {
	registryMu.Lock()
	entry, ok := backendRegistry[key]
	if !ok {
		registryMu.Unlock()
		return nil
	}

	entry.refs--
	if entry.refs > 0 {
		registryMu.Unlock()
		return nil
	}

	delete(backendRegistry, key)
	backend := entry.backend
	registryMu.Unlock()

	return backend.close()
}

// CloseAll forcibly closes all SQLite backends currently registered in-process.
// This is useful during application shutdown when some module forgot to release
// its manager reference.
func CloseAll() error {
	registryMu.Lock()
	entries := make([]*writeBackend, 0, len(backendRegistry))
	for key, entry := range backendRegistry {
		entries = append(entries, entry.backend)
		delete(backendRegistry, key)
	}
	registryMu.Unlock()

	var errs []string
	for _, backend := range entries {
		if backend == nil {
			continue
		}
		if err := backend.close(); err != nil {
			errs = append(errs, err.Error())
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("close all sqlite backends: %s", strings.Join(errs, "; "))
}

func normalizePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("empty sqlite path")
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	return filepath.Clean(path), nil
}

type writeFunc func(context.Context, *sql.DB) (any, error)

type writeRequest struct {
	ctx  context.Context
	fn   writeFunc
	resp chan writeResponse
}

type writeResponse struct {
	value any
	err   error
}

type writeBackend struct {
	db *sql.DB

	writes chan writeRequest
	done   chan struct{}

	enqueueMu sync.Mutex
	closed    bool
	closeOnce sync.Once
}

func newWriteBackend(path string) (*writeBackend, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := applyPragmas(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	backend := &writeBackend{
		db:     db,
		writes: make(chan writeRequest, defaultQueueSize),
		done:   make(chan struct{}),
	}
	go backend.runWorker()
	return backend, nil
}

func applyPragmas(db *sql.DB) error {
	ctx := context.Background()
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
	}
	for _, stmt := range pragmas {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply sqlite pragma %q: %w", stmt, err)
		}
	}
	return nil
}

func (b *writeBackend) runWorker() {
	for req := range b.writes {
		value, err := b.execWithRetry(req.ctx, req.fn)
		req.resp <- writeResponse{value: value, err: err}
	}
	close(b.done)
}

func (b *writeBackend) submit(ctx context.Context, fn writeFunc) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	req := writeRequest{
		ctx:  ctx,
		fn:   fn,
		resp: make(chan writeResponse, 1),
	}
	if err := b.enqueue(ctx, req); err != nil {
		return nil, err
	}

	select {
	case resp := <-req.resp:
		return resp.value, resp.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *writeBackend) enqueue(ctx context.Context, req writeRequest) error {
	b.enqueueMu.Lock()
	defer b.enqueueMu.Unlock()

	if b.closed {
		return ErrClosed
	}

	select {
	case b.writes <- req:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *writeBackend) execWithRetry(ctx context.Context, fn writeFunc) (any, error) {
	delay := busyRetryInitialDelay
	var lastErr error

	for attempt := 1; attempt <= busyRetryMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		value, err := fn(ctx, b.db)
		if err == nil {
			return value, nil
		}
		lastErr = err

		if !isBusyError(err) || attempt == busyRetryMaxAttempts {
			return nil, err
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}

		delay *= 2
		if delay > busyRetryMaxDelay {
			delay = busyRetryMaxDelay
		}
	}

	return nil, lastErr
}

func (b *writeBackend) close() error {
	var closeErr error
	b.closeOnce.Do(func() {
		b.enqueueMu.Lock()
		b.closed = true
		close(b.writes)
		b.enqueueMu.Unlock()

		<-b.done
		checkpointErr := b.checkpointWAL()
		dbCloseErr := b.db.Close()

		if checkpointErr != nil && dbCloseErr != nil {
			closeErr = fmt.Errorf("sqlite wal checkpoint failed: %v; close db failed: %w", checkpointErr, dbCloseErr)
			return
		}
		if dbCloseErr != nil {
			closeErr = dbCloseErr
			return
		}
		closeErr = checkpointErr
	})
	return closeErr
}

func (b *writeBackend) checkpointWAL() error {
	if b == nil || b.db == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), walCheckpointTimeout)
	defer cancel()

	_, err := b.execWithRetry(ctx, func(runCtx context.Context, db *sql.DB) (any, error) {
		_, execErr := db.ExecContext(runCtx, "PRAGMA wal_checkpoint(TRUNCATE)")
		return nil, execErr
	})
	if err != nil {
		return fmt.Errorf("wal_checkpoint(TRUNCATE): %w", err)
	}
	return nil
}

func isBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked")
}
