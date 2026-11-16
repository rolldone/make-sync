package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
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

// commitOnBranch forks <branch> at the current HEAD, checks it out, runs fn
// (which is expected to commit), then returns to the original branch.
func (f *fixtureRepo) commitOnBranch(t *testing.T, branch string, fn func()) plumbing.Hash {
	t.Helper()
	head, err := f.repo.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	orig := head.Name()
	ref := plumbing.NewHashReference(plumbing.NewBranchReferenceName(branch), head.Hash())
	if err := f.repo.Storer.SetReference(ref); err != nil {
		t.Fatalf("SetReference %s: %v", branch, err)
	}
	if err := f.wt.Checkout(&git.CheckoutOptions{Branch: ref.Name()}); err != nil {
		t.Fatalf("Checkout %s: %v", branch, err)
	}

	fn()

	tip, err := f.repo.Head()
	if err != nil {
		t.Fatalf("Head after branch work: %v", err)
	}
	if err := f.wt.Checkout(&git.CheckoutOptions{Branch: orig}); err != nil {
		t.Fatalf("Checkout back %s: %v", orig, err)
	}
	return tip.Hash()
}

// merge records a merge commit on the current branch whose second parent is the
// tip of <branch>.
func (f *fixtureRepo) merge(t *testing.T, branch, msg string) plumbing.Hash {
	t.Helper()
	head, err := f.repo.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	other, err := f.repo.Reference(plumbing.NewBranchReferenceName(branch), true)
	if err != nil {
		t.Fatalf("Reference %s: %v", branch, err)
	}
	hash, err := f.wt.Commit(msg, &git.CommitOptions{
		Parents:           []plumbing.Hash{head.Hash(), other.Hash()},
		AllowEmptyCommits: true,
		Author:            &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("Merge commit %q: %v", msg, err)
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
	// A root commit has no parent, so there is nothing to fall back to: the
	// range stays empty instead of panicking.
	if len(lists.Upload) != 0 || len(lists.Deletion) != 0 {
		t.Errorf("lists = %+v, want both empty", lists)
	}
	if lists.Notice != "" {
		t.Errorf("notice = %q, want empty for a root commit", lists.Notice)
	}
}

func TestComputeCommitRangeLists_RevEqualToHeadFallsBackToParent(t *testing.T) {
	f := newFixtureRepo(t)

	f.write(t, "README.md", "v1\n")
	f.commit(t, "base")
	f.write(t, "app/Controller.php", "<?php\n")
	f.write(t, "tests/LockTest.php", "<?php\n")
	head := f.commit(t, "merge-like change")

	// The hash came from `git log -1`, i.e. HEAD: the range would be empty.
	lists, err := computeCommitRangeLists(f.dir, head.String())
	if err != nil {
		t.Fatalf("computeCommitRangeLists: %v", err)
	}
	wantUpload := []string{"app/Controller.php", "tests/LockTest.php"}
	if got := sortedCopy(lists.Upload); !reflect.DeepEqual(got, wantUpload) {
		t.Errorf("upload list = %v, want %v (HEAD must fall back to its parent)", got, wantUpload)
	}
	if lists.Notice == "" {
		t.Error("expected a notice explaining the automatic fallback")
	}
	// The notice must name both the abandoned revision and the chosen parent.
	if !strings.Contains(lists.Notice, shortHash(head)) {
		t.Errorf("notice %q does not mention HEAD %s", lists.Notice, shortHash(head))
	}
	if lists.Baseline == head {
		t.Error("baseline still points at HEAD; fallback did not happen")
	}
}

func TestComputeCommitRangeLists_OldRevisionIsNotAdjusted(t *testing.T) {
	f := newFixtureRepo(t)

	f.write(t, "README.md", "v1\n")
	base := f.commit(t, "base")
	f.write(t, "a.txt", "a\n")
	f.commit(t, "second")
	f.write(t, "b.txt", "b\n")
	f.commit(t, "third")

	lists, err := computeCommitRangeLists(f.dir, base.String())
	if err != nil {
		t.Fatalf("computeCommitRangeLists: %v", err)
	}
	if want := []string{"a.txt", "b.txt"}; !reflect.DeepEqual(sortedCopy(lists.Upload), want) {
		t.Errorf("upload list = %v, want %v", sortedCopy(lists.Upload), want)
	}
	if lists.Notice != "" {
		t.Errorf("notice = %q, want empty for an explicit old baseline", lists.Notice)
	}
	if lists.Baseline != base {
		t.Errorf("baseline = %s, want %s", lists.Baseline, base)
	}
}

func TestComputeCommitRangeLists_HeadFallbackUnionsWorkingTree(t *testing.T) {
	f := newFixtureRepo(t)

	f.write(t, "README.md", "v1\n")
	f.commit(t, "base")
	f.write(t, "app/Controller.php", "<?php\n")
	head := f.commit(t, "committed change")
	// Uncommitted edit on top of HEAD.
	f.write(t, "pm2-prod.json", "{\"apps\":[]}\n")
	f.write(t, "app/Controller.php", "<?php\n// tweak\n")

	lists, err := computeCommitRangeLists(f.dir, head.String())
	if err != nil {
		t.Fatalf("computeCommitRangeLists: %v", err)
	}
	wantUpload := []string{"app/Controller.php", "pm2-prod.json"}
	if got := sortedCopy(lists.Upload); !reflect.DeepEqual(got, wantUpload) {
		t.Errorf("upload list = %v, want %v (parent range ∪ working tree)", got, wantUpload)
	}
	if lists.Notice == "" {
		t.Error("expected the fallback notice when merging with working-tree changes")
	}
}

func TestComputeCommitRangeLists_HeadFallbackResolvesSymbolicHead(t *testing.T) {
	f := newFixtureRepo(t)

	f.write(t, "README.md", "v1\n")
	f.commit(t, "base")
	f.write(t, "app/Controller.php", "<?php\n")
	f.commit(t, "committed change")

	// "HEAD" and "HEAD~0" resolve to the same commit as the raw hash.
	lists, err := computeCommitRangeLists(f.dir, "HEAD~0")
	if err != nil {
		t.Fatalf("computeCommitRangeLists: %v", err)
	}
	if want := []string{"app/Controller.php"}; !reflect.DeepEqual(sortedCopy(lists.Upload), want) {
		t.Errorf("upload list = %v, want %v", sortedCopy(lists.Upload), want)
	}
	if lists.Notice == "" {
		t.Error("symbolic HEAD must trigger the same fallback as a raw hash")
	}
}

func TestResolveSinceBaseline_Direct(t *testing.T) {
	f := newFixtureRepo(t)
	f.write(t, "README.md", "v1\n")
	root := f.commit(t, "root")
	f.write(t, "a.txt", "a\n")
	tip := f.commit(t, "tip")

	head, err := headCommitOf(f.repo)
	if err != nil {
		t.Fatalf("headCommitOf: %v", err)
	}
	since, err := resolveCommitForRev(f.repo, tip.String())
	if err != nil {
		t.Fatalf("resolveCommitForRev: %v", err)
	}
	got, notice := resolveSinceBaseline(f.repo, since, head)
	if got == nil || got.Hash == tip {
		t.Fatalf("expected fallback to the parent, got %v", got)
	}
	if notice == "" {
		t.Error("expected a notice")
	}
	if !strings.Contains(notice, "1 commit") {
		t.Errorf("notice = %q, want the commit count", notice)
	}

	// The root commit is reachable, so a multi-commit range counts them all.
	sinceRoot, _ := resolveCommitForRev(f.repo, root.String())
	if _, rootNotice := resolveSinceBaseline(f.repo, sinceRoot, head); rootNotice != "" {
		t.Errorf("an old baseline must not be adjusted, got notice %q", rootNotice)
	}
}

func TestCountCommitsInRange(t *testing.T) {
	f := newFixtureRepo(t)
	f.write(t, "README.md", "v1\n")
	first := f.commit(t, "first")
	f.write(t, "README.md", "v2\n")
	second := f.commit(t, "second")
	f.write(t, "README.md", "v3\n")
	head := f.commit(t, "third")

	headCommit, err := resolveCommitForRev(f.repo, head.String())
	if err != nil {
		t.Fatalf("resolveCommitForRev: %v", err)
	}
	if got := countCommitsInRange(f.repo, headCommit, first); got != 2 {
		t.Errorf("countCommitsInRange(first) = %d, want 2", got)
	}
	if got := countCommitsInRange(f.repo, headCommit, second); got != 1 {
		t.Errorf("countCommitsInRange(second) = %d, want 1", got)
	}
	if got := countCommitsInRange(f.repo, headCommit, head); got != 0 {
		t.Errorf("countCommitsInRange(HEAD) = %d, want 0", got)
	}
	if got := countCommitsInRange(nil, headCommit, first); got != 0 {
		t.Errorf("countCommitsInRange(nil repo) = %d, want 0", got)
	}
}

// TestCountCommitsInRange_MergeDoesNotInflate is a regression test: counting
// must prune the baseline's whole ancestry, not just the baseline hash, or a
// merge whose second parent forks off older history reports the entire history
// (observed 349 instead of 2 on umah_loka_prod).
func TestCountCommitsInRange_MergeDoesNotInflate(t *testing.T) {
	f := newFixtureRepo(t)

	// Long base history on the first-parent line.
	f.write(t, "history.txt", "v0\n")
	for i := 1; i <= 20; i++ {
		f.write(t, "history.txt", fmt.Sprintf("v%d\n", i))
		f.commit(t, fmt.Sprintf("history %d", i))
	}
	f.write(t, "base.txt", "base\n")
	base := f.commit(t, "base")
	baseCommit, err := resolveCommitForRev(f.repo, base.String())
	if err != nil {
		t.Fatalf("resolveCommitForRev: %v", err)
	}

	// Feature branch forked from the base, two commits.
	featureTip := f.commitOnBranch(t, "feature", func() {
		f.write(t, "feature/a.txt", "a\n")
		f.commit(t, "feature 1")
		f.write(t, "feature/b.txt", "b\n")
		f.commit(t, "feature 2")
	})
	// First-parent commit on master.
	f.write(t, "master.txt", "m\n")
	f.commit(t, "master change")

	// Merge commit: parents = [master change, feature tip].
	merge := f.merge(t, "feature", "merge feature")

	mergeCommit, err := resolveCommitForRev(f.repo, merge.String())
	if err != nil {
		t.Fatalf("resolveCommitForRev(merge): %v", err)
	}
	if got := countCommitsInRange(f.repo, mergeCommit, baseCommit.Hash); got != 4 {
		t.Errorf("countCommitsInRange = %d, want 4 (1 master + 2 feature + merge), feature tip %s",
			got, shortHash(featureTip))
	}
}

func TestPluralCommits(t *testing.T) {
	cases := map[int]string{0: "0 commit", 1: "1 commit", 2: "2 commit", 17: "17 commit"}
	for n, want := range cases {
		if got := pluralCommits(n); got != want {
			t.Errorf("pluralCommits(%d) = %q, want %q", n, got, want)
		}
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
