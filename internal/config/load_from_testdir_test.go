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

	// Ensure test private key present (test make-sync.yaml references .ssh/openssh_nopassword)
	sshDir := filepath.Join(testDir, ".ssh")
	_ = os.MkdirAll(sshDir, 0755)
	pkPath := filepath.Join(sshDir, "openssh_nopassword")
	if _, err := os.Stat(pkPath); os.IsNotExist(err) {
		if err := os.WriteFile(pkPath, []byte("dummykey"), 0600); err != nil {
			t.Fatalf("failed to write dummy private key: %v", err)
		}
		defer os.Remove(pkPath)
	}

	// Ensure =... template references in the config can be resolved by
	// rendering template variables into the file first. The test's
	// make-sync.yaml uses =username, =privateKey, etc. Build a small
	// cfg with Var.auth so RenderTemplateVariables can substitute them.
	renderCfg := &Config{
		Var: map[string]interface{}{
			"auth": map[interface{}]interface{}{
				"username":   "donny",
				"privateKey": ".ssh/openssh_nopassword",
				"host":       expected,
				"port":       "22",
				"remotePath": "/home/donny/workspaces/project-xyz",
			},
		},
	}

	if err := RenderTemplateVariables(renderCfg); err != nil {
		t.Fatalf("RenderTemplateVariables failed: %v", err)
	}

	cfg, err := LoadAndValidateConfig()
	if err != nil {
		t.Fatalf("LoadAndValidateConfig failed: %v", err)
	}

	if cfg.Devsync.Auth.Host != expected {
		t.Fatalf("expected host from test/.env (%s), got %s", expected, cfg.Devsync.Auth.Host)
	}
}
