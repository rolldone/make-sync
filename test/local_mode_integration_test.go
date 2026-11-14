//go:build integration

// Package main holds the path-to-path (mode: local) integration harness.
//
// Run it with:
//
//	go test -tags integration -timeout 10m -v ./test/
//
// The harness drives the real CLI binary against two local directories
// (source project + localTargetPath) so the whole local pipeline is exercised:
// agent build + local indexing, index DB location, delta upload, manual
// transfer and pull — all without any SSH connection.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const e2eBinaryName = ".e2e-make-sync-test"

// repoRootDir resolves the repository root from the package working directory
// (the harness lives in <repo>/test).
func repoRootDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, ".."))
}

// existingAgentBinaries snapshots the agent binaries already present in the
// repository root so the harness can delete the ones it causes the CLI to build.
func existingAgentBinaries(repoRoot string) map[string]bool {
	matches, _ := filepath.Glob(filepath.Join(repoRoot, "sync-agent*"))
	seen := make(map[string]bool, len(matches))
	for _, m := range matches {
		seen[m] = true
	}
	return seen
}

// removeNewAgentBinaries deletes agent binaries created during the test run
// (the CLI rebuilds the agent on every push/pull).
func removeNewAgentBinaries(repoRoot string, before map[string]bool) {
	matches, _ := filepath.Glob(filepath.Join(repoRoot, "sync-agent*"))
	for _, m := range matches {
		if !before[m] {
			_ = os.Remove(m)
		}
	}
}

// buildCLI compiles the make-sync CLI into the repository root.
//
// It must live in the repo root: util.GetProjectRoot() derives the project root
// from the executable location in production mode, and the agent binary is
// built from <projectRoot>/sub_app/agent.
func buildCLI(t *testing.T) string {
	t.Helper()
	repoRoot := repoRootDir(t)
	bin := filepath.Join(repoRoot, e2eBinaryName)

	before := existingAgentBinaries(repoRoot)

	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build CLI: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = os.Remove(bin)
		removeNewAgentBinaries(repoRoot, before)
	})
	return bin
}

// writeFile writes content to path, creating parent directories.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// readFile fails the test when the file is missing.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// assertMissing fails when path exists.
func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("expected %s to be absent, but it exists", path)
	}
}

// runSync executes the CLI in dir and returns combined output + exit code.
func runSync(t *testing.T, bin, dir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %v in %s: %v\n%s", args, dir, err, out)
		}
		code = exitErr.ExitCode()
	}
	t.Logf("[E2E] %s $ make-sync %s -> exit=%d\n%s", dir, strings.Join(args, " "), code, out)
	return string(out), code
}

// localModeYAML renders a minimal config selecting the local transport.
//
// os_target is deliberately set to "windows" to prove local mode uses the host
// OS (no cross-OS command syntax for a plain local directory).
func localModeYAML(projectName, target string) string {
	return fmt.Sprintf(`project_name: %s
devsync:
  os_target: windows
  auth:
    mode: local
    localTargetPath: %s
`, projectName, target)
}

// sshModeYAML renders a config with mode unset (implicit ssh, backward compat)
// pointing at a closed port so the connection fails fast.
func sshModeYAML(projectName string) string {
	return fmt.Sprintf(`project_name: %s
devsync:
  os_target: linux
  auth:
    username: nobody
    host: 127.0.0.1
    port: "1"
    remotePath: /tmp/should-not-be-used
`, projectName)
}

