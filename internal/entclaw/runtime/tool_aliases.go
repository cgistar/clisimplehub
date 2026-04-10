package entclawruntime

import "strings"

var canonicalToolNames = map[string]string{
	"fs_read":      "read",
	"fs_write":     "write",
	"command_exec": "exec",
}

func normalizeToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	if canonical, ok := canonicalToolNames[trimmed]; ok {
		return canonical
	}
	return trimmed
}
