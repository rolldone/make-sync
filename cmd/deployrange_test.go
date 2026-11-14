package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// --- fixtures -------------------------------------------------------------

type fixtureRepo struct {
	dir  string
	repo *git.Repository
	wt   *git.Worktree
}

func newFixtureRepo(t *testing.T) *fixtureRepo {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	return &fixtureRepo{dir: dir, repo: repo, wt: wt}
}

// write creates/overwrites a file in the fixture worktree.
func (f *fixtureRepo) write(t *testing.T, rel string, content string) {
	t.Helper()
	abs := filepath.Join(f.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", rel, err)
	}
}

func (f *fixtureRepo) remove(t *testing.T, rel string) {
	t.Helper()
	if err := os.Remove(filepath.Join(f.dir, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("Remove %s: %v", rel, err)
	}
}

// commit stages the given paths (or everything when empty) and commits.
func (f *fixtureRepo) commit(t *testing.T, msg string, paths ...string) plumbing.Hash {
	t.Helper()
	if len(paths) == 0 {
		if err := f.wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
			t.Fatalf("Add all: %v", err)
		}
	} else {
		for _, p := range paths {
			if _, err := f.wt.Add(p); err != nil {
				t.Fatalf("Add %s: %v", p, err)
			}
		}
	}
	hash, err := f.wt.Commit(msg, &git.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("Commit %q: %v", msg, err)
	}
	return hash
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// --- tests ---------------------------------------------------------------

func TestComputeCommitRangeLists_RangeUnion(t *testing.T) {
	f := newFixtureRepo(t)

	f.write(t, "README.md", "v1\n")
	f.write(t, "cmd/exec.go", "v1\n")
	f.write(t, "cmd/keep.go", "v1\n")
	base := f.commit(t, "base")

	// Commit 1: modify README, add rawargs.
	f.write(t, "README.md", "v2\n")
	f.write(t, "cmd/rawargs_posix.go", "package cmd\n")
	f.commit(t, "commit 1", "README.md", "cmd/rawargs_posix.go")

	// Commit 2: modify exec.go.
	f.write(t, "cmd/exec.go", "v2\n")
	f.commit(t, "commit 2", "cmd/exec.go")

	lists, err := computeCommitRangeLists(f.dir, base.String())
	if err != nil {
		t.Fatalf("computeCommitRangeLists: %v", err)
	}
	wantUpload := []string{"README.md", "cmd/exec.go", "cmd/rawargs_posix.go"}
	if got := sortedCopy(lists.Upload); !reflect.DeepEqual(got, wantUpload) {
		t.Errorf("upload list = %v, want %v", got, wantUpload)
	}
	if len(lists.Deletion) != 0 {
		t.Errorf("deletion list = %v, want empty", lists.Deletion)
	}
}

func TestComputeCommitRangeLists_DeletionInRange(t *testing.T) {
	f := newFixtureRepo(t)

	f.write(t, "keep.md", "keep\n")
	f.write(t, "obsolete/spec.md", "old\n")
	base := f.commit(t, "base")

	f.remove(t, "obsolete/spec.md")
	f.commit(t, "delete obsolete", "obsolete/spec.md")

	lists, err := computeCommitRangeLists(f.dir, base.String())
	if err != nil {
		t.Fatalf("computeCommitRangeLists: %v", err)
	}
	wantDeletion := []string{"obsolete/spec.md"}
	if got := sortedCopy(lists.Deletion); !reflect.DeepEqual(got, wantDeletion) {
		t.Errorf("deletion list = %v, want %v", got, wantDeletion)
	}
	if len(lists.Upload) != 0 {
		t.Errorf("upload list = %v, want empty", lists.Upload)
	}
}

func TestComputeCommitRangeLists_RenameSplitsIntoDeleteAndUpload(t *testing.T) {
	f := newFixtureRepo(t)

	body := "package cmd\n\nfunc pushUpload() {}\n"
	f.write(t, "cmd/push.go", body)
	base := f.commit(t, "base")

	f.remove(t, "cmd/push.go")
	f.write(t, "cmd/push_upload.go", body)
	f.commit(t, "rename push.go", "cmd/push.go", "cmd/push_upload.go")

	lists, err := computeCommitRangeLists(f.dir, base.String())
	if err != nil {
		t.Fatalf("computeCommitRangeLists: %v", err)
	}
	if got, want := sortedCopy(lists.Upload), []string{"cmd/push_upload.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("upload list = %v, want %v", got, want)
	}
	if got, want := sortedCopy(lists.Deletion), []string{"cmd/push.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deletion list = %v, want %v", got, want)
	}
}