// TestSSHModeSyncRunsLikeBefore mirrors the local-mode harness over SSH.
//
// It is opt-in because it needs a reachable SSH server: set
// MAKE_SYNC_E2E_SSH_HOST/_PORT/_USER/_KEY/_REMOTE_PATH to run it. Without those
// variables the test is skipped, so `go test -tags integration ./test/` stays
// green on machines without a remote.
func TestSSHModeSyncRunsLikeBefore(t *testing.T) {
	host := os.Getenv("MAKE_SYNC_E2E_SSH_HOST")
	if host == "" {
		t.Skip("set MAKE_SYNC_E2E_SSH_HOST/_PORT/_USER/_KEY/_REMOTE_PATH to run the ssh-mode integration test")
	}

	port := envOr("MAKE_SYNC_E2E_SSH_PORT", "22")
	user := os.Getenv("MAKE_SYNC_E2E_SSH_USER")
	key := os.Getenv("MAKE_SYNC_E2E_SSH_KEY")
	remoteRoot := os.Getenv("MAKE_SYNC_E2E_SSH_REMOTE_PATH")
	if user == "" || remoteRoot == "" {
		t.Fatal("MAKE_SYNC_E2E_SSH_USER and MAKE_SYNC_E2E_SSH_REMOTE_PATH are required")
	}

	sshArgs := []string{"-p", port, "-o", "StrictHostKeyChecking=no", "-o", "BatchMode=yes"}
	if key != "" {
		if !filepath.IsAbs(key) {
			key = filepath.Join(repoRootDir(t), key)
		}
		sshArgs = append(sshArgs, "-i", key)
	}
	sshTarget := user + "@" + host

	remoteRun := func(script string) (string, int) {
		t.Helper()
		cmd := exec.Command("ssh", append(append([]string{}, sshArgs...), sshTarget, script)...)
		out, err := cmd.CombinedOutput()
		code := 0
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		}
		return string(out), code
	}

	bin := buildCLI(t)
	src := t.TempDir()

	writeFile(t, filepath.Join(src, "make-sync.yaml"), fmt.Sprintf(`project_name: e2e-ssh
devsync:
  os_target: linux
  auth:
    mode: ssh
    username: %s
    host: %s
    port: "%s"
    privateKey: %s
    remotePath: %s
`, user, host, port, key, remoteRoot))
	writeFile(t, filepath.Join(src, ".sync_ignore"), "ignored.txt\n")
	writeFile(t, filepath.Join(src, "keep.txt"), "hello ssh")

	if out, code := runSync(t, bin, src, "push"); code != 0 {
		t.Fatalf("ssh push failed (exit %d):\n%s", code, out)
	}

	out, code := remoteRun(fmt.Sprintf("cat %s/keep.txt", remoteRoot))
	if code != 0 || strings.TrimSpace(out) != "hello ssh" {
		t.Fatalf("expected keep.txt on remote (%s), exit=%d out=%q", remoteRoot, code, out)
	}

	if _, code := remoteRun(fmt.Sprintf("test -e %s/ignored.txt", remoteRoot)); code == 0 {
		t.Fatalf("ignored.txt must not be uploaded to remote %s", remoteRoot)
	}
}

