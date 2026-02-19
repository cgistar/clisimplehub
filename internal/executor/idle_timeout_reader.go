package executor

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// StreamIdleTimeoutError indicates no data was read within the idle timeout.
type StreamIdleTimeoutError struct {
	IdleTimeout time.Duration
	Err         error
}

func (e *StreamIdleTimeoutError) Error() string {
	return fmt.Sprintf("stream read idle timeout: no data for %s", e.IdleTimeout)
}

func (e *StreamIdleTimeoutError) Unwrap() error { return e.Err }

func (e *StreamIdleTimeoutError) Timeout() bool { return true }

func IsStreamIdleTimeout(err error) bool {
	var t *StreamIdleTimeoutError
	return errors.As(err, &t)
}

// IdleTimeoutReader wraps an io.Reader and aborts blocked reads when no data
// arrives within idleTimeout. It closes abortCloser (typically resp.Body) to
// interrupt the blocking Read call.
//
// Thread safety: time.AfterFunc runs its callback in a separate goroutine.
// A readID is used to ensure a stale timer callback (from Read N) does not
// interfere with a subsequent Read N+1.
type IdleTimeoutReader struct {
	inner       io.Reader
	abortCloser io.Closer
	idleTimeout time.Duration

	mu           sync.Mutex
	nextReadID   uint64
	activeReadID uint64
	readTimedOut bool
}

func NewIdleTimeoutReader(reader io.Reader, abortCloser io.Closer, idleTimeout time.Duration) io.Reader {
	if reader == nil || idleTimeout <= 0 {
		return reader
	}
	if abortCloser == nil {
		if c, ok := reader.(io.Closer); ok {
			abortCloser = c
		}
	}
	return &IdleTimeoutReader{
		inner:       reader,
		abortCloser: abortCloser,
		idleTimeout: idleTimeout,
	}
}

func (r *IdleTimeoutReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return r.inner.Read(p)
	}

	readID := r.beginRead()
	timer := time.AfterFunc(r.idleTimeout, func() { r.onTimeout(readID) })
	n, err := r.inner.Read(p)
	timer.Stop()

	if timedOut := r.finishRead(readID); timedOut && n == 0 {
		return 0, &StreamIdleTimeoutError{IdleTimeout: r.idleTimeout, Err: err}
	}
	return n, err
}

func (r *IdleTimeoutReader) beginRead() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextReadID++
	r.activeReadID = r.nextReadID
	r.readTimedOut = false
	return r.activeReadID
}

func (r *IdleTimeoutReader) onTimeout(readID uint64) {
	r.mu.Lock()
	if r.activeReadID != readID {
		r.mu.Unlock()
		return
	}
	r.readTimedOut = true
	closer := r.abortCloser
	r.mu.Unlock()

	if closer != nil {
		_ = closer.Close()
	}
}

func (r *IdleTimeoutReader) finishRead(readID uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	timedOut := r.activeReadID == readID && r.readTimedOut
	if r.activeReadID == readID {
		r.activeReadID = 0
		r.readTimedOut = false
	}
	return timedOut
}
