package indexer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// SimpleIgnoreCache provides per-directory cascading .sync_ignore support for the agent.
// This is a lightweight implementation (no external deps) that supports:
// - comments (#) and empty lines
// - negation with leading '!'
// - simple glob patterns and basename fallback
// - preprocessing of patterns like '*.log' to also match '**/*.log'
// It's intentionally conservative and matches the client's semantics closely enough.
type SimpleIgnoreCache struct {
	Root string
	raw  map[string][]string // directory -> preprocessed lines
	// if authoritative is true, use only c.raw[Root] and do not scan disk for .sync_ignore
	authoritative bool
	// manualTransfer contains paths that should not be ignored even if they match ignore patterns
	manualTransfer []string
}

func NewSimpleIgnoreCache(root string) *SimpleIgnoreCache {
	c := &SimpleIgnoreCache{Root: root, raw: map[string][]string{}}
	// attempt to read preprocessed ignores from .sync_temp/config.json under root
	cfgPath := filepath.Join(root, ".sync_temp", "config.json")
	if data, err := os.ReadFile(cfgPath); err == nil {
		// try to parse JSON structure with ignores and manual_transfer
		type devsyncCfg struct {
			Devsync struct {
				Ignores        []string `json:"ignores"`
				ManualTransfer []string `json:"manual_transfer"`
			} `json:"devsync"`
		}
		var dc devsyncCfg
		if jerr := json.Unmarshal(data, &dc); jerr == nil {
			if len(dc.Devsync.Ignores) > 0 {
				// store into raw for root directory so they are applied globally
				c.raw[root] = append(c.raw[root], dc.Devsync.Ignores...)
				// mark authoritative so we do not scan per-directory .sync_ignore files
				c.authoritative = true
			}
			// store manual transfer paths
			c.manualTransfer = dc.Devsync.ManualTransfer
		}
	}
	return c
}

// Match returns true if path should be ignored. path may be absolute or relative.
func (c *SimpleIgnoreCache) Match(path string, isDir bool) bool {
	// default ignores
	defaults := []string{".sync_temp", "make-sync.yaml", ".sync_ignore", ".sync_collections"}
	base := filepath.Base(path)
	for _, d := range defaults {
		if strings.EqualFold(d, base) {
			return true
		}
	}

	// determine directory to look up .sync_ignore (if path is file, use its dir)
	dir := path
	if !isDir {
		dir = filepath.Dir(path)
	}

	// build ancestor list from root -> dir
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
	// reverse to root->dir
	for i, j := 0, len(ancestors)-1; i < j; i, j = i+1, j-1 {
		ancestors[i], ancestors[j] = ancestors[j], ancestors[i]
	}

	// collect cumulative rules
	cumulative := []string{}
	if c.authoritative {
		// if authoritative list is present, only use root-level rules
		if lines, ok := c.raw[c.Root]; ok {
			cumulative = append(cumulative, lines...)
		}
	} else {
		for _, a := range ancestors {
			if lines, ok := c.raw[a]; ok {
				cumulative = append(cumulative, lines...)
				continue
			}
			syncPath := filepath.Join(a, ".sync_ignore")
			if _, err := os.Stat(syncPath); err == nil {
				data, rerr := os.ReadFile(syncPath)
				if rerr == nil {
					raw := strings.Split(string(data), "\n")
					// preprocess
					lines := []string{}
					for _, ln := range raw {
						l := strings.TrimSpace(ln)
						if l == "" || strings.HasPrefix(l, "#") {
							continue
						}
						neg := false
						if strings.HasPrefix(l, "!") {
							neg = true
							l = strings.TrimPrefix(l, "!")
						}
						// if pattern contains slash or ** keep as-is
						if strings.Contains(l, "/") || strings.Contains(l, "**") {
							if neg {
								lines = append(lines, "!"+l)
							} else {
								lines = append(lines, l)
							}
							continue
						}
						// otherwise add both forms: pattern and **/pattern
						if neg {
							lines = append(lines, "!"+l)
							lines = append(lines, "!**/"+l)
						} else {
							lines = append(lines, l)
							lines = append(lines, "**/"+l)
						}
					}
					c.raw[a] = lines
					cumulative = append(cumulative, lines...)
					continue
				}
			}
			c.raw[a] = nil
		}
	}

	if len(cumulative) == 0 {
		return false
	}

	// rel path relative to each directory will be tested; we'll compute rel to root
	relToRoot, err := filepath.Rel(c.Root, path)
	if err != nil {
		relToRoot = path
	}
	relToRoot = filepath.ToSlash(relToRoot)
	baseName := filepath.ToSlash(filepath.Base(path))

	// last matching rule wins
	matched := false
	for _, pat := range cumulative {
		neg := false
		p := pat
		if strings.HasPrefix(p, "!") {
			neg = true
			p = strings.TrimPrefix(p, "!")
		}
		// direct match against rel
		ok := false
		if matchedGlob(p, relToRoot) || matchedGlob(p, baseName) {
			ok = true
		}
		if ok {
			if neg {
				matched = false
			} else {
				matched = true
			}
		}
	}
	return matched
}

// MatchWithManualTransfer returns true if path should be ignored, but respects manual_transfer context.
// If path belongs to a manual transfer endpoint, it will not be ignored even if it matches ignore patterns.
func (c *SimpleIgnoreCache) MatchWithManualTransfer(path string, isDir bool) bool {
	// Check if path belongs to manual transfer endpoint
	for _, endpoint := range c.manualTransfer {
		// Normalize endpoint to use forward slashes
		endpoint = filepath.ToSlash(endpoint)

		// Check if path is the endpoint itself or sub-path of endpoint
		pathRel, err := filepath.Rel(c.Root, path)
		if err != nil {
			pathRel = path
		}
		pathRel = filepath.ToSlash(pathRel)

		// If path is endpoint itself or starts with endpoint/
		if pathRel == endpoint || strings.HasPrefix(pathRel, endpoint+"/") {
			// Belongs to manual transfer - don't ignore
			return false
		}
	}

	// Not in manual transfer - use normal ignore logic
	return c.Match(path, isDir)
}

// matchedGlob is a helper that tries filepath.Match with pattern variants.
func matchedGlob(pattern, target string) bool {
	// Use filepath.Match which treats path separator as '/' on unix; ensure slashes
	p := filepath.ToSlash(pattern)
	t := filepath.ToSlash(target)
	m, _ := filepath.Match(p, t)
	if m {
		return true
	}
	// also try match where pattern may start with **/
	if strings.HasPrefix(p, "**/") {
		m2, _ := filepath.Match(strings.TrimPrefix(p, "**/"), t)
		if m2 {
			return true
		}
	}
	return false
}
