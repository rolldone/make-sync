package deployagent

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// --- parsing (mirrors cmd/deploydelta_test.go fixtures) --------------------

func TestParseSha256SumOutput_MixedLines(t *testing.T) {
	valid := strings.Repeat("a1", 32)
	other := strings.Repeat("b2", 32)
	out := valid + "  /srv/.sync_temp/sync-agent-x\n" +
		"sha256sum: /srv/.sync_temp/missing: No such file or directory\n" +
		other + "  /srv/.sync_temp/other\n" +
		"\n" +
		"not a hash line\n"

	got := parseSha256SumOutput(out)
	want := map[string]string{
		"/srv/.sync_temp/sync-agent-x": valid,
		"/srv/.sync_temp/other":        other,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseSha256SumOutput = %#v, want %#v", got, want)
	}
}

func TestParseSha256SumOutput_SeparatorAndSpaces(t *testing.T) {
	sum := strings.Repeat("c3", 32)

	// One space is not the sha256sum text-mode format.
	if got := parseSha256SumOutput(sum + " /srv/one-space\n"); len(got) != 0 {
		t.Fatalf("single-space line parsed as %#v, want empty", got)
	}
	// Two spaces is the canonical format, and spaces inside the path survive.
	got := parseSha256SumOutput(sum + "  /srv/dir with spaces/sync-agent x\n")
	if got["/srv/dir with spaces/sync-agent x"] != sum {
		t.Fatalf("space-containing path parsed as %#v", got)
	}
	// sha256sum -b ("hash *path") must not be misread.
	if got := parseSha256SumOutput(sum + " */srv/binary-mode\n"); len(got) != 0 {
		t.Fatalf("binary-mode line parsed as %#v, want empty", got)
	}
}

func TestSha256SumUnavailable(t *testing.T) {
	cases := []struct {
		out  string
		want bool
	}{
		{"bash: line 1: sha256sum: command not found", true},
		{"sha256sum: not found", true},
		{"'sha256sum' is not recognized as an internal or external command", true},
		{"sha256sum: /srv/.sync_temp/sync-agent-x: No such file or directory", false},
		{"", false},
	}
	for _, c := range cases {
		if got := sha256SumUnavailable(c.out); got != c.want {
			t.Errorf("sha256SumUnavailable(%q) = %v, want %v", c.out, got, c.want)
		}
	}
}

func TestBuildSha256CheckCommand_QuotedAndTolerant(t *testing.T) {
	cmd := buildSha256CheckCommand("/srv/dir it's/app/sync-agent-1")
	if !strings.HasPrefix(cmd, "sha256sum -- ") {
		t.Fatalf("command %q must start with 'sha256sum -- '", cmd)
	}
	if !strings.Contains(cmd, `'/srv/dir it'\''s/app/sync-agent-1'`) {
		t.Fatalf("path not escaped POSIX-style: %q", cmd)
	}
	if !strings.HasSuffix(cmd, " 2>&1; true") {
		t.Fatalf("command %q must merge stderr and neutralise the exit status", cmd)
	}
}

// --- uploadAgentBinary behavior -------------------------------------------

func writeAgentFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatalf("write agent fixture: %v", err)
	}
	return p
}

func TestUploadAgentBinary_SkipsWhenHashMatches(t *testing.T) {
	dir := t.TempDir()
	local := writeAgentFixture(t, dir, "sync-agent-abc", "binary-content")

	hash, err := GetFileHash(local)
	if err != nil {
		t.Fatalf("GetFileHash: %v", err)
	}

	remoteTemp := "/srv/app/.sync_temp"
	remote := remoteTemp + "/sync-agent-abc"

	mock := NewMockSSHClient()
	mock.SetCommandResponse("sha256sum", hash+"  "+remote+"\n")

	if err := uploadAgentBinary(mock, local, remoteTemp, "linux"); err != nil {
		t.Fatalf("uploadAgentBinary: %v", err)
	}

	if len(mock.uploadCalls) != 0 {
		t.Errorf("upload calls = %d, want 0 (hash identical → skip)", len(mock.uploadCalls))
	}
	if !hasCommand(mock.commands, "chmod +x") {
		t.Errorf("chmod must still run when the upload is skipped: %v", mock.commands)
	}
}

func TestUploadAgentBinary_UploadsWhenHashDiffers(t *testing.T) {
	dir := t.TempDir()
	local := writeAgentFixture(t, dir, "sync-agent-abc", "binary-content")

	remoteTemp := "/srv/app/.sync_temp"
	remote := remoteTemp + "/sync-agent-abc"

	mock := NewMockSSHClient()
	mock.SetCommandResponse("sha256sum", strings.Repeat("ff", 32)+"  "+remote+"\n")

	if err := uploadAgentBinary(mock, local, remoteTemp, "linux"); err != nil {
		t.Fatalf("uploadAgentBinary: %v", err)
	}
	if len(mock.uploadCalls) != 1 {
		t.Fatalf("upload calls = %d, want 1 (hash differs)", len(mock.uploadCalls))
	}
	if mock.uploadCalls[0].RemotePath != remote {
		t.Errorf("remote path = %q, want %q", mock.uploadCalls[0].RemotePath, remote)
	}
	if !hasCommand(mock.commands, "chmod +x") {
		t.Errorf("chmod must run after upload: %v", mock.commands)
	}
}

