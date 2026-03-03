package converters

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

func generateUUID() string {
	return uuid.New().String()
}

// truncateUTF8Safe 安全截断 UTF-8 字符串到指定字符数，不会在多字节字符中间截断
func truncateUTF8Safe(s string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}

	charCount := 0
	for i := range s {
		if charCount >= maxChars {
			return s[:i]
		}
		charCount++
	}
	return s
}

// marshalNoEscapeHTML marshals v to JSON without escaping HTML characters (<, >, &).
func marshalNoEscapeHTML(v any) ([]byte, error) {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	s := buf.String()
	if len(s) > 0 && s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	return []byte(s), nil
}
