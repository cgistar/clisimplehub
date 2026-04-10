package entclawruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ApplyPatchRequest struct {
	Input string `json:"input"`
}

type ApplyPatchSummary struct {
	Added    []string `json:"added,omitempty"`
	Modified []string `json:"modified,omitempty"`
	Deleted  []string `json:"deleted,omitempty"`
}

type applyPatchRequestInput struct {
	Input *string `json:"input"`
}

type applyPatchOperation struct {
	Kind       string
	Path       string
	OldContent string
	NewContent string
}

type applyPatchPlannedFile struct {
	AbsPath string
	Exists  bool
	Mode    os.FileMode
	Content string
}

const (
	applyPatchKindAdd    = "add"
	applyPatchKindUpdate = "update"
	applyPatchKindDelete = "delete"
)

func decodeApplyPatchRequest(raw json.RawMessage) (ApplyPatchRequest, error) {
	var input applyPatchRequestInput
	if err := json.Unmarshal(rawJSONObjectOrEmpty(raw), &input); err != nil {
		return ApplyPatchRequest{}, err
	}
	if input.Input == nil || *input.Input == "" {
		return ApplyPatchRequest{}, fmt.Errorf("input is required")
	}
	return ApplyPatchRequest{Input: *input.Input}, nil
}

func (r *ToolRuntime) executeApplyPatch(raw json.RawMessage) (ToolResult, error) {
	input, err := decodeApplyPatchRequest(raw)
	if err != nil {
		return errorToolResult(err), nil
	}

	ops, err := parseApplyPatchOperations(input.Input)
	if err != nil {
		return errorToolResult(err), nil
	}

	summary, err := r.applyPatchOperations(ops)
	if err != nil {
		return errorToolResult(err), nil
	}

	return marshalToolPayload(summary, nil)
}

func parseApplyPatchOperations(input string) ([]applyPatchOperation, error) {
	lines := splitPatchDocumentLines(input)
	if len(lines) < 2 {
		return nil, fmt.Errorf("apply_patch input must include begin and end markers")
	}
	if lines[0] != "*** Begin Patch" {
		return nil, fmt.Errorf("apply_patch input must start with *** Begin Patch")
	}
	if lines[len(lines)-1] != "*** End Patch" {
		return nil, fmt.Errorf("apply_patch input must end with *** End Patch")
	}

	ops := make([]applyPatchOperation, 0)
	for i := 1; i < len(lines)-1; {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))
			if path == "" {
				return nil, fmt.Errorf("apply_patch add file path is required")
			}
			i++

			contentLines := make([]string, 0)
			for i < len(lines)-1 && !isApplyPatchOperationHeader(lines[i]) {
				if !strings.HasPrefix(lines[i], "+") {
					return nil, fmt.Errorf("apply_patch add file only supports added lines in v1")
				}
				contentLines = append(contentLines, strings.TrimPrefix(lines[i], "+"))
				i++
			}

			ops = append(ops, applyPatchOperation{
				Kind:       applyPatchKindAdd,
				Path:       path,
				NewContent: applyPatchLinesToContent(contentLines),
			})
		case strings.HasPrefix(line, "*** Delete File: "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))
			if path == "" {
				return nil, fmt.Errorf("apply_patch delete file path is required")
			}
			ops = append(ops, applyPatchOperation{
				Kind: applyPatchKindDelete,
				Path: path,
			})
			i++
		case strings.HasPrefix(line, "*** Update File: "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))
			if path == "" {
				return nil, fmt.Errorf("apply_patch update file path is required")
			}
			i++
			if i >= len(lines)-1 {
				return nil, fmt.Errorf("apply_patch update requires a single @@ hunk in v1")
			}
			if strings.HasPrefix(lines[i], "*** Move to: ") {
				return nil, fmt.Errorf("apply_patch move operations are unsupported in v1")
			}
			if lines[i] != "@@" {
				return nil, fmt.Errorf("apply_patch update requires a single @@ hunk in v1")
			}
			i++

			oldLines := make([]string, 0)
			newLines := make([]string, 0)
			for i < len(lines)-1 && !isApplyPatchOperationHeader(lines[i]) {
				switch {
				case lines[i] == "@@":
					return nil, fmt.Errorf("apply_patch only supports a single full-file update hunk in v1")
				case strings.HasPrefix(lines[i], " "):
					return nil, fmt.Errorf("apply_patch update hunks only support full-file replacement in v1")
				case strings.HasPrefix(lines[i], "-"):
					oldLines = append(oldLines, strings.TrimPrefix(lines[i], "-"))
				case strings.HasPrefix(lines[i], "+"):
					newLines = append(newLines, strings.TrimPrefix(lines[i], "+"))
				default:
					return nil, fmt.Errorf("apply_patch update contains unsupported line %q", lines[i])
				}
				i++
			}

			ops = append(ops, applyPatchOperation{
				Kind:       applyPatchKindUpdate,
				Path:       path,
				OldContent: applyPatchLinesToContent(oldLines),
				NewContent: applyPatchLinesToContent(newLines),
			})
		case strings.HasPrefix(line, "*** Move to: "):
			return nil, fmt.Errorf("apply_patch move operations are unsupported in v1")
		default:
			return nil, fmt.Errorf("apply_patch contains unsupported construct %q", line)
		}
	}

	if len(ops) == 0 {
		return nil, fmt.Errorf("apply_patch input must contain at least one file operation")
	}
	return ops, nil
}

