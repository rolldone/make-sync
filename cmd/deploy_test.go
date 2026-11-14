package cmd

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployRemotePath_POSIX(t *testing.T) {
	cases := []struct {
		remote, rel, want string
	}{
		{"/home/user/app", "cmd/exec.go", "/home/user/app/cmd/exec.go"},
		{"/home/user/app/", "README.md", "/home/user/app/README.md"},
		{"/srv/app", "docs/index.html", "/srv/app/docs/index.html"},
	}
	for _, c := range cases {
		if got := deployRemotePath(c.remote, c.rel, "linux"); got != c.want {
			t.Errorf("deployRemotePath(%q, %q) = %q, want %q", c.remote, c.rel, got, c.want)
		}
	}
}

func TestDeployRemotePath_Windows(t *testing.T) {
	cases := []struct {
		remote, rel, want string
	}{
		{`C:\app`, "cmd/exec.go", `C:\app\cmd\exec.go`},
		{`C:\app\`, "README.md", `C:\app\README.md`},
		{"C:/app", "docs/index.html", `C:\app\docs\index.html`},
	}
	for _, c := range cases {
		if got := deployRemotePath(c.remote, c.rel, "windows"); got != c.want {
			t.Errorf("deployRemotePath(%q, %q) = %q, want %q", c.remote, c.rel, got, c.want)
		}
	}
}

func TestBuildDeployUploadPairs_PreservesStructure(t *testing.T) {
	absRoot := filepath.FromSlash("/local/project")
	uploads := []string{"README.md", "cmd/exec.go"}

	pairs := buildDeployUploadPairs(absRoot, "/srv/app", "linux", uploads)
	if len(pairs) != 2 {
		t.Fatalf("got %d pairs, want 2", len(pairs))
	}
	wantLocal := filepath.Join(absRoot, filepath.FromSlash("README.md"))
	if pairs[0].Local != wantLocal {
		t.Errorf("pairs[0].Local = %q, want %q", pairs[0].Local, wantLocal)
	}
	if pairs[0].Remote != "/srv/app/README.md" {
		t.Errorf("pairs[0].Remote = %q, want /srv/app/README.md", pairs[0].Remote)
	}
	if pairs[1].Remote != "/srv/app/cmd/exec.go" {
		t.Errorf("pairs[1].Remote = %q, want /srv/app/cmd/exec.go", pairs[1].Remote)
	}
}

func TestBuildDeployUploadPairs_SkipsEmpty(t *testing.T) {
	pairs := buildDeployUploadPairs("/local", "/srv/app", "linux", []string{"", "   "})
	if len(pairs) != 0 {
		t.Errorf("got %d pairs, want 0", len(pairs))
	}
}

func TestBuildDeployDeleteCommands_POSIXQuoted(t *testing.T) {
	cmds := buildDeployDeleteCommands("/srv/app", "linux", []string{"obsolete/spec.md", "a b/c'd.md"})
	if len(cmds) != 1 {
		t.Fatalf("got %d commands, want 1: %v", len(cmds), cmds)
	}
	cmd := cmds[0]
	if !strings.HasPrefix(cmd, "rm -f -- ") {
		t.Errorf("command %q must start with 'rm -f -- '", cmd)
	}
	if !strings.Contains(cmd, "'/srv/app/obsolete/spec.md'") {
		t.Errorf("command %q missing quoted deletion path", cmd)
	}
	// Single quotes inside the path must be escaped POSIX-style.
	if !strings.Contains(cmd, `'/srv/app/a b/c'\''d.md'`) {
		t.Errorf("command %q missing escaped quote for embedded apostrophe", cmd)
	}
}

func TestBuildDeployDeleteCommands_Windows(t *testing.T) {
	cmds := buildDeployDeleteCommands(`C:\app`, "windows", []string{"obsolete/spec.md"})
	if len(cmds) != 1 {
		t.Fatalf("got %d commands, want 1", len(cmds))
	}
	if !strings.Contains(cmds[0], `del /F /Q "C:\app\obsolete\spec.md"`) {
		t.Errorf("command %q does not delete the expected windows path", cmds[0])
	}
}

func TestBuildDeployDeleteCommands_Batches(t *testing.T) {
	deletions := make([]string, deployBatchSize+5)
	for i := range deletions {
		deletions[i] = "dir/file_" + itoa(i) + ".txt"
	}

	cmds := buildDeployDeleteCommands("/srv/app", "linux", deletions)
	want := 2
	if len(cmds) != want {
		t.Fatalf("got %d commands, want %d", len(cmds), want)
	}
	// The second batch (5 files) must not exceed the batch size either.
	if n := strings.Count(cmds[1], "'/srv/app/"); n != 5 {
		t.Errorf("second batch quotes %d paths, want 5", n)
	}
}

func TestBuildDeployDeleteCommands_Empty(t *testing.T) {
	if cmds := buildDeployDeleteCommands("/srv/app", "linux", nil); len(cmds) != 0 {
		t.Errorf("got %d commands, want 0", len(cmds))
	}
}

func TestRunDeploy_RequiresAMode(t *testing.T) {
	err := runDeploy("   ", "   ")
	if err == nil {
		t.Fatal("expected error when no deploy mode is given")
	}
	if !strings.Contains(err.Error(), "--since-commit or --to-commit") {
		t.Errorf("error = %q, want mention of --since-commit/--to-commit", err)
	}
}

func TestRunDeploy_RejectsBothModes(t *testing.T) {
	err := runDeploy("HEAD~1", "HEAD~2")
	if err == nil {
		t.Fatal("expected error when both modes are given")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %q, want mention of mutual exclusivity", err)
	}
}

// itoa is a tiny helper so the batch test does not depend on strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
