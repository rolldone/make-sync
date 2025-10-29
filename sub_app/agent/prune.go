package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"sync-agent/internal/util"
)

// PruneResult holds summary of prune operation
type PruneResult struct {
	Removed []string
	Failed  []PruneFailure
	DryRun  bool
}

// PruneFailure records a failed removal
type PruneFailure struct {
	Path  string
	Error error
}

// performPrune walks prefixes and removes empty directories bottom-up.
// It respects ignore rules via SimpleIgnoreCache unless bypassIgnore is true.
func performPrune(config *AgentConfig, bypassIgnore bool, prefixes []string, dryRun bool) (PruneResult, error) {
	res := PruneResult{Removed: []string{}, Failed: []PruneFailure{}, DryRun: dryRun}

	root, err := os.Getwd()
	if err != nil {
		return res, fmt.Errorf("failed to get working dir: %v", err)
	}

	// Ensure prefixes default to root if empty
	if len(prefixes) == 0 {
		prefixes = []string{""}
	}

	// NOTE: conservative prune: we only remove truly empty directories.
	// Ignore/cache handling is not used here — directories that contain
	// ignored files will NOT be removed.

	// collect directories to consider
	dirSet := map[string]struct{}{}
	for _, p := range prefixes {
		p = stringsTrimSlashes(p)
		start := root
		if p != "" {
			start = filepath.Join(root, filepath.FromSlash(p))
		}
		// walk and collect dirs
		filepath.WalkDir(start, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				// ignore and continue
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			dirSet[path] = struct{}{}
			return nil
		})
	}

	// Turn set into slice and sort by path length descending (deepest first)
	dirs := make([]string, 0, len(dirSet))
	for d := range dirSet {
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i]) > len(dirs[j])
	})

	// skip special dirs and any directories under them (e.g. .sync_temp/*)
	for _, d := range dirs {
		// never attempt to remove repository root
		if samePath(d, root) {
			continue
		}

		// compute path relative to working root so we can detect subpaths like
		// .sync_temp/... or .git/...
		relToRoot, rerr := filepath.Rel(root, d)
		if rerr != nil {
			// If we can't compute relative path, be conservative and skip
			continue
		}
		relToRoot = filepath.ToSlash(relToRoot)

		// Skip any path that is .sync_temp or under .sync_temp, likewise .git
		if relToRoot == ".sync_temp" || strings.HasPrefix(relToRoot, ".sync_temp/") {
			continue
		}
		if relToRoot == ".git" || strings.HasPrefix(relToRoot, ".git/") {
			continue
		}

		// read entries
		entries, rerr := os.ReadDir(d)
		if rerr != nil {
			res.Failed = append(res.Failed, PruneFailure{Path: d, Error: rerr})
			continue
		}

		// Conservative behavior: only prune directories that are truly empty
		// (i.e., contain zero directory entries). If any entry exists (including
		// ones matched by ignore rules), do not remove the directory. This
		// protects user-intended ignored files and avoids surprising removals.
		if len(entries) != 0 {
			continue
		}

		if dryRun {
			res.Removed = append(res.Removed, d)
			continue
		}

		// attempt remove
		if err := os.Remove(d); err != nil {
			// If directory disappeared between our checks, treat as non-fatal
			if os.IsNotExist(err) {
				// Already removed by someone/something else; ignore
				continue
			}
			res.Failed = append(res.Failed, PruneFailure{Path: d, Error: err})
		} else {
			res.Removed = append(res.Removed, d)
			util.Default.ClearLine()
			util.Default.Printf("🧹 Pruned empty dir: %s\n", d)
		}
		// small sleep to avoid busy-loops on large deletes
		time.Sleep(10 * time.Millisecond)
	}

	return res, nil
}

// printPruneResult prints a machine-readable JSON result first, then a human summary
func printPruneResult(res PruneResult) error {
	type Failure struct {
		Path  string `json:"path"`
		Error string `json:"error"`
	}
	out := struct {
		Removed []string  `json:"removed"`
		Failed  []Failure `json:"failed"`
		DryRun  bool      `json:"dry_run"`
	}{
		Removed: res.Removed,
		Failed:  []Failure{},
		DryRun:  res.DryRun,
	}
	for _, f := range res.Failed {
		out.Failed = append(out.Failed, Failure{Path: f.Path, Error: f.Error.Error()})
	}
	// Marshal compact JSON for machine parsing
	data, err := json.Marshal(out)
	if err != nil {
		return err
	}
	// Print JSON line first
	fmt.Println(string(data))

	// Then human-friendly summary
	if len(res.Removed) > 0 {
		fmt.Printf("🧹 Pruned empty dirs: %d\n", len(res.Removed))
		for _, p := range res.Removed {
			fmt.Printf(" - %s\n", p)
		}
	} else {
		fmt.Println("🧹 No directories pruned")
	}
	if len(res.Failed) > 0 {
		fmt.Printf("⚠️  Prune failures: %d\n", len(res.Failed))
		for _, f := range res.Failed {
			fmt.Printf(" ! %s -> %v\n", f.Path, f.Error)
		}
	}
	return nil
}

// helper: stringsTrimSlashes removes leading/trailing slashes and normalizes empty
func stringsTrimSlashes(s string) string {
	if s == "" {
		return ""
	}
	s = filepath.ToSlash(s)
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimSuffix(s, "/")
	return s
}

// helper: samePath checks if two paths refer to same cleaned absolute path
func samePath(a, b string) bool {
	aa, aerr := filepath.Abs(a)
	if aerr != nil {
		aa = a
	}
	bb, berr := filepath.Abs(b)
	if berr != nil {
		bb = b
	}
	aa = filepath.Clean(aa)
	bb = filepath.Clean(bb)
	return aa == bb
}
