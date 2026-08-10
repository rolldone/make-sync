package cmd

import (
	"os"
	"path/filepath"

	"make-sync/internal/config"
	"make-sync/internal/syncdata"

	"github.com/go-git/go-git/v5"
)

// resolveLocalRoot determines the local project root directory.
// Priority: cfg.LocalPath → cfg.Devsync.Auth.LocalPath → current working directory.
func resolveLocalRoot(cfg *config.Config) string {
	if cfg != nil {
		if cfg.LocalPath != "" {
			return cfg.LocalPath
		}
		if cfg.Devsync.Auth.LocalPath != "" {
			return cfg.Devsync.Auth.LocalPath
		}
	}
	wd, err := os.Getwd()
	if err == nil {
		return wd
	}
	return "."
}

// resolvePushPullPrefixes builds the list of relative path prefixes for push/pull
// based on --path flags and/or --git-changes flag.
//   - pathFlags: explicit paths from --path (repeatable)
//   - gitChanges: if true, get modified/added/untracked files from git working tree
//   - bypassIgnore: if false, git-changes candidates matching .sync_ignore are filtered out;
//     if true, all git-changes candidates are kept (bypass ignore filtering)
//
// Returns nil if no scoping is requested (full project sync).
func resolvePushPullPrefixes(localRoot string, pathFlags []string, gitChanges, bypassIgnore bool) []string {
	absRoot, err := filepath.Abs(localRoot)
	if err != nil {
		absRoot = localRoot
	}

	var candidates []string

	if gitChanges {
		gitPaths := getGitChangedPaths(absRoot)
		if !bypassIgnore {
			gitPaths = syncdata.FilterPathsByIgnore(absRoot, gitPaths)
		}
		candidates = append(candidates, gitPaths...)
	}

	candidates = append(candidates, pathFlags...)

	if len(candidates) == 0 {
		return nil
	}

	return syncdata.NormalizeToRelativePrefixes(absRoot, candidates)
}

// getGitChangedPaths returns relative paths of files changed in the git working tree.
// Status codes included: Modified (M), Added (A), Untracked (?).
// Deleted (D) is NOT included — deletion requires --force mode.
func getGitChangedPaths(absRoot string) []string {
	repo, err := git.PlainOpen(absRoot)
	if err != nil {
		return nil
	}

	wt, err := repo.Worktree()
	if err != nil {
		return nil
	}

	status, err := wt.Status()
	if err != nil {
		return nil
	}

	seen := make(map[string]struct{})
	var paths []string
	for filePath, fileStatus := range status {
		p := filepath.ToSlash(filePath)

		if fileStatus.Staging == git.Modified || fileStatus.Staging == git.Added || fileStatus.Staging == git.Untracked {
			if _, ok := seen[p]; !ok {
				seen[p] = struct{}{}
				paths = append(paths, p)
			}
			continue
		}
		if fileStatus.Worktree == git.Modified || fileStatus.Worktree == git.Added || fileStatus.Worktree == git.Untracked {
			if _, ok := seen[p]; !ok {
				seen[p] = struct{}{}
				paths = append(paths, p)
			}
		}
	}

	return paths
}
