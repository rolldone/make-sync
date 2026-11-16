package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	git "github.com/go-git/go-git/v5"
)

// setConfig writes one repository-local config value (e.g. core.autocrlf).
func (f *fixtureRepo) setConfig(t *testing.T, section, key, value string) {
	t.Helper()
	cfg, err := f.repo.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.Raw == nil {
		t.Fatalf("Config.Raw is nil")
	}
	cfg.Raw.Section(section).SetOption(key, value)
	if err := f.repo.SetConfig(cfg); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
}

// rawStatus returns go-git's unfiltered view of the working tree.
func (f *fixtureRepo) rawStatus(t *testing.T) git.Status {
	t.Helper()
	status, err := f.wt.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	return status
}

// filteredStatus returns the status after this package's CRLF false-positive
// filtering, mirroring what the deploy/push paths consume.
func (f *fixtureRepo) filteredStatus(t *testing.T) git.Status {
	t.Helper()
	return dropStatusFalsePositives(f.dir, f.repo, f.wt, f.rawStatus(t))
}

func TestDropLineEndingFalsePositives_CRLFOnlyIsDropped(t *testing.T) {
	f := newFixtureRepo(t)
	f.setConfig(t, "core", "autocrlf", "true")

	// git stores LF in the blob ...
	f.write(t, "src/app.php", "<?php\nline1\nline2\n")
	f.commit(t, "base")
	// ... and core.autocrlf=true converts it to CRLF on checkout.
	f.write(t, "src/app.php", "<?php\r\nline1\r\nline2\r\n")

	if _, ok := f.rawStatus(t)["src/app.php"]; !ok {
		t.Fatalf("precondition failed: go-git did not report the CRLF file as changed")
	}
	if _, ok := f.filteredStatus(t)["src/app.php"]; ok {
		t.Errorf("CRLF-only difference must be filtered out (git reports it clean)")
	}
}

func TestDropLineEndingFalsePositives_RealChangeKept(t *testing.T) {
	f := newFixtureRepo(t)
	f.setConfig(t, "core", "autocrlf", "true")

	f.write(t, "src/app.php", "<?php\nline1\n")
	f.commit(t, "base")
	f.write(t, "src/app.php", "<?php\r\nline1\r\nline2-added\r\n")

	if _, ok := f.filteredStatus(t)["src/app.php"]; !ok {
		t.Errorf("a genuine content change must survive the filter")
	}
}

func TestDropLineEndingFalsePositives_LFToCRLFConversionOnly(t *testing.T) {
	f := newFixtureRepo(t)
	f.setConfig(t, "core", "autocrlf", "true")

	// A file committed with CRLF in the blob (as a Windows checkout may do) and
	// unchanged on disk must also be filtered: the conversion is a no-op there.
	f.write(t, "src/win.php", "<?php\r\nline1\r\n")
	f.commit(t, "base")

	status := f.filteredStatus(t)
	if _, ok := status["src/win.php"]; ok {
		t.Errorf("unchanged file reported as changed: %v", status)
	}
}

func TestDropLineEndingFalsePositives_UntrackedKept(t *testing.T) {
	f := newFixtureRepo(t)
	f.setConfig(t, "core", "autocrlf", "true")

	f.write(t, "README.md", "base\n")
	f.commit(t, "base")
	f.write(t, "src/new.php", "<?php\r\nbrand new\r\n")

	if _, ok := f.filteredStatus(t)["src/new.php"]; !ok {
		t.Errorf("untracked files must never be filtered out")
	}
}

func TestDropLineEndingFalsePositives_DeletedKept(t *testing.T) {
	f := newFixtureRepo(t)
	f.setConfig(t, "core", "autocrlf", "true")

	f.write(t, "src/gone.php", "<?php\n")
	f.commit(t, "base")
	f.remove(t, "src/gone.php")

	if _, ok := f.filteredStatus(t)["src/gone.php"]; !ok {
		t.Errorf("deletions must never be filtered out")
	}
}

