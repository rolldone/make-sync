package indexer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	ig "github.com/sabhiram/go-gitignore"
)

// defaultIgnores are always applied on top of any ignore rules.
var defaultIgnores = []string{
	".sync_temp",
	"make-sync.yaml",
	".sync_ignore",
	".sync_collections",
}

// SimpleIgnoreCache provides ignore matching for the agent using go-gitignore,
// consistent with the make-sync client. It reads patterns from either
// .sync_temp/config.json (authoritative) or by scanning .sync_ignore files on disk.
type SimpleIgnoreCache struct {
	Root    string
	matcher *ig.GitIgnore
	// manualTransfer contains paths that should not be ignored even if they'd match
	manualTransfer []string
}

// NewSimpleIgnoreCache creates a cache for the given root directory.
func NewSimpleIgnoreCache(root string) *SimpleIgnoreCache {
	c := &SimpleIgnoreCache{Root: root}

	var ignoreLines []string

	// Try authoritative ignores from .sync_temp/config.json
	cfgPath := filepath.Join(root, ".sync_temp", "config.json")
	if data, err := os.ReadFile(cfgPath); err == nil {
		type devsyncCfg struct {
			Devsync struct {
				Ignores        []string `json:"ignores"`
				ManualTransfer []string `json:"manual_transfer"`
			} `json:"devsync"`
		}
		var dc devsyncCfg
		if json.Unmarshal(data, &dc) == nil {
			if len(dc.Devsync.Ignores) > 0 {
				ignoreLines = dc.Devsync.Ignores
			}
			c.manualTransfer = dc.Devsync.ManualTransfer
		}
	}

	// Fallback: scan .sync_ignore files on disk
	if len(ignoreLines) == 0 {
		ignoreLines = collectSyncIgnoreFiles(root)
	}

	// Ensure negations come last so they override positive patterns (gitignore semantics: last match wins)
	ignoreLines = negationsLast(ignoreLines)

	// Default ignores always go first; config/disk patterns override as last-match-wins
	allLines := append(defaultIgnores, ignoreLines...)
	c.matcher = ig.CompileIgnoreLines(allLines...)
	return c
}

// collectSyncIgnoreFiles walks root and aggregates all .sync_ignore patterns,
// skipping .sync_temp and .git subtrees.
func collectSyncIgnoreFiles(root string) []string {
	var all []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if base == ".sync_temp" || base == ".git" {
			return filepath.SkipDir
		}
		sp := filepath.Join(path, ".sync_ignore")
		data, rerr := os.ReadFile(sp)
		if rerr != nil {
			return nil
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			all = append(all, line)
		}
		return nil
	})
	return all
}

// negationsLast moves all negation patterns (starting with !) to the end of the slice.
// This ensures gitignore semantics: negations override earlier positive patterns.
func negationsLast(lines []string) []string {
	pos := make([]string, 0, len(lines))
	neg := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.HasPrefix(l, "!") {
			neg = append(neg, l)
		} else {
			pos = append(pos, l)
		}
	}
	return append(pos, neg...)
}

// Match returns true if path should be ignored.
// path may be absolute; it will be resolved relative to Root.
// When isDir is true, also checks with trailing "/" so patterns like !/app/
// can correctly negate directory ignores.
func (c *SimpleIgnoreCache) Match(path string, isDir bool) bool {
	rel, err := filepath.Rel(c.Root, path)
	if err != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)
	if c.matcher.MatchesPath(rel) {
		// If it's a directory, also check with trailing "/" — a negation
		// like !/app/ should be able to recover the directory.
		if isDir {
			return c.matcher.MatchesPath(rel + "/")
		}
		return true
	}
	return false
}

// MatchWithManualTransfer returns true if path should be ignored, unless it
// belongs to a manual_transfer endpoint.
func (c *SimpleIgnoreCache) MatchWithManualTransfer(path string, isDir bool) bool {
	// manual transfer override: never ignore files inside a manual_transfer endpoint
	for _, endpoint := range c.manualTransfer {
		endpoint = filepath.ToSlash(endpoint)
		rel, err := filepath.Rel(c.Root, path)
		if err != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		if rel == endpoint || strings.HasPrefix(rel, endpoint+"/") {
			return false
		}
	}
	return c.Match(path, isDir)
}
