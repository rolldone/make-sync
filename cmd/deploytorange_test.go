package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The revert-style resolver is pure git: it compares tree(<rev>) with tree(HEAD)
// and never looks at the working tree.

func TestComputeToCommitLists_RestoreAndDelete(t *testing.T) {
	f := newFixtureRepo(t)

	f.write(t, "keep.md", "keep\n")
	f.write(t, "changed.md", "v1\n")
	f.write(t, "gone-later.md", "restore me\n")
	target := f.commit(t, "target")

	// After <rev>: change one file, delete one, add a brand new one.
	f.write(t, "changed.md", "v2\n")
	f.commit(t, "change", "changed.md")
	f.remove(t, "gone-later.md")
	f.commit(t, "delete", "gone-later.md")
	f.write(t, "added-later.md", "new\n")
	f.commit(t, "add", "added-later.md")

	lists, err := computeToCommitLists(f.dir, target.String())
	if err != nil {
		t.Fatalf("computeToCommitLists: %v", err)
	}

	// changed.md gets its <rev> content back, gone-later.md is restored.
	if got, want := sortedCopy(lists.Upload), []string{"changed.md", "gone-later.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("upload list = %v, want %v", got, want)
	}
	// added-later.md exists in HEAD but not in <rev> → deleted on remote.
	if got, want := sortedCopy(lists.Deletion), []string{"added-later.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deletion list = %v, want %v", got, want)
	}
}

func TestComputeToCommitLists_IdenticalFileUntouched(t *testing.T) {
	f := newFixtureRepo(t)

	f.write(t, "same.md", "same\n")
	target := f.commit(t, "target")
	f.write(t, "other.md", "other\n")
	f.commit(t, "add other")

	lists, err := computeToCommitLists(f.dir, target.String())
	if err != nil {
		t.Fatalf("computeToCommitLists: %v", err)
	}
	if len(lists.Upload) != 0 {
		t.Errorf("upload list = %v, want empty (content unchanged)", lists.Upload)
	}
	if got, want := sortedCopy(lists.Deletion), []string{"other.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deletion list = %v, want %v", got, want)
	}
}

func TestComputeToCommitLists_IgnoresWorkingTree(t *testing.T) {
	f := newFixtureRepo(t)

	f.write(t, "tracked.md", "v1\n")
	target := f.commit(t, "target")
	f.write(t, "tracked.md", "v2\n")
	f.commit(t, "change tracked")

	// Uncommitted noise: a modification, an untracked file and a deletion.
	// None of these may appear, because <rev> vs HEAD is unchanged by them.
	f.write(t, "tracked.md", "dirty\n")
	f.write(t, "untracked.md", "untracked\n")
	f.write(t, "new-locally.md", "local\n")

	lists, err := computeToCommitLists(f.dir, target.String())
	if err != nil {
		t.Fatalf("computeToCommitLists: %v", err)
	}
	if got, want := sortedCopy(lists.Upload), []string{"tracked.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("upload list = %v, want %v (working tree must be ignored)", got, want)
	}
	if len(lists.Deletion) != 0 {
		t.Errorf("deletion list = %v, want empty", lists.Deletion)
	}
}

func TestComputeToCommitLists_SameAsHeadIsEmpty(t *testing.T) {
	f := newFixtureRepo(t)
	f.write(t, "README.md", "v1\n")
	f.commit(t, "base")
	f.write(t, "README.md", "v2\n")
	f.commit(t, "second")

	lists, err := computeToCommitLists(f.dir, "HEAD")
	if err != nil {
		t.Fatalf("computeToCommitLists: %v", err)
	}
	if len(lists.Upload) != 0 || len(lists.Deletion) != 0 {
		t.Errorf("lists = %+v, want both empty for rev == HEAD", lists)
	}
}

func TestComputeToCommitLists_RespectsSyncIgnore(t *testing.T) {
	f := newFixtureRepo(t)

	f.write(t, ".sync_ignore", "docs/\n")
	f.write(t, "cmd/exec.go", "v1\n")
	target := f.commit(t, "target")

	f.write(t, "docs/index.html", "<html></html>\n")
	f.write(t, "cmd/exec.go", "v2\n")
	f.commit(t, "docs + exec", "docs/index.html", "cmd/exec.go")

	lists, err := computeToCommitLists(f.dir, target.String())
	if err != nil {
		t.Fatalf("computeToCommitLists: %v", err)
	}
	if got, want := sortedCopy(lists.Upload), []string{"cmd/exec.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("upload list = %v, want %v (docs/ must be ignored)", got, want)
	}
}

func TestComputeToCommitLists_NotAGitRepository(t *testing.T) {
	if _, err := computeToCommitLists(t.TempDir(), "HEAD"); err == nil {
		t.Fatal("expected error for a non-git directory")
	}
}

