package syncdata

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cespare/xxhash/v2"
	ig "github.com/sabhiram/go-gitignore"

	"make-sync/internal/config"
	"make-sync/internal/util"
)

// buildIncludeMatcher compiles a matcher that includes only paths matching the provided
// negation patterns (from .sync_ignore). Strategy: ignore everything via "**" then
// unignore the provided patterns (each prefixed with '!'), after preprocessing short
// tokens to include **/ variants similar to IgnoreCache.
func buildIncludeMatcher(negPatterns []string) *ig.GitIgnore {
	lines := []string{"**"} // ignore everything by default
	for _, p := range negPatterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// normalize to forward slashes
		p = filepath.ToSlash(p)
		// If pattern contains '/' or '**', keep as-is. Otherwise, add both forms.
		if strings.Contains(p, "/") || strings.Contains(p, "**") {
			lines = append(lines, "!"+p)
		} else {
			// add both pattern and **/pattern
			lines = append(lines, "!"+p)
			lines = append(lines, "!**/"+p)
		}
	}
	return ig.CompileIgnoreLines(lines...)
}

// CompareAndDownloadByIgnoreIncludes downloads only remote entries whose rel matches
// the include matcher created from .sync_ignore negation patterns.
func CompareAndDownloadByIgnoreIncludes(cfg *config.Config, localRoot string, negPatterns []string) ([]string, error) {
	// decide local root
	root := localRoot
	if root == "" {
		if cfg.LocalPath != "" {
			root = cfg.LocalPath
		} else if cfg.Devsync.Auth.LocalPath != "" {
			root = cfg.Devsync.Auth.LocalPath
		} else {
			wd, err := os.Getwd()
			if err != nil {
				return nil, fmt.Errorf("failed to determine working dir: %v", err)
			}
			root = wd
		}
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute root: %v", err)
	}

	// Download remote DB
	localDBPath, err := DownloadIndexDB(cfg, absRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to download remote DB: %v", err)
	}

	// Load remote DB
	remoteByRel := map[string]struct {
		Path  string
		Rel   string
		Size  int64
		Mod   int64
		Hash  string
		IsDir bool
	}{}

	db, err := sql.Open("sqlite", localDBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open remote DB: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT path, rel, size, mod_time, hash, is_dir FROM files`)
	if err != nil {
		return nil, fmt.Errorf("failed to query remote DB: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var pathStr, relStr, hashStr string
		var sizeInt, modNano int64
		var isDirInt int
		if err := rows.Scan(&pathStr, &relStr, &sizeInt, &modNano, &hashStr, &isDirInt); err != nil {
			continue
		}
		key := filepath.ToSlash(relStr)
		remoteByRel[key] = struct {
			Path  string
			Rel   string
			Size  int64
			Mod   int64
			Hash  string
			IsDir bool
		}{Path: pathStr, Rel: relStr, Size: sizeInt, Mod: modNano, Hash: hashStr, IsDir: isDirInt != 0}
	}

	// Build include matcher
	inc := buildIncludeMatcher(negPatterns)

	// Create IgnoreCache rooted at absRoot
	ic := NewIgnoreCache(absRoot)

	// Connect SSH for downloads
	sshCli, err := ConnectSSH(cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh connect failed: %v", err)
	}
	defer sshCli.Close()

	// deterministically iterate remote entries by sorted rel
	rels := make([]string, 0, len(remoteByRel))
	for r := range remoteByRel {
		rels = append(rels, r)
	}
	sort.Strings(rels)

	downloaded := []string{}
	examined := 0
	skippedIgnored := 0
	skippedUpToDate := 0
	downloadErrors := 0

	for _, rel := range rels {
		rm := remoteByRel[rel]
		if rm.IsDir {
			continue
		}
		relNorm := filepath.ToSlash(rel)
		// scope: only include if matcher says NOT ignored (since base is "**")
		if inc.MatchesPath(relNorm) {
			// ignored -> not included
			continue
		}

		examined++
		localPath := filepath.Join(absRoot, filepath.FromSlash(relNorm))

		if relNorm == ".sync_temp" || strings.HasPrefix(relNorm, ".sync_temp/") || strings.Contains(relNorm, "/.sync_temp/") {
			skippedIgnored++
			continue
		}
		if ic.Match(localPath, false) {
			skippedIgnored++
			continue
		}

		info, statErr := os.Stat(localPath)
		if statErr != nil {
			util.Default.Printf("⬇️  Downloading %s -> %s\n", buildRemotePath(cfg, relNorm), localPath)
			if err := sshCli.DownloadFile(localPath, buildRemotePath(cfg, relNorm)); err != nil {
				util.Default.Printf("❌ Failed to download %s: %v\n", buildRemotePath(cfg, relNorm), err)
				downloadErrors++
				continue
			}
			downloaded = append(downloaded, localPath)
			continue
		}
		if info.IsDir() {
			continue
		}

		// compute local hash
		localHash := ""
		if f, err := os.Open(localPath); err == nil {
			h := xxhash.New()
			if _, err := io.Copy(h, f); err == nil {
				localHash = fmt.Sprintf("%x", h.Sum(nil))
			}
			f.Close()
		}

		if strings.TrimSpace(rm.Hash) == "" || rm.Hash != localHash {
			util.Default.Printf("⬇️  Downloading %s -> %s\n", buildRemotePath(cfg, relNorm), localPath)
			if err := sshCli.DownloadFile(localPath, buildRemotePath(cfg, relNorm)); err != nil {
				util.Default.Printf("❌ Failed to download %s: %v\n", buildRemotePath(cfg, relNorm), err)
				downloadErrors++
				continue
			}
			downloaded = append(downloaded, localPath)
			continue
		}
		skippedUpToDate++
	}

	util.Default.Printf("🔁 Download via !patterns: examined=%d, downloaded=%d, skipped(ignored)=%d, skipped(up-to-date)=%d, errors=%d\n",
		examined, len(downloaded), skippedIgnored, skippedUpToDate, downloadErrors)

	return downloaded, nil
}

// CompareAndDownloadByIgnoreIncludesForce performs the same as above, with an additional
// delete pass: delete local files that match include patterns but do not exist in remote DB.
func CompareAndDownloadByIgnoreIncludesForce(cfg *config.Config, localRoot string, negPatterns []string) ([]string, error) {
	// First run the soft variant to download updates
	downloaded, err := CompareAndDownloadByIgnoreIncludes(cfg, localRoot, negPatterns)
	if err != nil {
		return downloaded, err
	}

	// decide local root
	root := localRoot
	if root == "" {
		if cfg.LocalPath != "" {
			root = cfg.LocalPath
		} else if cfg.Devsync.Auth.LocalPath != "" {
			root = cfg.Devsync.Auth.LocalPath
		} else {
			wd, err := os.Getwd()
			if err != nil {
				return downloaded, fmt.Errorf("failed to determine working dir: %v", err)
			}
			root = wd
		}
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return downloaded, fmt.Errorf("failed to resolve absolute root: %v", err)
	}

	// Load remote DB again to build set (or we could refactor soft to return it)
	localDBPath, err := DownloadIndexDB(cfg, absRoot)
	if err != nil {
		return downloaded, fmt.Errorf("failed to download remote DB: %v", err)
	}

	remoteExists := make(map[string]bool)
	db, err := sql.Open("sqlite", localDBPath)
	if err != nil {
		return downloaded, fmt.Errorf("failed to open remote DB: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT rel, is_dir FROM files`)
	if err != nil {
		return downloaded, fmt.Errorf("failed to query remote DB: %v", err)
	}
	for rows.Next() {
		var rel string
		var isDirInt int
		if err := rows.Scan(&rel, &isDirInt); err == nil {
			if isDirInt == 0 {
				remoteExists[filepath.ToSlash(rel)] = true
			}
		}
	}
	rows.Close()

	inc := buildIncludeMatcher(negPatterns)
	ic := NewIgnoreCache(absRoot)

	deleted := 0
	deleteErrors := 0

	// Walk entire tree (respect ignore) and delete files that match include but not in remoteExists
	filepath.WalkDir(absRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p == absRoot {
			return nil
		}
		rel, rerr := filepath.Rel(absRoot, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == ".sync_temp" || strings.HasPrefix(rel, ".sync_temp/") || strings.Contains(rel, "/.sync_temp/") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if ic.Match(p, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// include only if matcher says NOT ignored
		if inc.MatchesPath(rel) {
			return nil
		}
		if !remoteExists[rel] {
			if err := os.Remove(p); err != nil {
				util.Default.Printf("❌ Failed to delete %s: %v\n", p, err)
				deleteErrors++
			} else {
				util.Default.Printf("🗑️  Deleted local file (not in remote, include-scope): %s\n", p)
				deleted++
			}
		}
		return nil
	})

	util.Default.Printf("🧹 Download via !patterns (force): deleted=%d, errors=%d\n", deleted, deleteErrors)
	return downloaded, nil
}
