package claude

import (
	"strconv"
	"strings"
	"sync"

	kiroShared "clisimplehub/internal/kiro/shared"
)

type KiroImage struct {
	Format string          `json:"format"`
	Source KiroImageSource `json:"source"`
}

type KiroImageSource struct {
	Bytes string `json:"bytes"`
}

func NewKiroImageFromBase64(format, base64Data string) KiroImage {
	return KiroImage{
		Format: format,
		Source: KiroImageSource{
			Bytes: base64Data,
		},
	}
}

type ToolUse struct {
	Name      string                 `json:"name"`
	ToolUseID string                 `json:"toolUseId"`
	Input     map[string]interface{} `json:"input"`
}

type ToolResult struct {
	Content   []ToolResultContent `json:"content"`
	Status    string              `json:"status"`
	ToolUseID string              `json:"toolUseId"`
	IsError   bool                `json:"isError,omitempty"`
}

type ToolResultContent struct {
	Text string `json:"text"`
}

var (
	cachedModelMapping map[string]string
	cachedModelMu      sync.RWMutex

	cachedBufferedStream bool
	cachedBufferedMu     sync.RWMutex
)

func SetCachedModelMapping(m map[string]string) {
	cachedModelMu.Lock()
	defer cachedModelMu.Unlock()
	if len(m) == 0 {
		cachedModelMapping = nil
		return
	}
	clone := make(map[string]string, len(m))
	for k, v := range m {
		clone[k] = v
	}
	cachedModelMapping = clone
}

func SetCachedBufferedStream(v bool) {
	cachedBufferedMu.Lock()
	defer cachedBufferedMu.Unlock()
	cachedBufferedStream = v
}

func GetCachedBufferedStream() bool {
	cachedBufferedMu.RLock()
	defer cachedBufferedMu.RUnlock()
	return cachedBufferedStream
}

func inferKiroModelID(claudeModel string) string {
	parts := strings.Split(claudeModel, "-")
	if len(parts) > 0 {
		last := parts[len(parts)-1]
		if len(last) == 8 {
			if _, err := strconv.Atoi(last); err == nil {
				parts = parts[:len(parts)-1]
			}
		}
	}

	if len(parts) < 4 {
		return claudeModel
	}

	minor := parts[len(parts)-1]
	if _, err := strconv.Atoi(minor); err != nil {
		return claudeModel
	}
	major := parts[len(parts)-2]
	if _, err := strconv.Atoi(major); err != nil {
		return claudeModel
	}

	prefix := strings.Join(parts[:len(parts)-2], "-")
	return prefix + "-" + major + "." + minor
}

func GetKiroModelID(claudeModel string) string {
	cachedModelMu.RLock()
	mapping := cachedModelMapping
	cachedModelMu.RUnlock()
	if mapping == nil {
		mapping = kiroShared.DefaultKiroModelMapping()
	}
	if kiroModel, ok := mapping[claudeModel]; ok {
		return kiroModel
	}
	return inferKiroModelID(claudeModel)
}
