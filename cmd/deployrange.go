package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"make-sync/internal/syncdata"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
)

// DeployRangeLists is the pure result of resolving a commit range for
// `make-sync deploy --since-commit <rev>`.
//
//   - Upload: files changed in <rev>..HEAD plus uncommitted working-tree
//     changes (Modified/Added/Untracked). Paths are relative, forward-slash.
//   - Deletion: files that existed in <rev> but are gone in HEAD or in the
//     working tree. Only explicit deletions — never a "missing locally" sweep.
//
// Git is the source of truth: no remote comparison, no index, no agent.
type DeployRangeLists struct {
	Upload   []string
	Deletion []string
}

// computeCommitRangeLists resolves the upload/deletion file lists for a commit
// range rooted at localRoot. It is pure: no SSH, no network, no remote state.
//
// Rules (see spec-push-since-commit.md):
//   - Range = tree diff <rev>..HEAD ∪ working-tree changes, deduplicated.
//   - Renames surface as delete (old path) + insert (new path): rename
//     detection is disabled on purpose.
//   - A working-tree deletion counts only when the path still exists in the
//     HEAD tree.
//   - Working-tree state wins over the commit range for the same path.
//   - .sync_ignore filtering is applied to both lists (local-side).
func computeCommitRangeLists(localRoot, rev string) (DeployRangeLists, error) {
	var result DeployRangeLists

	rev = strings.TrimSpace(rev)
	if rev == "" {
		return result, fmt.Errorf("empty revision: --since-commit requires a git revision (e.g. HEAD~1, a tag, or a commit hash)")
	}

	absRoot, err := filepath.Abs(localRoot)
	if err != nil {
		return result, fmt.Errorf("cannot resolve local root %q: %w", localRoot, err)
	}

	repo, err := git.PlainOpen(absRoot)
	if err != nil {
		return result, fmt.Errorf("%s is not a git repository: %w", absRoot, err)
	}

	sinceCommit, err := resolveCommitForRev(repo, rev)
	if err != nil {
		return result, err
	}

	headCommit, err := headCommitOf(repo)
	if err != nil {
		return result, err
	}

	sinceTree, err := sinceCommit.Tree()
	if err != nil {
		return result, fmt.Errorf("cannot read tree of %q: %w", rev, err)
	}
	headTree, err := headCommit.Tree()
	if err != nil {
		return result, fmt.Errorf("cannot read HEAD tree: %w", err)
	}

	upload := map[string]struct{}{}
	deletion := map[string]struct{}{}

	// 1. Commit range: diff tree <rev> against tree HEAD.
	changes, err := object.DiffTreeWithOptions(
		context.Background(),
		sinceTree,
		headTree,
		&object.DiffTreeOptions{DetectRenames: false},
	)
	if err != nil {
		return result, fmt.Errorf("tree diff %s..HEAD failed: %w", rev, err)
	}
	for _, change := range changes {
		action, aerr := change.Action()
		if aerr != nil {
			return result, fmt.Errorf("malformed change in %s..HEAD: %w", rev, aerr)
		}
		switch action {
		case merkletrie.Insert, merkletrie.Modify:
			if !change.To.TreeEntry.Mode.IsFile() {
				continue
			}
			if p := normalizeDeployPath(change.To.Name); p != "" {
				upload[p] = struct{}{}
			}
		case merkletrie.Delete:
			if !change.From.TreeEntry.Mode.IsFile() {
				continue
			}
			if p := normalizeDeployPath(change.From.Name); p != "" {
				deletion[p] = struct{}{}
			}
		}
	}

	// 2. Working-tree changes (uncommitted). These override the commit range
	// for the same path, because they represent the current local state.
	wt, err := repo.Worktree()
	if err != nil {
		return result, fmt.Errorf("cannot read worktree: %w", err)
	}
	status, err := wt.Status()
	if err != nil {
		return result, fmt.Errorf("cannot read worktree status: %w", err)
	}
	for rawPath, fileStatus := range status {
		p := normalizeDeployPath(rawPath)
		if p == "" {
			continue
		}
		// Staged rename: the map key is the new path, Extra is the old one.
		if fileStatus.Staging == git.Renamed && fileStatus.Extra != "" {
			oldPath := normalizeDeployPath(fileStatus.Extra)
			delete(deletion, p)
			upload[p] = struct{}{}
			if oldPath != "" && treeHasFile(headTree, oldPath) {
				delete(upload, oldPath)
				deletion[oldPath] = struct{}{}
			}
			continue
		}
		if isDeletionStatus(fileStatus.Staging) || isDeletionStatus(fileStatus.Worktree) {
			delete(upload, p)
			if treeHasFile(headTree, p) {
				deletion[p] = struct{}{}
			} else {
				delete(deletion, p)
			}
			continue
		}
		if isUploadStatus(fileStatus.Staging) || isUploadStatus(fileStatus.Worktree) {
			delete(deletion, p)
			upload[p] = struct{}{}
		}
	}

	// 3. Local-side ignore filtering (.sync_ignore), deterministic order.
	result.Upload = filterDeployPaths(absRoot, upload)
	result.Deletion = filterDeployPaths(absRoot, deletion)
	return result, nil
}

// resolveCommitForRev resolves a user-supplied git revision to a commit.
func resolveCommitForRev(repo *git.Repository, rev string) (*object.Commit, error) {
	hash, err := repo.ResolveRevision(plumbing.Revision(rev))
	if err != nil {
		return nil, fmt.Errorf("cannot resolve revision %q: %w", rev, err)
	}
	commit, err := repo.CommitObject(*hash)
	if err != nil {
		return nil, fmt.Errorf("revision %q does not point to a commit: %w", rev, err)
	}
	return commit, nil
}

// headCommitOf returns the commit HEAD points at.
func headCommitOf(repo *git.Repository) (*object.Commit, error) {
	ref, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("cannot read HEAD: %w", err)
	}
	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("cannot read HEAD commit: %w", err)
	}
	return commit, nil
}

// treeHasFile reports whether path (forward-slash) exists as a file in tree.
func treeHasFile(tree *object.Tree, path string) bool {
	if tree == nil || path == "" {
		return false
	}
	entry, err := tree.FindEntry(path)
	if err != nil {
		return false
	}
	return entry.Mode.IsFile()
}

// normalizeDeployPath converts a git path to the canonical relative,
// forward-slash form used by the sync pipeline.
func normalizeDeployPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = filepath.ToSlash(filepath.Clean(p))
	p = strings.TrimPrefix(p, "./")
	if p == "." {
		return ""
	}
	return p
}

// isDeletionStatus reports whether a git status code means the path was removed.
func isDeletionStatus(code git.StatusCode) bool {
	return code == git.Deleted
}

// isUploadStatus reports whether a git status code means the path should be
// uploaded (added, modified, or untracked).
func isUploadStatus(code git.StatusCode) bool {
	switch code {
	case git.Modified, git.Added, git.Untracked:
		return true
	default:
		return false
	}
}

// filterDeployPaths applies .sync_ignore rules and returns a sorted,
// deduplicated slice.
func filterDeployPaths(absRoot string, set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	paths := make([]string, 0, len(set))
	for p := range set {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	paths = syncdata.FilterPathsByIgnore(absRoot, paths)
	sort.Strings(paths)
	return paths
}
