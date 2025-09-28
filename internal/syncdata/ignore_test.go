package syncdata

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIgnoreCacheSimple verifies that basic patterns and default ignores work.
func TestIgnoreCacheSimple(t *testing.T) {
	dir := t.TempDir()

	// create a .sync_ignore at root that ignores *.tmp
	rootIgnore := filepath.Join(dir, ".sync_ignore")
	os.WriteFile(rootIgnore, []byte("*.tmp\n# comment\n"), 0644)

	ic := NewIgnoreCache(dir)

	// file that should be ignored
	p1 := filepath.Join(dir, "foo.tmp")
	if !ic.Match(p1, false) {
		t.Fatalf("expected %s to match ignore pattern", p1)
	}

	// default ignore should match
	p2 := filepath.Join(dir, ".sync_temp")
	if !ic.Match(p2, true) {
		t.Fatalf("expected %s to be ignored by default", p2)
	}
}

// TestIgnoreCacheCascade verifies that child .sync_ignore can override parent via negation
func TestIgnoreCacheCascade(t *testing.T) {
	root := t.TempDir()
	// parent ignore ignores *.log
	os.WriteFile(filepath.Join(root, ".sync_ignore"), []byte("*.log\n"), 0644)

	// create child dir and a child .sync_ignore that negates a specific file
	child := filepath.Join(root, "sub")
	os.MkdirAll(child, 0755)
	os.WriteFile(filepath.Join(child, ".sync_ignore"), []byte("!keep.log\n"), 0644)

	ic := NewIgnoreCache(root)

	// file that should be ignored by parent rule
	f1 := filepath.Join(child, "other.log")
	// debug: log ancestor match results
	t.Logf("ancestors check for %s:\n", f1)
	if !ic.Match(f1, false) {
		t.Fatalf("expected %s to be ignored by parent rule; debug cache: %+v", f1, ic)
	}

	// file that should be un-ignored by child's negation
	f2 := filepath.Join(child, "keep.log")
	if ic.Match(f2, false) {
		t.Fatalf("expected %s to NOT be ignored due to child negation", f2)
	}
}
