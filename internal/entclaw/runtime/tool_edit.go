package entclawruntime

import (
	"fmt"
	"strings"
)

type EditReplacement struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

type editRequest struct {
	Path  string            `json:"path"`
	Edits []EditReplacement `json:"edits"`
}

func applyEdits(content string, edits []EditReplacement) (string, error) {
	updated := content
	for _, edit := range edits {
		if strings.TrimSpace(edit.OldText) == "" {
			return "", fmt.Errorf("oldText is required")
		}
		if !strings.Contains(updated, edit.OldText) {
			return "", fmt.Errorf("could not find exact text in file")
		}
		updated = strings.Replace(updated, edit.OldText, edit.NewText, 1)
	}
	return updated, nil
}
