package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPerformPruneConservativeUnit tests performPrune on a temporary tree.
func TestPerformPruneConservativeUnit(t *testing.T) {
	tmp, err := os.MkdirTemp("", "agent-prune-test-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmp)

	if err := os.MkdirAll(filepath.Join(tmp, "keep"), 0755); err != nil {
		t.Fatalf("mkdir keep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "keep", "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(tmp, "empty", "deep", "dir"), 0755); err != nil {
		t.Fatalf("mkdir empty tree: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(tmp, ".sync_temp", "inner"), 0755); err != nil {
		t.Fatalf("mkdir .sync_temp: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, ".git", "inner"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(oldwd)

	res, err := performPrune(&AgentConfig{}, false, nil, false)
	if err != nil {
		t.Fatalf("performPrune failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join("keep", "file.txt")); err != nil {
		t.Fatalf("expected keep/file.txt to exist: %v", err)
	}
	if _, err := os.Stat(".sync_temp"); err != nil {
		t.Fatalf("expected .sync_temp to exist: %v", err)
	}
	if _, err := os.Stat(".git"); err != nil {
		t.Fatalf("expected .git to exist: %v", err)
	}
	if len(res.Removed) == 0 {
		t.Fatalf("expected some directories to be removed, got none")
	}
}
