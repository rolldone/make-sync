package syncdata

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
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

// ClearCache invalidates all cached matchers and raw lines, forcing reload on next Match call.
func (c *IgnoreCache) ClearCache() {
	c.cache = map[string]*ig.GitIgnore{}
	c.rawLinesCache = map[string][]string{}
}

// Match returns true if the given path (absolute or relative) should be ignored.
// isDir indicates whether the path refers to a directory.
func (c *IgnoreCache) Match(path string, isDir bool) bool {
	// PRIORITY CHECK: If file matches any negation pattern (!), include immediately
	if c.matchesPriorityIncludes(path, isDir) {
		return false // Not ignored - priority include
	}

	// default ignores (always skip)
	defaultIgnores := []string{".sync_temp", "make-sync.yaml", ".sync_ignore", ".sync_collections"}

	base := filepath.Base(path)
	for _, di := range defaultIgnores {
		if strings.EqualFold(di, base) {
			return true
		}
	}

	// Also ignore any path containing .sync_temp
	if strings.Contains(path, ".sync_temp") {
		return true
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
	if m, ok := c.cache[dir]; ok && !strings.Contains(path, ".git") {
		if m == nil {
			return false
		}
		relp, _ := filepath.Rel(c.Root, path)
		relp = filepath.ToSlash(relp)

		var result bool
		if strings.HasPrefix(strings.ToLower(runtime.GOOS), "windows") {
			result = m.MatchesPath(strings.ToLower(relp))
			if !result {
				result = m.MatchesPath(strings.ToLower(filepath.ToSlash(filepath.Base(path))))
			}
		} else {
			result = m.MatchesPath(relp)
			if !result {
				result = m.MatchesPath(filepath.ToSlash(filepath.Base(path)))
			}
		}
		return result
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
		} else {
			// .sync_ignore not found, mark empty to avoid repeated stat calls
			c.rawLinesCache[td] = nil
		}
	}

	if len(cumulative) == 0 {
		c.cache[dir] = nil
		return false
	}

	m := ig.CompileIgnoreLines(cumulative...)
	c.cache[dir] = m
	relp, _ := filepath.Rel(c.Root, path)
	relp = filepath.ToSlash(relp)

	if strings.Contains(path, ".git") {
		// Git path handling - no special logic needed
	}

	var result bool
	if strings.HasPrefix(strings.ToLower(runtime.GOOS), "windows") {
		result = m.MatchesPath(strings.ToLower(relp))
		if !result {
			result = m.MatchesPath(strings.ToLower(filepath.ToSlash(filepath.Base(path))))
		}
	} else {
		result = m.MatchesPath(relp)
		if !result {
			baseResult := m.MatchesPath(filepath.ToSlash(filepath.Base(path)))
			result = baseResult
		}
	}

	if strings.Contains(path, ".git") {
	}

	if strings.Contains(path, "docker-compose.yml") {
		if len(cumulative) <= 10 {
		}
	}

	if strings.Contains(path, ".git") {
	}

	return result
}

// matchesPriorityIncludes checks if a path matches any negation patterns (!) for priority inclusion
func (c *IgnoreCache) matchesPriorityIncludes(path string, isDir bool) bool {
	// Debug for docker-compose.yml
	if strings.Contains(path, "docker-compose.yml") {
	}
	dir := path
	if !isDir {
		dir = filepath.Dir(path)
	}

	// Build ancestor list from Root -> dir (same logic as Match)
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

	// Collect only negation patterns (!) from all .sync_ignore files
	var priorityPatterns []string
	for _, td := range ancestors {
		syncIgnorePath := filepath.Join(td, ".sync_ignore")
		if _, err := os.Stat(syncIgnorePath); err == nil {
			if c.rawLinesCache[td] == nil {
				data, rerr := os.ReadFile(syncIgnorePath)
				if rerr != nil {
					continue
				}
				lines := strings.Split(string(data), "\n")
				var processedLines []string
				for _, ln := range lines {
					l := strings.TrimSpace(ln)
					if l == "" || strings.HasPrefix(l, "#") {
						continue
					}
					// Only collect negation patterns for priority check
					if strings.HasPrefix(l, "!") {
						l = strings.TrimPrefix(l, "!")
						l = filepath.ToSlash(l)
						processedLines = append(processedLines, "!"+l)
						// Add recursive variant for simple patterns
						if !strings.Contains(l, "/") && !strings.Contains(l, "**") {
							processedLines = append(processedLines, "!**/"+l)
						}
					}
				}
				c.rawLinesCache[td] = processedLines
			}
			// Add cached negation patterns
			for _, line := range c.rawLinesCache[td] {
				if strings.HasPrefix(line, "!") {
					priorityPatterns = append(priorityPatterns, line)
				}
			}
		}
	}

	if len(priorityPatterns) == 0 {
		if strings.Contains(path, "docker-compose.yml") {
		}
		return false
	}

	if strings.Contains(path, "docker-compose.yml") {
	}

	// Test against priority patterns
	m := ig.CompileIgnoreLines(priorityPatterns...)
	relp, _ := filepath.Rel(dir, path)
	relp = filepath.ToSlash(relp)

	if strings.Contains(path, "docker-compose.yml") {
	}

	if strings.HasPrefix(strings.ToLower(runtime.GOOS), "windows") {
		result := m.MatchesPath(strings.ToLower(relp))
		if !result {
			result = m.MatchesPath(strings.ToLower(filepath.ToSlash(filepath.Base(path))))
		}
		if strings.Contains(path, "docker-compose.yml") {
		}
		return result
	}
	result := m.MatchesPath(relp)
	if !result {
		result = m.MatchesPath(filepath.ToSlash(filepath.Base(path)))
	}
	if strings.Contains(path, "docker-compose.yml") {
	}
	return result
}

// GetAllPatterns returns all ignore patterns (including negation patterns)
// collected from .sync_ignore files in the project hierarchy.
// This is used for sending patterns to remote agents.
func (c *IgnoreCache) GetAllPatterns() []string {
	// Use a set to dedupe patterns
	found := map[string]struct{}{}

	// Add default ignores first
	defaults := []string{".sync_temp", "make-sync.yaml", ".sync_ignore", ".sync_collections"}
	for _, d := range defaults {
		found[d] = struct{}{}
	}

	// Walk the tree and collect all patterns
	_ = filepath.WalkDir(c.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), ".sync_ignore") {
			data, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			lines := strings.Split(string(data), "\n")
			for _, ln := range lines {
				l := strings.TrimSpace(ln)
				if l == "" || strings.HasPrefix(l, "#") {
					continue
				}
				neg := false
				if strings.HasPrefix(l, "!") {
					neg = true
					l = strings.TrimPrefix(l, "!")
				}
				// Normalize to forward slashes
				l = filepath.ToSlash(l)
				if strings.Contains(l, "/") || strings.Contains(l, "**") {
					if neg {
						found["!"+l] = struct{}{}
					} else {
						found[l] = struct{}{}
					}
					continue
				}
				// Add both simple and recursive variants
				if neg {
					found["!"+l] = struct{}{}
					found["!**/"+l] = struct{}{}
				} else {
					found[l] = struct{}{}
					found["**/"+l] = struct{}{}
				}
			}
		}
		return nil
	})

	// Convert to sorted slice
	out := make([]string, 0, len(found))
	for k := range found {
		out = append(out, k)
	}
	sort.Strings(out)

	return out
}
