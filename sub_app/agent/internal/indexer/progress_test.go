package indexer

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBuildIndexWithProgressReports verifies that progress is reported while
// building an index and that the resulting index is still complete.
func TestBuildIndexWithProgressReports(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	var calls int
	lastFiles := 0
	var lastCurrent string
	progress := func(files, dirs int, current string) {
		calls++
		lastFiles = files
		if current != "" {
			lastCurrent = current
		}
	}

	idx, err := BuildIndexWithProgress(root, true, progress)
	if err != nil {
		t.Fatalf("build index failed: %v", err)
	}
	if len(idx) != 3 { // a.txt, sub, sub/b.txt
		t.Fatalf("expected 3 entries, got %d", len(idx))
	}
	if calls == 0 {
		t.Fatal("expected progress callbacks")
	}
	if lastFiles != 2 {
		t.Fatalf("expected 2 files counted, got %d", lastFiles)
	}
	if lastCurrent == "" {
		t.Fatal("expected a current path in progress")
	}
}

// TestBuildIndexWithoutProgressStillWorks verifies the legacy API is unchanged.
func TestBuildIndexWithoutProgressStillWorks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "only.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := BuildIndex(root, true)
	if err != nil {
		t.Fatalf("build index failed: %v", err)
	}
	if len(idx) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(idx))
	}
}