func TestDropLineEndingFalsePositives_AutocrlfDisabledKeepsCRLFChange(t *testing.T) {
	f := newFixtureRepo(t)
	f.setConfig(t, "core", "autocrlf", "false")

	f.write(t, "src/app.php", "<?php\nline1\n")
	f.commit(t, "base")
	f.write(t, "src/app.php", "<?php\r\nline1\r\n")

	// With autocrlf=false git compares raw bytes, so this IS a change for git
	// too; the filter must not silently hide it.
	if _, ok := f.filteredStatus(t)["src/app.php"]; !ok {
		t.Errorf("with core.autocrlf=false the CRLF rewrite must be kept")
	}
}

func TestDropLineEndingFalsePositives_GitAttributesTextAuto(t *testing.T) {
	f := newFixtureRepo(t)
	// No autocrlf: the conversion must come from .gitattributes alone.
	f.write(t, ".gitattributes", "*.php text=auto\n")
	f.write(t, "src/app.php", "<?php\nline1\n")
	f.commit(t, "base")
	f.write(t, "src/app.php", "<?php\r\nline1\r\n")

	if _, ok := f.filteredStatus(t)["src/app.php"]; ok {
		t.Errorf("text=auto must enable the checkin conversion for .php files")
	}
}

func TestDropLineEndingFalsePositives_GitAttributesBinaryKept(t *testing.T) {
	f := newFixtureRepo(t)
	f.setConfig(t, "core", "autocrlf", "true")
	f.write(t, ".gitattributes", "*.bin binary\n")
	// A NUL byte marks the content as binary; the CRLF rewrite is a real change
	// because git never converts it.
	f.write(t, "data/asset.bin", "a\x00b\n")
	f.commit(t, "base")
	f.write(t, "data/asset.bin", "a\x00b\r\n")

	if _, ok := f.filteredStatus(t)["data/asset.bin"]; !ok {
		t.Errorf("binary content must never be normalised away")
	}
}

func TestDropLineEndingFalsePositives_NoCRContentUntouched(t *testing.T) {
	f := newFixtureRepo(t)
	f.setConfig(t, "core", "autocrlf", "true")

	f.write(t, "src/app.php", "<?php\nline1\n")
	f.commit(t, "base")
	f.write(t, "src/app.php", "<?php\nline1\nline2\n")

	if _, ok := f.filteredStatus(t)["src/app.php"]; !ok {
		t.Errorf("content without CR must be compared as-is")
	}
}

func TestComputeCommitRangeLists_IgnoresCRLFFalsePositives(t *testing.T) {
	f := newFixtureRepo(t)
	f.setConfig(t, "core", "autocrlf", "true")

	f.write(t, "src/app.php", "<?php\nline1\n")
	f.write(t, "src/other.php", "<?php\nother\n")
	head := f.commit(t, "base")

	// A checkout with core.autocrlf=true rewrites every text file to CRLF.
	f.write(t, "src/app.php", "<?php\r\nline1\r\n")
	f.write(t, "src/other.php", "<?php\r\nother\r\n")
	// One file really changes.
	f.write(t, "src/real.php", "<?php\r\nnew file\r\n")

	lists, err := computeCommitRangeLists(f.dir, head.String())
	if err != nil {
		t.Fatalf("computeCommitRangeLists: %v", err)
	}
	want := []string{"src/real.php"}
	if got := sortedCopy(lists.Upload); !reflect.DeepEqual(got, want) {
		t.Errorf("upload list = %v, want %v", got, want)
	}
}

