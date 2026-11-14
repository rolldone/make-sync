package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
)

// Revert-style deploy (`deploy --to-commit <rev>`) makes the remote match the
// content of an older commit. Everything here is pure git: the comparison is
// tree(<rev>) against tree(HEAD) and the working tree is deliberately ignored.
// The uploaded bytes are the blobs of <rev>, never the disk files.

// toCommitTrees resolves the two trees compared by --to-commit: the target
// revision's tree and HEAD's tree.
func toCommitTrees(localRoot, rev string) (revTree, headTree *object.Tree, revHash string, err error) {
	absRoot, aerr := filepath.Abs(localRoot)
	if aerr != nil {
		return nil, nil, "", fmt.Errorf("cannot resolve local root %q: %w", localRoot, aerr)
	}

	repo, rerr := git.PlainOpen(absRoot)
	if rerr != nil {
		return nil, nil, "", fmt.Errorf("%s is not a git repository: %w", absRoot, rerr)
	}

	commit, cerr := resolveCommitForRev(repo, rev)
	if cerr != nil {
		return nil, nil, "", cerr
	}
	head, herr := headCommitOf(repo)
	if herr != nil {
		return nil, nil, "", herr
	}

	revTree, terr := commit.Tree()
	if terr != nil {
		return nil, nil, "", fmt.Errorf("cannot read tree of %q: %w", rev, terr)
	}
	headTree, herr = head.Tree()
	if herr != nil {
		return nil, nil, "", fmt.Errorf("cannot read HEAD tree: %w", herr)
	}
	return revTree, headTree, commit.Hash.String(), nil
}

// computeToCommitLists resolves the upload/deletion lists that make the remote
// match the content of tree(<rev>). Only paths that differ between <rev> and
// HEAD appear:
//
//   - Upload: exists in <rev> but not in HEAD (file restored), or exists in both
//     with different content — the remote must receive the <rev> version.
//   - Deletion: exists in HEAD but not in <rev> (file created after <rev>) — it
//     must be removed on the remote.
//
// Files present in both trees with identical content, files absent from both,
// and files that exist only because of manual server edits are never touched.
func computeToCommitLists(localRoot, rev string) (DeployRangeLists, error) {
	var result DeployRangeLists

	rev = strings.TrimSpace(rev)
	if rev == "" {
		return result, fmt.Errorf("empty revision: --to-commit requires a git revision (e.g. HEAD~1, a tag, or a commit hash)")
	}

	absRoot, err := filepath.Abs(localRoot)
	if err != nil {
		return result, fmt.Errorf("cannot resolve local root %q: %w", localRoot, err)
	}

	revTree, headTree, _, err := toCommitTrees(absRoot, rev)
	if err != nil {
		return result, err
	}

	// diff <rev> -> HEAD (rename detection off: renames surface as
	// delete + insert, matching the git semantics of the deploy command).
	changes, err := object.DiffTreeWithOptions(
		context.Background(),
		revTree,
		headTree,
		&object.DiffTreeOptions{DetectRenames: false},
	)
	if err != nil {
		return result, fmt.Errorf("tree diff %s..HEAD failed: %w", rev, err)
	}

	upload := map[string]struct{}{}
	deletion := map[string]struct{}{}
	for _, change := range changes {
		action, aerr := change.Action()
		if aerr != nil {
			return result, fmt.Errorf("malformed change while comparing %s and HEAD: %w", rev, aerr)
		}
		switch action {
		case merkletrie.Modify:
			// Present in both trees with different content: send the <rev> version.
			if !change.From.TreeEntry.Mode.IsFile() {
				continue
			}
			if p := normalizeDeployPath(change.From.Name); p != "" {
				upload[p] = struct{}{}
			}
		case merkletrie.Delete:
			// In <rev>, gone from HEAD: the file was deleted later — restore it.
			if !change.From.TreeEntry.Mode.IsFile() {
				continue
			}
			if p := normalizeDeployPath(change.From.Name); p != "" {
				upload[p] = struct{}{}
			}
		case merkletrie.Insert:
			// In HEAD, absent from <rev>: created after <rev> — delete on remote.
			if !change.To.TreeEntry.Mode.IsFile() {
				continue
			}
			if p := normalizeDeployPath(change.To.Name); p != "" {
				deletion[p] = struct{}{}
			}
		}
	}

	result.Upload = filterDeployPaths(absRoot, upload)
	result.Deletion = filterDeployPaths(absRoot, deletion)
	return result, nil
}

