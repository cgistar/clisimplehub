package entclawruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type PathGuard struct {
	root string
}

func NewPathGuard(root string) PathGuard {
	return PathGuard{
		root: root,
	}
}

func (g PathGuard) Resolve(rel string) (string, error) {
	root := strings.TrimSpace(g.root)
	if root == "" {
		return "", fmt.Errorf("path guard root is required")
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}

	trimmed := strings.TrimSpace(rel)
	if trimmed == "" || trimmed == "." {
		return absRoot, nil
	}
	if filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("absolute paths are not allowed")
	}

	target := filepath.Clean(filepath.Join(absRoot, trimmed))
	relative, err := filepath.Rel(absRoot, target)
	if err != nil {
		return "", fmt.Errorf("resolve relative path: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes guarded root")
	}

	resolvedRoot, err := resolveGuardPath(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve guard root: %w", err)
	}
	resolvedTarget, err := resolveGuardPath(target)
	if err != nil {
		return "", fmt.Errorf("resolve target path: %w", err)
	}
	resolvedRelative, err := filepath.Rel(resolvedRoot, resolvedTarget)
	if err != nil {
		return "", fmt.Errorf("resolve guarded target: %w", err)
	}
	if resolvedRelative == ".." || strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes guarded root via symlink")
	}
	return target, nil
}

func resolveGuardPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}

	parent := filepath.Dir(path)
	if parent == path {
		return path, nil
	}
	resolvedParent, parentErr := resolveGuardPath(parent)
	if parentErr != nil {
		return "", parentErr
	}
	return filepath.Join(resolvedParent, filepath.Base(path)), nil
}
