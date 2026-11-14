package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseSha256SumOutput_MixedLines(t *testing.T) {
	valid := strings.Repeat("a1", 32)
	other := strings.Repeat("b2", 32)
	out := valid + "  src/keep.php\n" +
		"sha256sum: src/missing.php: No such file or directory\n" +
		other + "  src/changed.php\n" +
		"\n" +
		"not a hash line\n"

	got := parseSha256SumOutput(out, "")
	want := map[string]string{
		"src/keep.php":    valid,
		"src/changed.php": other,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseSha256SumOutput = %#v, want %#v", got, want)
	}
}

func TestParseSha256SumOutput_TwoSpaceSeparator(t *testing.T) {
	sum := strings.Repeat("c3", 32)
	// One space is not the sha256sum text-mode format and must be rejected.
	if got := parseSha256SumOutput(sum+" src/single-space.php\n", ""); len(got) != 0 {
		t.Fatalf("single-space line parsed as %#v, want empty", got)
	}
	// Two spaces is the canonical format.
	if got := parseSha256SumOutput(sum+"  src/two-space.php\n", ""); got["src/two-space.php"] != sum {
		t.Fatalf("two-space line parsed as %#v, want hash %q", got, sum)
	}
	// `sha256sum -b` emits "hash *path" (single space + asterisk), which is not
	// the text-mode two-space format and must be rejected.
	if got := parseSha256SumOutput(sum+" *src/binary.php\n", ""); len(got) != 0 {
		t.Fatalf("binary-mode line parsed as %#v, want empty", got)
	}
}

func TestParseSha256SumOutput_PathWithSpaces(t *testing.T) {
	sum := strings.Repeat("d4", 32)
	got := parseSha256SumOutput(sum+"  src/my file (draft).php\n", "")
	if got["src/my file (draft).php"] != sum {
		t.Fatalf("path with spaces parsed as %#v, want hash %q", got, sum)
	}
}

func TestParseSha256SumOutput_StderrPerFile(t *testing.T) {
	sum := strings.Repeat("e5", 32)
	got := parseSha256SumOutput("", "sha256sum: src/gone.php: No such file or directory\n"+sum+"  src/ok.php\n")
	if got["src/ok.php"] != sum {
		t.Fatalf("stderr stream parsed as %#v, want hash for src/ok.php", got)
	}
	if _, ok := got["src/gone.php"]; ok {
		t.Fatalf("missing file must not have a hash: %#v", got)
	}
}

func TestComputeDelta_Deterministic(t *testing.T) {
	same := strings.Repeat("f6", 32)
	uploads := []string{"b.php", "a.php", "c.php", "a.php", "  "}
	local := map[string]string{"a.php": same, "b.php": same}
	remote := map[string]string{"a.php": strings.ToUpper(same)}

	send, skip := computeDelta(uploads, local, remote)

	// a.php matches (case-insensitive) → skip; b.php has no remote hash and
	// c.php no local hash → both sent, in upload order.
	if want := []string{"b.php", "c.php"}; !reflect.DeepEqual(send, want) {
		t.Fatalf("send = %v, want %v", send, want)
	}
	if want := []string{"a.php"}; !reflect.DeepEqual(skip, want) {
		t.Fatalf("skip = %v, want %v", skip, want)
	}

	// Running twice must not reshuffle: order follows the upload list.
	send2, skip2 := computeDelta(uploads, local, remote)
	if !reflect.DeepEqual(send, send2) || !reflect.DeepEqual(skip, skip2) {
		t.Fatalf("non-deterministic delta: %v/%v then %v/%v", send, skip, send2, skip2)
	}
}

func TestComputeDelta_EmptyRemoteOutputSendsAll(t *testing.T) {
	same := strings.Repeat("77", 32)
	uploads := []string{"a.php", "b.php"}
	local := map[string]string{"a.php": same, "b.php": same}

	send, skip := computeDelta(uploads, local, map[string]string{})
	if !reflect.DeepEqual(send, uploads) {
		t.Fatalf("send = %v, want all uploads %v", send, uploads)
	}
	if len(skip) != 0 {
		t.Fatalf("skip = %v, want empty", skip)
	}
}

func TestComputeDelta_MissingLocalHashSends(t *testing.T) {
	same := strings.Repeat("88", 32)
	send, skip := computeDelta([]string{"a.php"}, map[string]string{}, map[string]string{"a.php": same})
	if !reflect.DeepEqual(send, []string{"a.php"}) || len(skip) != 0 {
		t.Fatalf("send = %v skip = %v, want a.php sent", send, skip)
	}
}

func TestBuildSha256SumCommand_QuotedBatch(t *testing.T) {
	cmd := buildSha256SumCommand("/srv/app", []string{"src/a.php", "src/it's here.php", "src/a.php"})
	if !strings.HasPrefix(cmd, "sha256sum -- ") {
		t.Fatalf("command %q must start with 'sha256sum -- '", cmd)
	}
	if strings.Count(cmd, "'/srv/app/src/a.php'") != 1 {
		t.Fatalf("duplicate path not collapsed: %q", cmd)
	}
	if !strings.Contains(cmd, `'/srv/app/src/it'\''s here.php'`) {
		t.Fatalf("embedded apostrophe not escaped: %q", cmd)
	}
	if !strings.HasSuffix(cmd, " 2>&1; true") {
		t.Fatalf("command %q must merge stderr and neutralise per-file exit status", cmd)
	}
}

func TestBuildSha256SumCommand_Empty(t *testing.T) {
	if cmd := buildSha256SumCommand("/srv/app", nil); cmd != "" {
		t.Fatalf("command = %q, want empty", cmd)
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
		{"sha256sum: src/missing.php: No such file or directory", false},
		{"", false},
	}
	for _, c := range cases {
		if got := sha256SumUnavailable(c.out); got != c.want {
			t.Errorf("sha256SumUnavailable(%q) = %v, want %v", c.out, got, c.want)
		}
	}
}

func TestHashLocalFiles_ComputesAndSkipsMissing(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(existing, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := hashLocalFiles(dir, []string{"keep.txt", "missing.txt", "", "keep.txt"})
	if len(got) != 1 {
		t.Fatalf("hashLocalFiles = %#v, want only keep.txt", got)
	}
	// SHA-256("hello")
	if want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"; got["keep.txt"] != want {
		t.Fatalf("keep.txt hash = %q, want %q", got["keep.txt"], want)
	}
}

func TestRemoteHashesByRel_MapsEscapedPaths(t *testing.T) {
	byRemote := remoteHashesByRel("/srv/app/", []string{"src/a.php", "docs/my file.md"})
	if byRemote["/srv/app/src/a.php"] != "src/a.php" {
		t.Fatalf("mapping = %#v", byRemote)
	}
	if byRemote["/srv/app/docs/my file.md"] != "docs/my file.md" {
		t.Fatalf("mapping = %#v", byRemote)
	}
}