func TestComputeToCommitLists_InvalidAndEmptyRevision(t *testing.T) {
	f := newFixtureRepo(t)
	f.write(t, "README.md", "v1\n")
	f.commit(t, "base")

	if _, err := computeToCommitLists(f.dir, "deadbeef99"); err == nil {
		t.Fatal("expected error for an unresolvable revision")
	}
	if _, err := computeToCommitLists(f.dir, "   "); err == nil {
		t.Fatal("expected error for an empty revision")
	}
}

func TestHashBlobsAtRev_UsesRevContentNotDisk(t *testing.T) {
	f := newFixtureRepo(t)

	f.write(t, "conf.txt", "rev-content\n")
	target := f.commit(t, "target")
	f.write(t, "conf.txt", "head-content\n")
	f.commit(t, "change")

	// The working tree is dirty on top of HEAD; it must not influence the hash.
	f.write(t, "conf.txt", "dirty-content\n")

	hashes, err := hashBlobsAtRev(f.dir, target.String(), []string{"conf.txt", "missing.txt", ""})
	if err != nil {
		t.Fatalf("hashBlobsAtRev: %v", err)
	}
	// SHA-256("rev-content\n") — the <rev> blob, not HEAD nor the dirty file.
	if want := "e0eaa1827a038e05a5561e0660fa6d2528c20c0ec3738449041de08bc5a1ae00"; hashes["conf.txt"] != want {
		t.Errorf("hash = %q, want %q", hashes["conf.txt"], want)
	}
	diskHash, herr := hashFileSHA256(filepath.Join(f.dir, "conf.txt"))
	if herr != nil {
		t.Fatalf("hashFileSHA256: %v", herr)
	}
	if hashes["conf.txt"] == diskHash {
		t.Errorf("hash must come from the %s blob, not the working-tree file", target)
	}
	if _, ok := hashes["missing.txt"]; ok {
		t.Errorf("missing path must not be hashed: %#v", hashes)
	}
	if _, ok := hashes[""]; ok {
		t.Errorf("empty path must be skipped: %#v", hashes)
	}
}

func TestHashBlobsAtRev_MatchesBlobContent(t *testing.T) {
	f := newFixtureRepo(t)
	f.write(t, "data.bin", "abc")
	f.commit(t, "base")

	hashes, err := hashBlobsAtRev(f.dir, "HEAD", []string{"data.bin"})
	if err != nil {
		t.Fatalf("hashBlobsAtRev: %v", err)
	}
	// SHA-256("abc")
	if want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"; hashes["data.bin"] != want {
		t.Errorf("hash = %q, want %q", hashes["data.bin"], want)
	}
}

func TestMaterializeRevTree_WritesRevContentAndCleanup(t *testing.T) {
	f := newFixtureRepo(t)

	f.write(t, "bin/run.sh", "echo run\n")
	f.write(t, "conf.txt", "rev\n")
	target := f.commit(t, "target")

	f.write(t, "conf.txt", "head\n")
	f.write(t, "brand-new.txt", "new\n")
	f.commit(t, "change")

	dir, cleanup, err := materializeRevTree(f.dir, target.String(), []string{"conf.txt", "bin/run.sh"})
	if err != nil {
		t.Fatalf("materializeRevTree: %v", err)
	}

	content, rerr := os.ReadFile(filepath.Join(dir, "conf.txt"))
	if rerr != nil {
		cleanup()
		t.Fatalf("read materialized conf.txt: %v", rerr)
	}
	if string(content) != "rev\n" {
		t.Errorf("materialized conf.txt = %q, want the %s version", content, target)
	}
	if _, serr := os.Stat(filepath.Join(dir, "bin", "run.sh")); serr != nil {
		t.Errorf("nested path not materialized: %v", serr)
	}

	cleanup()
	if _, serr := os.Stat(dir); !os.IsNotExist(serr) {
		t.Errorf("cleanup did not remove %s", dir)
	}
}

func TestMaterializeRevTree_MissingPathFailsAndCleansUp(t *testing.T) {
	f := newFixtureRepo(t)
	f.write(t, "present.txt", "x\n")
	f.commit(t, "base")

	dir, _, err := materializeRevTree(f.dir, "HEAD", []string{"present.txt", "absent.txt"})
	if err == nil {
		t.Fatal("expected an error for a path missing from the revision")
	}
	if dir != "" {
		t.Errorf("dir = %q, want empty on failure", dir)
	}
}

func TestCountUncommitted(t *testing.T) {
	f := newFixtureRepo(t)
	f.write(t, "tracked.txt", "v1\n")
	f.commit(t, "base")

	if n, err := countUncommitted(f.dir); err != nil || n != 0 {
		t.Fatalf("countUncommitted = %d, %v; want 0, nil", n, err)
	}
	f.write(t, "tracked.txt", "v2\n")
	f.write(t, "untracked.txt", "new\n")
	if n, err := countUncommitted(f.dir); err != nil || n != 2 {
		t.Fatalf("countUncommitted = %d, %v; want 2, nil", n, err)
	}
}
