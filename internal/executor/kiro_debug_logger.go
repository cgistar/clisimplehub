package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type kiroDebugLogger struct {
	dir string
}

func sanitizePathSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		return "unknown"
	}
	return out
}

func newKiroDebugLogger(ctx context.Context, baseDir string) (*kiroDebugLogger, error) {
	ts := time.Now().Format("20060102150405")
	reqID := sanitizePathSegment(RequestIDFromContext(ctx))

	root := baseDir
	if strings.TrimSpace(root) == "" {
		root = "debug_logs"
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create debug log root %q: %w", root, err)
	}

	tryDirs := []string{
		filepath.Join(root, ts),
		filepath.Join(root, ts+"_"+reqID),
	}
	for i := 0; i < 20; i++ {
		tryDirs = append(tryDirs, filepath.Join(root, fmt.Sprintf("%s_%s_%d", ts, reqID, i+1)))
	}

	var created string
	for _, d := range tryDirs {
		if err := os.Mkdir(d, 0o755); err == nil {
			created = d
			break
		} else if os.IsExist(err) {
			continue
		}
	}

	if created == "" {
		return nil, fmt.Errorf("failed to create debug log directory under %q", root)
	}

	return &kiroDebugLogger{dir: created}, nil
}

func (l *kiroDebugLogger) writeFile(name string, data []byte) error {
	if l == nil || strings.TrimSpace(l.dir) == "" {
		return fmt.Errorf("debug logger not initialized")
	}
	path := filepath.Join(l.dir, name)
	return os.WriteFile(path, data, 0o600)
}

func (l *kiroDebugLogger) dirPath() string {
	if l == nil {
		return ""
	}
	return l.dir
}
