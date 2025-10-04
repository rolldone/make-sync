package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigFromTestFolderUsingDotEnv(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	// repo root is two levels up from internal/config
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
	// target test dir inside repo root
	testDir := filepath.Join(repoRoot, "test")

	// ensure .env exists in test dir with MAKE_SYNC_HOST (create default if missing)
	envPath := filepath.Join(testDir, ".env")
	expected := ""
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		// create default
		defaultIP := "10.11.12.13"
		if err := os.WriteFile(envPath, []byte("MAKE_SYNC_HOST="+defaultIP), 0644); err != nil {
			t.Fatalf("failed to write test .env: %v", err)
		}
		defer os.Remove(envPath)
		expected = defaultIP
	} else {
		// read and parse MAKE_SYNC_HOST from file
		b, err := os.ReadFile(envPath)
		if err != nil {
			t.Fatalf("failed to read existing test .env: %v", err)
		}
		lines := strings.Split(string(b), "\n")
		for _, ln := range lines {
			if strings.HasPrefix(ln, "MAKE_SYNC_HOST=") {
				expected = strings.TrimPrefix(ln, "MAKE_SYNC_HOST=")
				break
			}
		}
	}

	// change cwd to test dir
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(testDir); err != nil {
		t.Fatalf("failed to chdir to test dir: %v", err)
	}

	cfg, err := LoadAndValidateConfig()
	if err != nil {
		t.Fatalf("LoadAndValidateConfig failed: %v", err)
	}

	if cfg.Host != expected {
		t.Fatalf("expected host from test/.env (%s), got %s", expected, cfg.Host)
	}
}
