package entclawruntime

import "strings"

const (
	toolNameRead  = "read"
	toolNameWrite = "write"
	toolNameExec  = "exec"
)

var canonicalToolNames = map[string]string{
	"fs_read":      toolNameRead,
	"fs_write":     toolNameWrite,
	"command_exec": toolNameExec,
}

func normalizeToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	if canonical, ok := canonicalToolNames[trimmed]; ok {
		return canonical
	}
	return trimmed
}