func TestUploadAgentBinary_UploadsWhenRemoteFileMissing(t *testing.T) {
	dir := t.TempDir()
	local := writeAgentFixture(t, dir, "sync-agent-abc", "binary-content")

	mock := NewMockSSHClient()
	// sha256sum reports the file is absent — an expected "not uploaded yet" case.
	mock.SetCommandResponse("sha256sum", "sha256sum: /srv/app/.sync_temp/sync-agent-abc: No such file or directory\n")

	if err := uploadAgentBinary(mock, local, "/srv/app/.sync_temp", "linux"); err != nil {
		t.Fatalf("uploadAgentBinary: %v", err)
	}
	if len(mock.uploadCalls) != 1 {
		t.Fatalf("upload calls = %d, want 1 (remote file missing)", len(mock.uploadCalls))
	}
}

func TestUploadAgentBinary_UploadsWhenExecFails(t *testing.T) {
	dir := t.TempDir()
	local := writeAgentFixture(t, dir, "sync-agent-abc", "binary-content")

	mock := NewMockSSHClient()
	mock.SetShouldFail("command_output", true)

	if err := uploadAgentBinary(mock, local, "/srv/app/.sync_temp", "linux"); err != nil {
		t.Fatalf("uploadAgentBinary: %v", err)
	}
	if len(mock.uploadCalls) != 1 {
		t.Fatalf("upload calls = %d, want 1 (exec error → blind fallback)", len(mock.uploadCalls))
	}
}

func TestUploadAgentBinary_UploadsWhenOutputUnparseable(t *testing.T) {
	dir := t.TempDir()
	local := writeAgentFixture(t, dir, "sync-agent-abc", "binary-content")

	mock := NewMockSSHClient()
	mock.SetCommandResponse("sha256sum", "\n")

	if err := uploadAgentBinary(mock, local, "/srv/app/.sync_temp", "linux"); err != nil {
		t.Fatalf("uploadAgentBinary: %v", err)
	}
	if len(mock.uploadCalls) != 1 {
		t.Fatalf("upload calls = %d, want 1 (unparseable output → blind fallback)", len(mock.uploadCalls))
	}
}

func TestUploadAgentBinary_UploadsWhenSha256SumUnavailable(t *testing.T) {
	dir := t.TempDir()
	local := writeAgentFixture(t, dir, "sync-agent-abc", "binary-content")

	mock := NewMockSSHClient()
	mock.SetCommandResponse("sha256sum", "bash: line 1: sha256sum: command not found\n")

	if err := uploadAgentBinary(mock, local, "/srv/app/.sync_temp", "linux"); err != nil {
		t.Fatalf("uploadAgentBinary: %v", err)
	}
	if len(mock.uploadCalls) != 1 {
		t.Fatalf("upload calls = %d, want 1 (sha256sum missing → blind fallback)", len(mock.uploadCalls))
	}
}

func TestUploadAgentBinary_WindowsSkipsPreCheck(t *testing.T) {
	dir := t.TempDir()
	local := writeAgentFixture(t, dir, "sync-agent-abc.exe", "binary-content")

	mock := NewMockSSHClient()
	if err := uploadAgentBinary(mock, local, `C:\app\.sync_temp`, "windows"); err != nil {
		t.Fatalf("uploadAgentBinary: %v", err)
	}
	if len(mock.uploadCalls) != 1 {
		t.Fatalf("upload calls = %d, want 1 (Windows always uploads)", len(mock.uploadCalls))
	}
	for _, c := range mock.commands {
		if strings.Contains(c, "sha256sum") {
			t.Errorf("Windows target must not run sha256sum: %q", c)
		}
	}
	if hasCommand(mock.commands, "chmod") {
		t.Errorf("Windows target must not run chmod: %v", mock.commands)
	}
}

func TestAgentBinaryUnchanged_MatchesOnlyOnEqualHash(t *testing.T) {
	dir := t.TempDir()
	local := writeAgentFixture(t, dir, "sync-agent-abc", "content")
	hash, err := GetFileHash(local)
	if err != nil {
		t.Fatalf("GetFileHash: %v", err)
	}
	remote := "/srv/.sync_temp/sync-agent-abc"

	mock := NewMockSSHClient()
	mock.SetCommandResponse("sha256sum", hash+"  "+remote+"\n")
	if same, checked := agentBinaryUnchanged(mock, local, remote); !checked || !same {
		t.Errorf("agentBinaryUnchanged = (%v, %v), want (true, true)", same, checked)
	}

	// Hash mismatch stays a trusted "checked" result (upload needed).
	mock2 := NewMockSSHClient()
	mock2.SetCommandResponse("sha256sum", strings.Repeat("11", 32)+"  "+remote+"\n")
	if same, checked := agentBinaryUnchanged(mock2, local, remote); !checked || same {
		t.Errorf("agentBinaryUnchanged = (%v, %v), want (false, true)", same, checked)
	}

	// Missing remote file is also trusted, but "not the same" (upload needed).
	mock3 := NewMockSSHClient()
	mock3.SetCommandResponse("sha256sum", "sha256sum: "+remote+": No such file or directory\n")
	if same, checked := agentBinaryUnchanged(mock3, local, remote); !checked || same {
		t.Errorf("agentBinaryUnchanged = (%v, %v), want (false, true)", same, checked)
	}
}

func TestAgentBinaryUnchanged_UntrustedWhenLocalMissing(t *testing.T) {
	mock := NewMockSSHClient()
	same, checked := agentBinaryUnchanged(mock, filepath.Join(t.TempDir(), "nope"), "/srv/x")
	if checked {
		t.Errorf("agentBinaryUnchanged = (%v, %v), want unchecked when the local file is unreadable", same, checked)
	}
	if len(mock.commands) != 0 {
		t.Errorf("no remote command should run when the local hash fails: %v", mock.commands)
	}
}

// hasCommand reports whether any recorded command contains the given substring.
func hasCommand(commands []string, substr string) bool {
	for _, c := range commands {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}
