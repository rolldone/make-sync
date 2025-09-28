package indexer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSimpleIgnoreCache_AuthoritativeDevsyncIgnores(t *testing.T) {
	// create temp dir
	dir := t.TempDir()
	// create a .sync_ignore in a nested dir that would normally ignore foo.txt
	nested := filepath.Join(dir, "nested")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	syncIgnorePath := filepath.Join(nested, ".sync_ignore")
	if err := os.WriteFile(syncIgnorePath, []byte("foo.txt\n"), 0644); err != nil {
		t.Fatalf("write .sync_ignore: %v", err)
	}

	// create devsync config in root .sync_temp with ignores that do NOT include foo.txt
	cfgDir := filepath.Join(dir, ".sync_temp")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatalf("mkdir .sync_temp: %v", err)
	}
	cfg := map[string]map[string][]string{"devsync": {"ignores": {}}}
	// explicit empty list means no ignores from devsync
	cfg["devsync"]["ignores"] = []string{"**/*.bak"}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), data, 0644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	// create the file that would normally be ignored
	foo := filepath.Join(nested, "foo.txt")
	if err := os.WriteFile(foo, []byte("hello"), 0644); err != nil {
		t.Fatalf("write foo: %v", err)
	}

	// create cache and check behavior
	ic := NewSimpleIgnoreCache(dir)

	// Because devsync.ignores exists and is authoritative, we expect foo.txt NOT to be ignored
	if ic.Match(foo, false) {
		t.Fatalf("expected foo.txt not to be ignored when devsync.ignores is authoritative")
	}
}
