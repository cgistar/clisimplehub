package entclawruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSkillStoreCatalogParsesFrontmatterAndScripts(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	store := NewSkillStore(dataDir)
	skillDir := filepath.Join(dataDir, "entclaw", "skills", "github-search")
	if err := os.MkdirAll(filepath.Join(skillDir, "scripts"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: github-search
description: Search GitHub repositories and similar projects.
---

# GitHub Search
`), 0o644); err != nil {
		t.Fatalf("WriteFile(SKILL.md): %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "scripts", "search.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(search.sh): %v", err)
	}

	entries, err := store.Catalog(context.Background())
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].Name != "github-search" {
		t.Fatalf("entries[0].Name = %q", entries[0].Name)
	}
	if entries[0].Description != "Search GitHub repositories and similar projects." {
		t.Fatalf("entries[0].Description = %q", entries[0].Description)
	}
	if !entries[0].HasScripts {
		t.Fatalf("entries[0].HasScripts = false, want true")
	}
	if len(entries[0].Scripts) != 1 || entries[0].Scripts[0] != "search.sh" {
		t.Fatalf("entries[0].Scripts = %#v", entries[0].Scripts)
	}
}

func TestSkillStoreCatalogFallsBackWhenFrontmatterIsMissing(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	store := NewSkillStore(dataDir)
	skillDir := filepath.Join(dataDir, "entclaw", "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(SKILL.md): %v", err)
	}

	entries, err := store.Catalog(context.Background())
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].Name != "demo" {
		t.Fatalf("entries[0].Name = %q, want demo", entries[0].Name)
	}
	if entries[0].Description != "" {
		t.Fatalf("entries[0].Description = %q, want empty", entries[0].Description)
	}
	if entries[0].HasScripts {
		t.Fatalf("entries[0].HasScripts = true, want false")
	}
	if len(entries[0].Scripts) != 0 {
		t.Fatalf("entries[0].Scripts = %#v, want empty", entries[0].Scripts)
	}
}
