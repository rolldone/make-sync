package config

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvInterpolationFromDotEnv(t *testing.T) {
	// create temp dir
	dir, err := os.MkdirTemp("", "cfgtest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// write make-sync.yaml
	cfgText := strings.Join([]string{
		"project_name: test",
		"username: user",
		"host: ${MAKE_SYNCHOST}",
		"port: \"22\"",
		"remotePath: /tmp",
		"devsync:",
		"  os_target: linux",
		"  auth:",
		"    username: user",
		"    host: ${MAKE_SYNCHOST}",
		"    port: \"22\"",
		"    remotePath: /tmp",
	}, "\n") + "\n"
	cfgPath := filepath.Join(dir, ConfigFileName)
	if err := ioutil.WriteFile(cfgPath, []byte(cfgText), 0644); err != nil {
		t.Fatal(err)
	}

	// write .env
	envText := "MAKE_SYNCHOST=example.env.host"
	if err := ioutil.WriteFile(filepath.Join(dir, ".env"), []byte(envText), 0644); err != nil {
		t.Fatal(err)
	}

	// change working dir to temp dir
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	os.Chdir(dir)

	cfg, err := LoadAndValidateConfig()
	if err != nil {
		t.Fatalf("LoadAndValidateConfig failed: %v", err)
	}

	if cfg.Devsync.Auth.Host != "example.env.host" {
		t.Fatalf("expected host from .env, got %s", cfg.Devsync.Auth.Host)
	}
}

func TestEnvInterpolationPrecedenceOSTakesPriority(t *testing.T) {
	dir, err := os.MkdirTemp("", "cfgtest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	cfgText := strings.Join([]string{
		"project_name: test",
		"username: user",
		"host: ${MAKE_SYNCHOST}",
		"port: \"22\"",
		"remotePath: /tmp",
		"devsync:",
		"  os_target: linux",
		"  auth:",
		"    username: user",
		"    host: ${MAKE_SYNCHOST}",
		"    port: \"22\"",
		"    remotePath: /tmp",
	}, "\n") + "\n"
	cfgPath := filepath.Join(dir, ConfigFileName)
	if err := ioutil.WriteFile(cfgPath, []byte(cfgText), 0644); err != nil {
		t.Fatal(err)
	}

	envText := "MAKE_SYNCHOST=example.env.host"
	if err := ioutil.WriteFile(filepath.Join(dir, ".env"), []byte(envText), 0644); err != nil {
		t.Fatal(err)
	}

	// set OS env to override
	os.Setenv("MAKE_SYNCHOST", "from.os.env")
	defer os.Unsetenv("MAKE_SYNCHOST")

	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	os.Chdir(dir)

	cfg, err := LoadAndValidateConfig()
	if err != nil {
		t.Fatalf("LoadAndValidateConfig failed: %v", err)
	}

	if cfg.Devsync.Auth.Host != "from.os.env" {
		t.Fatalf("expected host from OS env, got %s", cfg.Devsync.Auth.Host)
	}
}