func (r *ToolRuntime) applyPatchOperations(ops []applyPatchOperation) (ApplyPatchSummary, error) {
	if err := os.MkdirAll(r.guard.root, 0o755); err != nil {
		return ApplyPatchSummary{}, fmt.Errorf("create entclaw root: %w", err)
	}

	planned := make([]applyPatchPlannedFile, len(ops))
	seenTargets := make(map[string]struct{}, len(ops))
	for i, op := range ops {
		file, err := r.loadApplyPatchFile(op.Path)
		if err != nil {
			return ApplyPatchSummary{}, err
		}
		if _, exists := seenTargets[file.AbsPath]; exists {
			return ApplyPatchSummary{}, fmt.Errorf("apply_patch multiple operations on the same path are unsupported in v1")
		}
		seenTargets[file.AbsPath] = struct{}{}

		switch op.Kind {
		case applyPatchKindAdd:
			if file.Exists {
				return ApplyPatchSummary{}, fmt.Errorf("apply_patch add file target already exists: %s", op.Path)
			}
		case applyPatchKindDelete:
			if !file.Exists {
				return ApplyPatchSummary{}, fmt.Errorf("apply_patch delete file target does not exist: %s", op.Path)
			}
		case applyPatchKindUpdate:
			if !file.Exists {
				return ApplyPatchSummary{}, fmt.Errorf("apply_patch update file target does not exist: %s", op.Path)
			}
			if file.Content != op.OldContent {
				return ApplyPatchSummary{}, fmt.Errorf("apply_patch update hunk does not match file contents: %s", op.Path)
			}
		default:
			return ApplyPatchSummary{}, fmt.Errorf("apply_patch contains unsupported operation %q", op.Kind)
		}

		planned[i] = file
	}

	summary := ApplyPatchSummary{
		Added:    make([]string, 0),
		Modified: make([]string, 0),
		Deleted:  make([]string, 0),
	}
	for i, op := range ops {
		file := planned[i]
		switch op.Kind {
		case applyPatchKindAdd:
			if err := os.MkdirAll(filepath.Dir(file.AbsPath), 0o755); err != nil {
				return ApplyPatchSummary{}, fmt.Errorf("create parent directory: %w", err)
			}
			if err := os.WriteFile(file.AbsPath, []byte(op.NewContent), 0o644); err != nil {
				return ApplyPatchSummary{}, err
			}
			summary.Added = append(summary.Added, op.Path)
		case applyPatchKindUpdate:
			if err := os.MkdirAll(filepath.Dir(file.AbsPath), 0o755); err != nil {
				return ApplyPatchSummary{}, fmt.Errorf("create parent directory: %w", err)
			}
			if err := os.WriteFile(file.AbsPath, []byte(op.NewContent), file.Mode); err != nil {
				return ApplyPatchSummary{}, err
			}
			summary.Modified = append(summary.Modified, op.Path)
		case applyPatchKindDelete:
			if err := os.Remove(file.AbsPath); err != nil {
				return ApplyPatchSummary{}, err
			}
			summary.Deleted = append(summary.Deleted, op.Path)
		}
	}

	return summary, nil
}

func (r *ToolRuntime) loadApplyPatchFile(path string) (applyPatchPlannedFile, error) {
	resolved, err := r.guard.Resolve(path)
	if err != nil {
		return applyPatchPlannedFile{}, err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return applyPatchPlannedFile{
				AbsPath: resolved,
				Exists:  false,
				Mode:    0o644,
			}, nil
		}
		return applyPatchPlannedFile{}, err
	}
	if info.IsDir() {
		return applyPatchPlannedFile{}, fmt.Errorf("apply_patch only supports regular files: %s", path)
	}

	body, err := os.ReadFile(resolved)
	if err != nil {
		return applyPatchPlannedFile{}, err
	}
	return applyPatchPlannedFile{
		AbsPath: resolved,
		Exists:  true,
		Mode:    info.Mode().Perm(),
		Content: string(body),
	}, nil
}

func splitPatchDocumentLines(input string) []string {
	normalized := strings.ReplaceAll(input, "\r\n", "\n")
	normalized = strings.TrimSuffix(normalized, "\n")
	return strings.Split(normalized, "\n")
}

func isApplyPatchOperationHeader(line string) bool {
	return strings.HasPrefix(line, "*** Add File: ") ||
		strings.HasPrefix(line, "*** Delete File: ") ||
		strings.HasPrefix(line, "*** Update File: ") ||
		strings.HasPrefix(line, "*** Move to: ")
}

func applyPatchLinesToContent(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
