package entclawruntime

import (
	"encoding/json"
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

type editRequestInput struct {
	Path  *string                `json:"path"`
	Edits []editReplacementInput `json:"edits"`
}

type editReplacementInput struct {
	OldText *string `json:"oldText"`
	NewText *string `json:"newText"`
}

func decodeEditRequest(raw json.RawMessage) (editRequest, error) {
	var input editRequestInput
	if err := json.Unmarshal(rawJSONObjectOrEmpty(raw), &input); err != nil {
		return editRequest{}, err
	}

	if input.Path == nil || strings.TrimSpace(*input.Path) == "" {
		return editRequest{}, fmt.Errorf("path is required")
	}
	if len(input.Edits) == 0 {
		return editRequest{}, fmt.Errorf("edits is required")
	}

	request := editRequest{
		Path:  *input.Path,
		Edits: make([]EditReplacement, 0, len(input.Edits)),
	}
	for i, edit := range input.Edits {
		if edit.OldText == nil || *edit.OldText == "" {
			return editRequest{}, fmt.Errorf("edits[%d].oldText is required", i)
		}
		if edit.NewText == nil {
			return editRequest{}, fmt.Errorf("edits[%d].newText is required", i)
		}
		request.Edits = append(request.Edits, EditReplacement{
			OldText: *edit.OldText,
			NewText: *edit.NewText,
		})
	}
	return request, nil
}

func applyEdits(content string, edits []EditReplacement) (string, error) {
	updated := content
	for _, edit := range edits {
		if edit.OldText == "" {
			return "", fmt.Errorf("oldText is required")
		}
		if !strings.Contains(updated, edit.OldText) {
			return "", fmt.Errorf("could not find exact text in file")
		}
		updated = strings.Replace(updated, edit.OldText, edit.NewText, 1)
	}
	return updated, nil
}
