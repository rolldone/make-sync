package syncdata

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	ig "github.com/sabhiram/go-gitignore"
)

// IgnoreCache caches compiled .sync_ignore matchers per directory and provides
// cascading ancestor matching similar to .gitignore semantics.
type IgnoreCache struct {
	Root  string
	cache map[string]*ig.GitIgnore
	// rawLinesCache stores the preprocessed lines for a directory's .sync_ignore (not cumulative)
	rawLinesCache map[string][]string
}

// NewIgnoreCache creates an IgnoreCache rooted at absRoot.
func NewIgnoreCache(absRoot string) *IgnoreCache {
	return &IgnoreCache{Root: absRoot, cache: map[string]*ig.GitIgnore{}, rawLinesCache: map[string][]string{}}
}

// Match returns true if the given path (absolute or relative) should be ignored.
// isDir indicates whether the path refers to a directory.
func (c *IgnoreCache) Match(path string, isDir bool) bool {
	// default ignores (always skip)
	defaultIgnores := []string{".sync_temp", "make-sync.yaml", ".sync_ignore", ".sync_collections"}

	base := filepath.Base(path)
	for _, di := range defaultIgnores {
		if strings.EqualFold(di, base) {
			return true
		}
	}

	dir := path
	if !isDir {
		dir = filepath.Dir(path)
	}

	// Build ancestor list from Root -> dir
	var ancestors []string
	cur := dir
	for {
		ancestors = append(ancestors, cur)
		if cur == c.Root || cur == string(os.PathSeparator) {
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	for i, j := 0, len(ancestors)-1; i < j; i, j = i+1, j-1 {
		ancestors[i], ancestors[j] = ancestors[j], ancestors[i]
	}

	// Build or reuse a cumulative matcher for the target directory 'dir'
	if m, ok := c.cache[dir]; ok {
		if m == nil {
			return false
		}
		relp, _ := filepath.Rel(dir, path)
		relp = filepath.ToSlash(relp)
		if strings.HasPrefix(strings.ToLower(runtime.GOOS), "windows") {
			if m.MatchesPath(strings.ToLower(relp)) {
				return true
			}
			return m.MatchesPath(strings.ToLower(filepath.ToSlash(filepath.Base(path))))
		}
		if m.MatchesPath(relp) {
			return true
		}
		return m.MatchesPath(filepath.ToSlash(filepath.Base(path)))
	}

	// Collect preprocessed lines from each ancestor in order
	cumulative := []string{}
	for _, td := range ancestors {
		if lines, ok := c.rawLinesCache[td]; ok {
			cumulative = append(cumulative, lines...)
			continue
		}
		syncIgnorePath := filepath.Join(td, ".sync_ignore")
		if _, err := os.Stat(syncIgnorePath); err == nil {
			data, rerr := os.ReadFile(syncIgnorePath)
			if rerr == nil {
				rawLines := strings.Split(string(data), "\n")
				// preprocess lines: convert patterns like '*.log' to '**/*.log' so they match in subdirs
				lines := make([]string, 0, len(rawLines)*2)
				for _, ln := range rawLines {
					l := strings.TrimSpace(ln)
					if l == "" || strings.HasPrefix(l, "#") {
						lines = append(lines, ln)
						continue
					}
					neg := false
					if strings.HasPrefix(l, "!") {
						neg = true
						l = strings.TrimPrefix(l, "!")
					}
					// if pattern contains a slash or starts with a leading slash or already contains **, leave it
					if strings.Contains(l, "/") || strings.HasPrefix(l, "/") || strings.Contains(l, "**") {
						if neg {
							lines = append(lines, "!"+l)
						} else {
							lines = append(lines, l)
						}
						continue
					}
					// otherwise add both forms: pattern and **/pattern so it matches in any subtree
					if neg {
						lines = append(lines, "!"+l)
						lines = append(lines, "!**/"+l)
					} else {
						lines = append(lines, l)
						lines = append(lines, "**/"+l)
					}
				}
				c.rawLinesCache[td] = lines
				cumulative = append(cumulative, lines...)
				continue
			}
		}
		// mark empty to avoid repeated stat calls
		c.rawLinesCache[td] = nil
	}

	if len(cumulative) == 0 {
		c.cache[dir] = nil
		return false
	}

	m := ig.CompileIgnoreLines(cumulative...)
	c.cache[dir] = m
	relp, _ := filepath.Rel(dir, path)
	relp = filepath.ToSlash(relp)
	if strings.HasPrefix(strings.ToLower(runtime.GOOS), "windows") {
		if m.MatchesPath(strings.ToLower(relp)) {
			return true
		}
		return m.MatchesPath(strings.ToLower(filepath.ToSlash(filepath.Base(path))))
	}
	if m.MatchesPath(relp) {
		return true
	}
	return m.MatchesPath(filepath.ToSlash(filepath.Base(path)))
}