func TestComputeCommitRangeLists_WorkingTreeChanges(t *testing.T) {
	f := newFixtureRepo(t)

	f.write(t, "tracked.go", "v1\n")
	f.write(t, "gone.go", "v1\n")
	base := f.commit(t, "base")

	// Untracked addition + working-tree modification + working-tree deletion.
	f.write(t, "untracked.md", "new\n")
	f.write(t, "tracked.go", "v2\n")
	f.remove(t, "gone.go")

	lists, err := computeCommitRangeLists(f.dir, base.String())
	if err != nil {
		t.Fatalf("computeCommitRangeLists: %v", err)
	}
	wantUpload := []string{"tracked.go", "untracked.md"}
	if got := sortedCopy(lists.Upload); !reflect.DeepEqual(got, wantUpload) {
		t.Errorf("upload list = %v, want %v", got, wantUpload)
	}
	wantDeletion := []string{"gone.go"}
	if got := sortedCopy(lists.Deletion); !reflect.DeepEqual(got, wantDeletion) {
		t.Errorf("deletion list = %v, want %v", got, wantDeletion)
	}
}

func TestComputeCommitRangeLists_EmptyRange(t *testing.T) {
	f := newFixtureRepo(t)
	f.write(t, "README.md", "v1\n")
	f.commit(t, "base")

	lists, err := computeCommitRangeLists(f.dir, "HEAD")
	if err != nil {
		t.Fatalf("computeCommitRangeLists: %v", err)
	}
	if len(lists.Upload) != 0 || len(lists.Deletion) != 0 {
		t.Errorf("lists = %+v, want both empty", lists)
	}
}

func TestComputeCommitRangeLists_IgnoreFilter(t *testing.T) {
	f := newFixtureRepo(t)

	f.write(t, ".sync_ignore", "docs/\n")
	f.write(t, "cmd/exec.go", "v1\n")
	base := f.commit(t, "base")

	f.write(t, "docs/index.html", "<html></html>\n")
	f.write(t, "cmd/exec.go", "v2\n")
	f.commit(t, "docs + exec", "docs/index.html", "cmd/exec.go")

	lists, err := computeCommitRangeLists(f.dir, base.String())
	if err != nil {
		t.Fatalf("computeCommitRangeLists: %v", err)
	}
	if got, want := sortedCopy(lists.Upload), []string{"cmd/exec.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("upload list = %v, want %v (docs/ must be ignored)", got, want)
	}
}

func TestComputeCommitRangeLists_NotAGitRepository(t *testing.T) {
	dir := t.TempDir()
	if _, err := computeCommitRangeLists(dir, "HEAD"); err == nil {
		t.Fatal("expected error for non-git directory, got nil")
	}
}

func TestComputeCommitRangeLists_InvalidRevision(t *testing.T) {
	f := newFixtureRepo(t)
	f.write(t, "README.md", "v1\n")
	f.commit(t, "base")

	if _, err := computeCommitRangeLists(f.dir, "deadbeef99"); err == nil {
		t.Fatal("expected error for unresolvable revision, got nil")
	}
}

func TestComputeCommitRangeLists_EmptyRevision(t *testing.T) {
	f := newFixtureRepo(t)
	f.write(t, "README.md", "v1\n")
	f.commit(t, "base")

	if _, err := computeCommitRangeLists(f.dir, "   "); err == nil {
		t.Fatal("expected error for empty revision, got nil")
	}
}

func TestResolveCommitForRev_RevisionForms(t *testing.T) {
	f := newFixtureRepo(t)

	f.write(t, "README.md", "v1\n")
	first := f.commit(t, "first")
	f.write(t, "README.md", "v2\n")
	f.commit(t, "second")

	// Branch name.
	if _, err := resolveCommitForRev(f.repo, "master"); err != nil {
		t.Errorf("resolve master: %v", err)
	}
	// HEAD~1 -> first commit.
	commit, err := resolveCommitForRev(f.repo, "HEAD~1")
	if err != nil {
		t.Fatalf("resolve HEAD~1: %v", err)
	}
	if commit.Hash != first {
		t.Errorf("HEAD~1 = %s, want %s", commit.Hash, first)
	}
	// Full hash and short hash.
	if _, err := resolveCommitForRev(f.repo, first.String()); err != nil {
		t.Errorf("resolve full hash: %v", err)
	}
	if _, err := resolveCommitForRev(f.repo, first.String()[:7]); err != nil {
		t.Errorf("resolve short hash: %v", err)
	}
}