func TestComputeCommitRangeLists_KeepsDeletionWithCRLFNoise(t *testing.T) {
	f := newFixtureRepo(t)
	f.setConfig(t, "core", "autocrlf", "true")

	f.write(t, "src/app.php", "<?php\nline1\n")
	f.write(t, "src/gone.php", "<?php\ngone\n")
	head := f.commit(t, "base")

	f.write(t, "src/app.php", "<?php\r\nline1\r\n") // CRLF noise only
	f.remove(t, "src/gone.php")

	lists, err := computeCommitRangeLists(f.dir, head.String())
	if err != nil {
		t.Fatalf("computeCommitRangeLists: %v", err)
	}
	if len(lists.Upload) != 0 {
		t.Errorf("upload list = %v, want empty (only CRLF noise)", lists.Upload)
	}
	if got := sortedCopy(lists.Deletion); !reflect.DeepEqual(got, []string{"src/gone.php"}) {
		t.Errorf("deletion list = %v, want [src/gone.php]", got)
	}
}

func TestCountUncommitted_IgnoresCRLFFalsePositives(t *testing.T) {
	f := newFixtureRepo(t)
	f.setConfig(t, "core", "autocrlf", "true")

	f.write(t, "src/app.php", "<?php\nline1\n")
	f.write(t, "src/other.php", "<?php\nother\n")
	f.commit(t, "base")
	f.write(t, "src/app.php", "<?php\r\nline1\r\n")
	f.write(t, "src/other.php", "<?php\r\nother\r\n")
	f.write(t, "src/new.php", "<?php\r\nnew\r\n")

	n, err := countUncommitted(f.dir)
	if err != nil {
		t.Fatalf("countUncommitted: %v", err)
	}
	if n != 1 {
		t.Errorf("countUncommitted = %d, want 1 (only the untracked file)", n)
	}
}

func TestGetGitChangedPaths_IgnoresCRLFFalsePositives(t *testing.T) {
	f := newFixtureRepo(t)
	f.setConfig(t, "core", "autocrlf", "true")

	f.write(t, "src/app.php", "<?php\nline1\n")
	f.commit(t, "base")
	f.write(t, "src/app.php", "<?php\r\nline1\r\n")
	f.write(t, "src/new.php", "<?php\r\nnew\r\n")

	got := getGitChangedPaths(f.dir)
	want := []string{"src/new.php"}
	if !reflect.DeepEqual(sortedCopy(got), want) {
		t.Errorf("getGitChangedPaths = %v, want %v", got, want)
	}
}

