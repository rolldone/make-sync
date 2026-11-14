package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func baseConfig() *Config {
	cfg := &Config{ProjectName: "test"}
	cfg.Devsync.OSTarget = "linux"
	cfg.Devsync.Auth.Username = "user"
	cfg.Devsync.Auth.Host = "example.com"
	cfg.Devsync.Auth.Port = "22"
	cfg.Devsync.Auth.RemotePath = "/var/www/app"
	return cfg
}

func TestValidateConfigDefaultModeKeepsSSHRequirements(t *testing.T) {
	cfg := baseConfig()
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected valid ssh config, got %v", err)
	}
	if cfg.Devsync.Auth.EffectiveMode() != AuthModeSSH {
		t.Fatalf("expected default mode ssh, got %s", cfg.Devsync.Auth.EffectiveMode())
	}
}

func TestValidateConfigSSHModeStillValidatesHostAndRemotePath(t *testing.T) {
	cfg := baseConfig()
	cfg.Devsync.Auth.Mode = AuthModeSSH
	cfg.Devsync.Auth.Host = ""
	cfg.Devsync.Auth.RemotePath = ""

	err := ValidateConfig(cfg)
	if err == nil {
		t.Fatal("expected validation error for missing host/remotePath")
	}
	msg := err.Error()
	if !strings.Contains(msg, "devsync.auth.host cannot be empty") {
		t.Fatalf("missing host error in %q", msg)
	}
	if !strings.Contains(msg, "devsync.auth.remotePath cannot be empty") {
		t.Fatalf("missing remotePath error in %q", msg)
	}
}

func TestValidateConfigLocalModeRequiresAbsoluteTargetPath(t *testing.T) {
	cfg := &Config{ProjectName: "test"}
	cfg.Devsync.OSTarget = "linux"
	cfg.Devsync.Auth.Mode = AuthModeLocal

	err := ValidateConfig(cfg)
	if err == nil {
		t.Fatal("expected error when localTargetPath is empty")
	}
	if !strings.Contains(err.Error(), "devsync.auth.localTargetPath cannot be empty") {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg.Devsync.Auth.LocalTargetPath = "relative/path"
	err = ValidateConfig(cfg)
	if err == nil {
		t.Fatal("expected error for relative localTargetPath")
	}
	if !strings.Contains(err.Error(), "must be an absolute path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateConfigLocalModeDoesNotRequireSSHFields(t *testing.T) {
	cfg := &Config{ProjectName: "test"}
	cfg.Devsync.OSTarget = "linux"
	cfg.Devsync.Auth.Mode = AuthModeLocal
	cfg.Devsync.Auth.LocalTargetPath = filepath.Join(t.TempDir(), "target")

	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected valid local config without ssh fields, got %v", err)
	}
	if !cfg.Devsync.Auth.IsLocal() {
		t.Fatal("expected IsLocal() to be true")
	}
}

func TestValidateConfigRejectsUnknownMode(t *testing.T) {
	cfg := baseConfig()
	cfg.Devsync.Auth.Mode = "s3"

	err := ValidateConfig(cfg)
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
	if !strings.Contains(err.Error(), "devsync.auth.mode must be") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEffectiveModeNormalisesValue(t *testing.T) {
	auth := Auth{Mode: "  LOCAL "}
	if auth.EffectiveMode() != AuthModeLocal {
		t.Fatalf("expected local, got %s", auth.EffectiveMode())
	}
	if !auth.IsLocal() {
		t.Fatal("expected IsLocal() true")
	}
}