// hashBlobsAtRev returns the SHA-256 of the blob content of each path as
// recorded in tree(<rev>). This is the delta pre-check's local side for
// --to-commit: the hash is taken from the git object, not from the disk file
// (which may belong to HEAD or be locally modified).
//
// Paths missing from the tree are skipped rather than failing the whole set.
func hashBlobsAtRev(localRoot, rev string, rels []string) (map[string]string, error) {
	if len(rels) == 0 {
		return map[string]string{}, nil
	}
	revTree, _, _, err := toCommitTrees(localRoot, rev)
	if err != nil {
		return nil, err
	}
	hashes := make(map[string]string, len(rels))
	for _, rel := range rels {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			continue
		}
		sum, herr := hashTreeBlob(revTree, rel)
		if herr != nil {
			continue
		}
		hashes[rel] = sum
	}
	return hashes, nil
}

// hashTreeBlob computes the SHA-256 of one file's content inside a git tree.
func hashTreeBlob(tree *object.Tree, rel string) (string, error) {
	if tree == nil || rel == "" {
		return "", fmt.Errorf("invalid tree or path")
	}
	file, err := tree.File(filepath.ToSlash(rel))
	if err != nil {
		return "", err
	}
	reader, err := file.Reader()
	if err != nil {
		return "", err
	}
	defer reader.Close()

	h := sha256.New()
	if _, err := io.Copy(h, reader); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// materializeRevTree writes the <rev> content of every path in rels into a fresh
// temporary directory, preserving the relative structure and the executable
// bit. The returned cleanup function removes the directory; callers must invoke
// it once the upload finishes.
//
// This is what lets the existing SFTP upload helpers send the <rev> bytes even
// when the working tree holds a different version of the same file.
func materializeRevTree(localRoot, rev string, rels []string) (dir string, cleanup func(), err error) {
	revTree, _, _, err := toCommitTrees(localRoot, rev)
	if err != nil {
		return "", nil, err
	}

	dir, err = os.MkdirTemp("", "make-sync-to-commit-*")
	if err != nil {
		return "", nil, fmt.Errorf("cannot create temporary directory: %w", err)
	}
	cleanup = func() { os.RemoveAll(dir) }

	for _, rel := range rels {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
			continue
		}
		file, ferr := revTree.File(rel)
		if ferr != nil {
			cleanup()
			return "", nil, fmt.Errorf("cannot read %q from revision %s: %w", rel, rev, ferr)
		}
		contents, cerr := file.Contents()
		if cerr != nil {
			cleanup()
			return "", nil, fmt.Errorf("cannot read content of %q at %s: %w", rel, rev, cerr)
		}
		mode := os.FileMode(0o644)
		if m, merr := file.Mode.ToOSFileMode(); merr == nil {
			mode = m.Perm()
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if merr := os.MkdirAll(filepath.Dir(dst), 0o755); merr != nil {
			cleanup()
			return "", nil, fmt.Errorf("cannot create directory for %q: %w", rel, merr)
		}
		if werr := os.WriteFile(dst, []byte(contents), mode); werr != nil {
			cleanup()
			return "", nil, fmt.Errorf("cannot write %q: %w", rel, werr)
		}
	}
	return dir, cleanup, nil
}

// countUncommitted reports how many paths differ in the working tree. --to-commit
// ignores the working tree, so the caller warns the operator when this is > 0.
func countUncommitted(localRoot string) (int, error) {
	absRoot, err := filepath.Abs(localRoot)
	if err != nil {
		return 0, err
	}
	repo, err := git.PlainOpen(absRoot)
	if err != nil {
		return 0, err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return 0, err
	}
	status, err := wt.Status()
	if err != nil {
		return 0, err
	}
	return len(status), nil
}