func TestCRLFToLF(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\r\nb\r\n", "a\nb\n"},
		{"a\nb\n", "a\nb\n"},
		{"", ""},
		{"\r\n", "\n"},
		{"a\rb", "a\rb"}, // lone CR is preserved, like git
	}
	for _, c := range cases {
		if got := string(crlfToLF([]byte(c.in))); got != c.want {
			t.Errorf("crlfToLF(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsBinaryContent(t *testing.T) {
	if isBinaryContent([]byte("plain text\n")) {
		t.Errorf("plain text classified as binary")
	}
	if !isBinaryContent([]byte("a\x00b")) {
		t.Errorf("NUL byte not detected as binary")
	}
	// A NUL beyond the sniff window is not enough, matching git's heuristic.
	big := make([]byte, binarySniffLen+10)
	for i := range big {
		big[i] = 'a'
	}
	big[binarySniffLen+5] = 0
	if isBinaryContent(big) {
		t.Errorf("NUL outside the sniff window must not mark binary")
	}
}

func TestEOLPolicy_Converts(t *testing.T) {
	cases := []struct {
		name     string
		autocrlf string
		attrs    string
		rel      string
		content  string
		want     bool
	}{
		{"autocrlf=true text", "true", "", "a.php", "x\r\n", true},
		{"autocrlf=input text", "input", "", "a.php", "x\r\n", true},
		{"autocrlf=false", "false", "", "a.php", "x\r\n", false},
		{"unset", "", "", "a.php", "x\r\n", false},
		{"autocrlf=true binary content", "true", "", "a.bin", "x\x00\r\n", false},
		{"attr text", "", "*.php text\n", "a.php", "x\r\n", true},
		{"attr text=auto", "", "*.php text=auto\n", "a.php", "x\r\n", true},
		{"attr -text", "true", "*.php -text\n", "a.php", "x\r\n", false},
		{"attr binary", "true", "*.bin binary\n", "a.bin", "x\r\n", false},
		{"attr eol=crlf", "", "*.php eol=crlf\n", "a.php", "x\r\n", true},
		{"attr in subdir", "", "sub/*.php text\n", "sub/a.php", "x\r\n", true},
		{"attr other dir", "", "sub/*.php text\n", "a.php", "x\r\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixtureRepo(t)
			if c.autocrlf != "" {
				f.setConfig(t, "core", "autocrlf", c.autocrlf)
			}
			if c.attrs != "" {
				f.write(t, ".gitattributes", c.attrs)
			}
			policy := newEOLPolicy(f.repo, f.wt.Filesystem)
			if got := policy.converts(c.rel, []byte(c.content)); got != c.want {
				t.Errorf("converts(%q, %q) = %v, want %v", c.rel, c.content, got, c.want)
			}
		})
	}
}

func TestDropLineEndingFalsePositives_NestedGitAttributes(t *testing.T) {
	f := newFixtureRepo(t)
	// Rules in a nested directory apply to that subtree only.
	f.write(t, "app/.gitattributes", "*.php text\n")
	f.write(t, "app/a.php", "<?php\nx\n")
	f.write(t, "other/a.php", "<?php\nx\n")
	f.commit(t, "base")
	f.write(t, "app/a.php", "<?php\r\nx\r\n")
	f.write(t, "other/a.php", "<?php\r\nx\r\n")

	status := f.filteredStatus(t)
	if _, ok := status["app/a.php"]; ok {
		t.Errorf("app/a.php is covered by app/.gitattributes and must be filtered")
	}
	if _, ok := status["other/a.php"]; !ok {
		t.Errorf("other/a.php is outside the rule and must be kept")
	}
}

func TestDropLineEndingFalsePositives_NoChangesIsNoop(t *testing.T) {
	f := newFixtureRepo(t)
	f.setConfig(t, "core", "autocrlf", "true")
	f.write(t, "src/app.php", "<?php\n")
	f.commit(t, "base")

	status := f.filteredStatus(t)
	if len(status) != 0 {
		t.Errorf("clean tree reported as %v", status)
	}
}

// chmod changes the permission bits of a tracked file without touching its
// content, reproducing a checkout whose files are 0755 while the index records
// 0644 (common when the working tree was copied from another machine).
func (f *fixtureRepo) chmod(t *testing.T, rel string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(filepath.Join(f.dir, filepath.FromSlash(rel)), mode); err != nil {
		t.Fatalf("Chmod %s: %v", rel, err)
	}
}

func TestDropStatusFalsePositives_FilemodeDisabledDropsExecBitOnly(t *testing.T) {
	f := newFixtureRepo(t)
	f.setConfig(t, "core", "autocrlf", "false")

	f.write(t, "app/Models/RegDistrictSearchable.php", "<?php\nclass X {}\n")
	f.commit(t, "base")
	f.chmod(t, "app/Models/RegDistrictSearchable.php", 0o755)

	if _, ok := f.rawStatus(t)["app/Models/RegDistrictSearchable.php"]; !ok {
		t.Fatalf("precondition failed: go-git did not report the exec-bit change")
	}

	// With the default core.filemode=true git does report this, so it must stay.
	if _, ok := f.filteredStatus(t)["app/Models/RegDistrictSearchable.php"]; !ok {
		t.Errorf("with core.filemode=true the exec-bit change must be kept")
	}

	f.setConfig(t, "core", "filemode", "false")
	status := f.filteredStatus(t)
	if _, ok := status["app/Models/RegDistrictSearchable.php"]; ok {
		t.Errorf("with core.filemode=false the exec-bit noise must be dropped: %v", status)
	}
}

func TestDropStatusFalsePositives_FilemodeDisabledKeepsContentChange(t *testing.T) {
	f := newFixtureRepo(t)
	f.setConfig(t, "core", "autocrlf", "false")
	f.setConfig(t, "core", "filemode", "false")

	f.write(t, "src/app.php", "<?php\nline1\n")
	f.commit(t, "base")
	f.write(t, "src/app.php", "<?php\nline1\nline2\n")
	f.chmod(t, "src/app.php", 0o755)

	if _, ok := f.filteredStatus(t)["src/app.php"]; !ok {
		t.Errorf("core.filemode=false must not hide a real content change")
	}
}

func TestDropStatusFalsePositives_BinaryIdenticalBytesExecBitOnly(t *testing.T) {
	f := newFixtureRepo(t)
	f.setConfig(t, "core", "autocrlf", "true")
	f.setConfig(t, "core", "filemode", "false")

	// Encrypted/binary payloads (.sync_collections/*_encrypto) exceed the
	// binary sniff window, so the line-ending path never applies; only the raw
	// hash plus the ignored exec bit prove the file unchanged.
	blob := "\x44\x97\x83\x24\x00\xb8\x28\xb2\x6e\xf5\xdc\x23\x00\x20\x73\xd8"
	f.write(t, ".sync_collections/rollproject/image_encrypto", blob)
	f.commit(t, "base")
	f.chmod(t, ".sync_collections/rollproject/image_encrypto", 0o755)

	if _, ok := f.rawStatus(t)[".sync_collections/rollproject/image_encrypto"]; !ok {
		t.Fatalf("precondition failed: go-git did not report the binary file")
	}
	status := f.filteredStatus(t)
	if _, ok := status[".sync_collections/rollproject/image_encrypto"]; ok {
		t.Errorf("identical binary content with only the exec bit differing must be dropped: %v", status)
	}
}

func TestDropStatusFalsePositives_FilemodeDisabledKeepsBinaryContentChange(t *testing.T) {
	f := newFixtureRepo(t)
	f.setConfig(t, "core", "autocrlf", "true")
	f.setConfig(t, "core", "filemode", "false")

	f.write(t, ".sync_collections/x/blob_encrypto", "\x00\x01\x02\x03")
	f.commit(t, "base")
	f.write(t, ".sync_collections/x/blob_encrypto", "\x00\x01\x02\x03\x04")
	f.chmod(t, ".sync_collections/x/blob_encrypto", 0o755)

	if _, ok := f.filteredStatus(t)[".sync_collections/x/blob_encrypto"]; !ok {
		t.Errorf("a binary content change must never be filtered out")
	}
}

func TestDropLineEndingFalsePositives_NoIndexEntryKept(t *testing.T) {
	f := newFixtureRepo(t)
	f.setConfig(t, "core", "autocrlf", "true")

	// Force an internal error path: a worktree without an index must not panic
	// and must keep the entry (uncertainty keeps candidates).
	f.write(t, "src/app.php", "<?php\nx\n")
	f.commit(t, "base")
	f.write(t, "src/app.php", "<?php\r\nx\r\n")

	if err := os.Remove(filepath.Join(f.dir, ".git", "index")); err != nil {
		t.Fatalf("remove index: %v", err)
	}
	status := dropStatusFalsePositives(f.dir, f.repo, f.wt, f.rawStatus(t))
	if _, ok := status["src/app.php"]; !ok {
		t.Errorf("without an index entry the candidate must be kept, got %v", status)
	}
}

func TestDropLineEndingFalsePositives_NilInputs(t *testing.T) {
	if got := dropStatusFalsePositives("/tmp", nil, nil, git.Status{}); got == nil {
		t.Errorf("nil status must be returned as-is")
	}
	status := git.Status{}
	if got := dropStatusFalsePositives("/tmp", nil, nil, status); got == nil {
		t.Errorf("nil repo/worktree must not panic")
	}
}
