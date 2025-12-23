package shared

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func DecodeJSONMap(raw []byte) (map[string]any, error) {
	var out map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func StringFromAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func IntFromAny(v any) int {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
	case float64:
		return int(n)
	case float32:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case int32:
		return int(n)
	}
	return 0
}

func SSEDataPayload(line []byte) ([]byte, bool) {
	if !bytes.HasPrefix(line, []byte("data:")) {
		return nil, false
	}
	return bytes.TrimSpace(line[5:]), true
}

func SSEEvent(event string, data any) string {
	b, _ := json.Marshal(data)
	return "event: " + event + "\n" + "data: " + string(b) + "\n\n"
}

func BuildClaudeSystemText(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []any:
		var b strings.Builder
		for _, item := range s {
			m, _ := item.(map[string]any)
			if m == nil {
				continue
			}
			if strings.TrimSpace(StringFromAny(m["type"])) != "text" {
				continue
			}
			t := StringFromAny(m["text"])
			if strings.TrimSpace(t) == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(t)
		}
		return b.String()
	default:
		return ""
	}
}

func RandomSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func StringListFromAny(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