// envOr returns the environment value or a fallback.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// TestLocalModeBackwardCompatibleWithoutMode verifies AC #1: when `mode` is not
// set the CLI keeps the legacy ssh behaviour and never touches a local target.
func TestLocalModeBackwardCompatibleWithoutMode(t *testing.T) {
	bin := buildCLI(t)

	src := t.TempDir()
	target := t.TempDir()

	writeFile(t, filepath.Join(src, "make-sync.yaml"), sshModeYAML("e2e-ssh-default"))
	writeFile(t, filepath.Join(src, "hello.txt"), "hello")

	out, code := runSync(t, bin, src, "push")
	if code == 0 {
		t.Fatalf("expected push without mode to fail (ssh), got success:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "ssh") {
		t.Fatalf("expected an ssh-related failure, got:\n%s", out)
	}

	// Nothing must have been copied to a local directory.
	assertMissing(t, filepath.Join(target, "hello.txt"))
	assertMissing(t, filepath.Join(src, ".sync_temp", "indexing_files_target.db"))
}

// TestLocalModePathToPathSync covers the local-mode acceptance criteria:
// initial push, delta push, scoped (manual transfer) push, pull and ignore
// handling — all between two local paths without SSH.
func TestLocalModePathToPathSync(t *testing.T) {
	bin := buildCLI(t)

	src := t.TempDir()
	target := t.TempDir()

	// Source project: two files plus an ignored one.
	writeFile(t, filepath.Join(src, "make-sync.yaml"), localModeYAML("e2e-local", target))
	writeFile(t, filepath.Join(src, ".sync_ignore"), "ignored.txt\n")
	writeFile(t, filepath.Join(src, "keep.txt"), "hello local")
	writeFile(t, filepath.Join(src, "sub", "nested.txt"), "nested")
	writeFile(t, filepath.Join(src, "ignored.txt"), "should not travel")

	t.Run("initial push uploads files and honours .sync_ignore", func(t *testing.T) {
		out, code := runSync(t, bin, src, "push")
		if code != 0 {
			t.Fatalf("initial push failed (exit %d):\n%s", code, out)
		}
		if strings.Contains(strings.ToLower(out), "ssh") {
			t.Fatalf("local mode must not touch SSH, got:\n%s", out)
		}

		if got := readFile(t, filepath.Join(target, "keep.txt")); got != "hello local" {
			t.Fatalf("keep.txt content mismatch: %q", got)
		}
		if got := readFile(t, filepath.Join(target, "sub", "nested.txt")); got != "nested" {
			t.Fatalf("sub/nested.txt content mismatch: %q", got)
		}
		assertMissing(t, filepath.Join(target, "ignored.txt"))

		if _, err := os.Stat(filepath.Join(target, ".sync_temp", "indexing_files.db")); err != nil {
			t.Fatalf("expected target index DB in local target: %v", err)
		}

		// The agent config carries the same ignore collection as the SSH path,
		// but is written into the local target's .sync_temp instead of being
		// uploaded. This is what keeps .sync_ignore behaviour identical.
		agentCfg := readFile(t, filepath.Join(target, ".sync_temp", "config.json"))
		if !strings.Contains(agentCfg, "ignored.txt") {
			t.Fatalf("expected collected ignore in local agent config, got:\n%s", agentCfg)
		}
		if !strings.Contains(agentCfg, target) {
			t.Fatalf("expected working_dir to be the local target, got:\n%s", agentCfg)
		}
	})

	t.Run("second push is a no-op (delta only)", func(t *testing.T) {
		out, code := runSync(t, bin, src, "push")
		if code != 0 {
			t.Fatalf("second push failed (exit %d):\n%s", code, out)
		}
		if !strings.Contains(out, "No files needed uploading") {
			t.Fatalf("expected no-op push, got:\n%s", out)
		}
	})

	t.Run("delta push uploads only the changed file", func(t *testing.T) {
		writeFile(t, filepath.Join(src, "keep.txt"), "hello local v2")

		out, code := runSync(t, bin, src, "push")
		if code != 0 {
			t.Fatalf("delta push failed (exit %d):\n%s", code, out)
		}
		if !strings.Contains(out, "Uploaded 1 files") {
			t.Fatalf("expected exactly 1 uploaded file, got:\n%s", out)
		}
		if got := readFile(t, filepath.Join(target, "keep.txt")); got != "hello local v2" {
			t.Fatalf("keep.txt not updated on target: %q", got)
		}
	})

	t.Run("scoped manual transfer push", func(t *testing.T) {
		writeFile(t, filepath.Join(src, "manual", "only.txt"), "manual payload")

		out, code := runSync(t, bin, src, "push", "--path", "manual")
		if code != 0 {
			t.Fatalf("scoped push failed (exit %d):\n%s", code, out)
		}
		if got := readFile(t, filepath.Join(target, "manual", "only.txt")); got != "manual payload" {
			t.Fatalf("manual/only.txt content mismatch: %q", got)
		}
	})

	t.Run("pull downloads target changes", func(t *testing.T) {
		writeFile(t, filepath.Join(target, "from_target.txt"), "came from target")

		out, code := runSync(t, bin, src, "pull")
		if code != 0 {
			t.Fatalf("pull failed (exit %d):\n%s", code, out)
		}
		if got := readFile(t, filepath.Join(src, "from_target.txt")); got != "came from target" {
			t.Fatalf("from_target.txt content mismatch: %q", got)
		}
	})

	t.Run("pull respects .sync_ignore", func(t *testing.T) {
		writeFile(t, filepath.Join(target, "ignored.txt"), "target ignored")

		out, code := runSync(t, bin, src, "pull")
		if code != 0 {
			t.Fatalf("pull failed (exit %d):\n%s", code, out)
		}
		if got := readFile(t, filepath.Join(src, "ignored.txt")); got != "should not travel" {
			t.Fatalf("ignored.txt must not be overwritten by pull, got %q", got)
		}
	})
}
