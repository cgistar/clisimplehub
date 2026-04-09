package entclawruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type SkillStore struct {
	root string
}

func NewSkillStore(dataDir string) SkillStore {
	return SkillStore{
		root: filepath.Join(dataDir, "entclaw", "skills"),
	}
}

func (s SkillStore) Write(ctx context.Context, name, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	path, err := s.skillPath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create skill directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write skill file: %w", err)
	}
	return nil
}

func (s SkillStore) Read(ctx context.Context, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	path, err := s.skillPath(name)
	if err != nil {
		return "", err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (s SkillStore) List(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list skills: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func (s SkillStore) Delete(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	dir, err := s.skillDir(name)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("delete skill directory: %w", err)
	}
	return nil
}

func (s SkillStore) skillPath(name string) (string, error) {
	dir, err := s.skillDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "SKILL.md"), nil
}

func (s SkillStore) SkillPath(name string) (string, error) {
	return s.skillPath(name)
}

func (s SkillStore) ScriptDir(name string) (string, error) {
	dir, err := s.skillDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "scripts"), nil
}

func (s SkillStore) ResolveScriptPath(name, script string) (string, string, error) {
	skillDir, err := s.resolveExistingSkillDir(name)
	if err != nil {
		return "", "", err
	}

	scriptName := strings.TrimSpace(script)
	if scriptName == "" || scriptName == "." {
		return "", "", fmt.Errorf("script is required")
	}

	scriptDir := filepath.Join(skillDir, "scripts")
	candidate := filepath.Clean(filepath.Join(scriptDir, scriptName))
	relative, err := filepath.Rel(scriptDir, candidate)
	if err != nil {
		return "", "", fmt.Errorf("resolve relative path: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path escapes guarded root")
	}

	info, err := os.Stat(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("script %q not found for skill %q", scriptName, strings.TrimSpace(name))
		}
		return "", "", fmt.Errorf("stat script file: %w", err)
	}
	if info.IsDir() {
		return "", "", fmt.Errorf("script %q not found for skill %q", scriptName, strings.TrimSpace(name))
	}

	resolved, err := NewPathGuard(scriptDir).Resolve(scriptName)
	if err != nil {
		return "", "", err
	}
	return resolved, skillDir, nil
}

func (s SkillStore) resolveExistingSkillDir(name string) (string, error) {
	skillDir, err := s.skillDir(name)
	if err != nil {
		return "", err
	}

	skillPath := filepath.Join(skillDir, "SKILL.md")
	info, err := os.Stat(skillPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("skill %q not found", strings.TrimSpace(name))
		}
		return "", fmt.Errorf("stat skill file: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("skill %q not found", strings.TrimSpace(name))
	}
	return skillDir, nil
}

func (s SkillStore) skillDir(name string) (string, error) {
	skillName := strings.TrimSpace(name)
	if skillName == "" {
		return "", fmt.Errorf("skill name is required")
	}
	if skillName == "." || skillName == ".." {
		return "", fmt.Errorf("invalid skill name %q", name)
	}
	if filepath.Base(skillName) != skillName {
		return "", fmt.Errorf("invalid skill name %q", name)
	}
	return filepath.Join(s.root, skillName), nil
}
