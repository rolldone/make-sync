package syncdata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"make-sync/internal/config"
)

// localModeConfig builds a config that selects the local transport against the
// given target directory.
func localModeConfig(t *testing.T, target string) *config.Config {
	t.Helper()
	cfg := &config.Config{ProjectName: "local-test"}
	cfg.Devsync.Auth.Mode = config.AuthModeLocal
	cfg.Devsync.Auth.LocalTargetPath = target
	cfg.Devsync.OSTarget = "windows" // deliberately "wrong": local mode must use the host OS
	cfg.LocalPath = t.TempDir()
	return cfg
}

// fakeAgent writes a shell script that mimics the agent's indexing side effect
// (creating .sync_temp/indexing_files.db in its working directory) so the local
// indexing path can be exercised without building the real agent.
func fakeAgent(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-agent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
		t.Fatalf("failed to write fake agent: %v", err)
	}
	return path
}

func TestRunLocalAgentIndexingCreatesTargetDB(t *testing.T) {
	target := t.TempDir()
	cfg := localModeConfig(t, target)

	agent := fakeAgent(t, "mkdir -p .sync_temp && echo indexed > .sync_temp/indexing_files.db && echo index-done")

	out, err := runLocalAgentIndexing(cfg, agent, false, nil)
	if err != nil {
		t.Fatalf("runLocalAgentIndexing failed: %v", err)
	}
	if !strings.Contains(out, "index-done") {
		t.Fatalf("expected agent output to be streamed, got %q", out)
	}

	dbPath := filepath.Join(target, ".sync_temp", "indexing_files.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected target index DB at %s: %v", dbPath, err)
	}

	// config.json must be written into the target .sync_temp so the agent honours
	// the same ignore rules in both modes.
	cfgPath := filepath.Join(target, ".sync_temp", "config.json")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("expected agent config at %s: %v", cfgPath, err)
	}
}

func TestRunLocalAgentIndexingPassesFlags(t *testing.T) {
	target := t.TempDir()
	cfg := localModeConfig(t, target)

	agent := fakeAgent(t, "echo \"args:$*\" && mkdir -p .sync_temp")

	out, err := runLocalAgentIndexing(cfg, agent, true, []string{"a", "b"})
	if err != nil {
		t.Fatalf("runLocalAgentIndexing failed: %v", err)
	}
	if !strings.Contains(out, "indexing --bypass-ignore --manual-transfer a,b") {
		t.Fatalf("expected indexing flags to be forwarded, got %q", out)
	}
}

func TestRunLocalAgentIndexingMissingTarget(t *testing.T) {
	cfg := localModeConfig(t, filepath.Join(t.TempDir(), "does-not-exist"))
	agent := fakeAgent(t, "true")
	if _, err := runLocalAgentIndexing(cfg, agent, false, nil); err == nil {
		t.Fatal("expected error for missing local target")
	}
}

// TestDownloadIndexDBLocalMode verifies that the target index DB is resolved
// locally (copy marked as target-side) instead of being downloaded over SFTP.
func TestDownloadIndexDBLocalMode(t *testing.T) {
	target := t.TempDir()
	cfg := localModeConfig(t, target)

	if err := os.MkdirAll(filepath.Join(target, ".sync_temp"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dbPath := filepath.Join(target, ".sync_temp", "indexing_files.db")
	if err := os.WriteFile(dbPath, []byte("db-content"), 0644); err != nil {
		t.Fatalf("write db: %v", err)
	}

	// Destination is the project (different from the target): DB is stored with a
	// target marker.
	dest := t.TempDir()
	got, err := DownloadIndexDB(cfg, dest)
	if err != nil {
		t.Fatalf("DownloadIndexDB failed: %v", err)
	}
	want := filepath.Join(dest, ".sync_temp", "indexing_files_target.db")
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read copied DB: %v", err)
	}
	if string(data) != "db-content" {
		t.Fatalf("unexpected DB content %q", string(data))
	}

	// Destination is the target root itself: the DB is returned in place.
	inPlace, err := DownloadIndexDB(cfg, target)
	if err != nil {
		t.Fatalf("DownloadIndexDB(in place) failed: %v", err)
	}
	if inPlace != dbPath {
		t.Fatalf("expected %s, got %s", dbPath, inPlace)
	}
}

func TestPruneRemoteEmptyDirsLocalMode(t *testing.T) {
	target := t.TempDir()
	cfg := localModeConfig(t, target)

	emptyDir := filepath.Join(target, "sub", "empty")
	if err := os.MkdirAll(emptyDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	keepDir := filepath.Join(target, "sub", "keep")
	if err := os.MkdirAll(keepDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(keepDir, "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	deleted := pruneRemoteEmptyDirs(nil, cfg, []string{"sub"})
	if deleted == 0 {
		t.Fatal("expected the empty directory to be pruned")
	}
	if _, err := os.Stat(emptyDir); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed", emptyDir)
	}
	if _, err := os.Stat(filepath.Join(keepDir, "f.txt")); err != nil {
		t.Fatalf("expected non-empty directory to be kept: %v", err)
	}
}
