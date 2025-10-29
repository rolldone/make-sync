package syncdata

import (
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cespare/xxhash/v2"

	"make-sync/internal/config"
	"make-sync/internal/util"
)

// CompareAndUploadByIgnoreIncludes uploads only local files whose rel matches
// include patterns derived from .sync_ignore negation lines. Soft mode: no deletions.
func CompareAndUploadByIgnoreIncludes(cfg *config.Config, localRoot string, negPatterns []string) ([]string, error) {
	// determine local root
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

	// Load remote index DB
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
	rows.Close()

	// Build include matcher
	inc := buildIncludeMatcher(negPatterns)

	// Create IgnoreCache
	ic := NewIgnoreCache(absRoot)

	// Connect SSH for uploads
	sshCli, err := ConnectSSH(cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh connect failed: %v", err)
	}
	defer sshCli.Close()

	uploaded := make([]string, 0)
	var examined, skippedIgnored, skippedUpToDate, uploadErrors int

	// Walk local files (local-first)
	err = filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
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

		// Skip .sync_temp
		if rel == ".sync_temp" || strings.HasPrefix(rel, ".sync_temp/") || strings.Contains(rel, "/.sync_temp/") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// Respect ignore cache
		if ic.Match(p, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			skippedIgnored++
			return nil
		}
		if d.IsDir() {
			return nil
		}

		// Include only if NOT ignored by include-matcher (since base is **)
		if inc.MatchesPath(rel) {
			return nil
		}

		examined++

		// compute local hash
		localHash := ""
		if f, ferr := os.Open(p); ferr == nil {
			h := xxhash.New()
			if _, err := io.Copy(h, f); err == nil {
				localHash = fmt.Sprintf("%x", h.Sum(nil))
			}
			f.Close()
		}

		rm, exists := remoteByRel[rel]
		needUpload := false
		if !exists {
			needUpload = true
		} else if strings.TrimSpace(rm.Hash) == "" || rm.Hash != localHash {
			needUpload = true
		}

		if needUpload {
			util.Default.Printf("⬆️  Uploading %s -> %s\n", p, buildRemotePath(cfg, rel))
			if err := sshCli.SyncFile(p, buildRemotePath(cfg, rel)); err != nil {
				util.Default.Printf("❌ Failed to upload %s: %v\n", p, err)
				uploadErrors++
			} else {
				uploaded = append(uploaded, p)
			}
		} else {
			skippedUpToDate++
		}
		return nil
	})
	if err != nil {
		return uploaded, fmt.Errorf("walk error: %v", err)
	}

	util.Default.Printf("🔁 Upload via !patterns: examined=%d, uploaded=%d, skipped(ignored)=%d, skipped(up-to-date)=%d, errors=%d\n",
		examined, len(uploaded), skippedIgnored, skippedUpToDate, uploadErrors)

	return uploaded, nil
}

// CompareAndUploadByIgnoreIncludesForce: upload + delete remote files matching include
// patterns that were not seen locally (checked=0). Uses DB 'checked' column if present.
func CompareAndUploadByIgnoreIncludesForce(cfg *config.Config, localRoot string, negPatterns []string) ([]string, error) {
	// determine local root
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

	// Open DB and load remote entries
	db, err := sql.Open("sqlite", localDBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open remote DB: %v", err)
	}
	defer db.Close()

	remoteByRel := map[string]struct {
		Path  string
		Rel   string
		Size  int64
		Mod   int64
		Hash  string
		IsDir bool
	}{}

	rows, err := db.Query(`SELECT path, rel, size, mod_time, hash, is_dir FROM files`)
	if err != nil {
		return nil, fmt.Errorf("failed to query remote DB: %v", err)
	}
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
	rows.Close()

	// detect checked column availability
	hasChecked := false
	if cols, err := db.Query(`PRAGMA table_info(files)`); err == nil {
		for cols.Next() {
			var cid int
			var name, ctype string
			var notnull, pk int
			var dflt sql.NullString
			if err := cols.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err == nil {
				if strings.EqualFold(name, "checked") {
					hasChecked = true
				}
			}
		}
		cols.Close()
	}

	inc := buildIncludeMatcher(negPatterns)
	ic := NewIgnoreCache(absRoot)

	// Connect SSH for uploads/deletions
	sshCli, err := ConnectSSH(cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh connect failed: %v", err)
	}
	defer sshCli.Close()

	uploaded := make([]string, 0)
	checkedSet := make(map[string]struct{})
	var examined, skippedIgnored, skippedUpToDate, uploadErrors int

	// Helper to mark checked in DB and memory
	markChecked := func(rel string) {
		rel = filepath.ToSlash(rel)
		checkedSet[rel] = struct{}{}
		if hasChecked {
			if _, err := db.Exec(`UPDATE files SET checked=1 WHERE rel=?`, rel); err != nil {
				hasChecked = false
			}
		}
	}

	// Walk local files (local-first)
	err = filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
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
			skippedIgnored++
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if inc.MatchesPath(rel) {
			return nil
		}

		examined++
		// compute local hash
		localHash := ""
		if f, ferr := os.Open(p); ferr == nil {
			h := xxhash.New()
			if _, err := io.Copy(h, f); err == nil {
				localHash = fmt.Sprintf("%x", h.Sum(nil))
			}
			f.Close()
		}
		rm, exists := remoteByRel[rel]
		needUpload := false
		if !exists {
			needUpload = true
		} else if strings.TrimSpace(rm.Hash) == "" || rm.Hash != localHash {
			needUpload = true
		}
		if needUpload {
			util.Default.Printf("⬆️  Uploading %s -> %s\n", p, buildRemotePath(cfg, rel))
			if err := sshCli.SyncFile(p, buildRemotePath(cfg, rel)); err != nil {
				util.Default.Printf("❌ Failed to upload %s: %v\n", p, err)
				uploadErrors++
			} else {
				uploaded = append(uploaded, p)
			}
		} else {
			skippedUpToDate++
		}
		markChecked(rel)
		return nil
	})
	if err != nil {
		return uploaded, fmt.Errorf("walk error: %v", err)
	}

	util.Default.Printf("🔁 Upload via !patterns: examined=%d, uploaded=%d, skipped(ignored)=%d, skipped(up-to-date)=%d, errors=%d\n",
		examined, len(uploaded), skippedIgnored, skippedUpToDate, uploadErrors)

	// Deletion candidates: remote entries matching include that are not checked
	toDelete := make([]string, 0)
	for rel, meta := range remoteByRel {
		if meta.IsDir {
			continue
		}
		// scope by include matcher
		if inc.MatchesPath(rel) {
			// ignored by include baseline -> not in scope
			continue
		}
		// skip .sync_temp and local ignore
		if rel == ".sync_temp" || strings.HasPrefix(rel, ".sync_temp/") || strings.Contains(rel, "/.sync_temp/") {
			continue
		}
		localPath := filepath.Join(absRoot, filepath.FromSlash(rel))
		if ic.Match(localPath, false) {
			continue
		}
		// checked?
		checked := false
		if hasChecked {
			var c int
			if err := db.QueryRow(`SELECT checked FROM files WHERE rel=?`, rel).Scan(&c); err == nil {
				checked = (c != 0)
			} else {
				checked = false
			}
		} else {
			_, checked = checkedSet[rel]
		}
		if !checked {
			toDelete = append(toDelete, rel)
		}
	}

	// Execute remote deletions
	deleted := 0
	deleteErrors := 0
	osTarget := strings.ToLower(strings.TrimSpace(cfg.Devsync.OSTarget))
	for _, rel := range toDelete {
		remotePath := buildRemotePath(cfg, rel)
		var cmd string
		if strings.Contains(osTarget, "win") {
			rp := strings.ReplaceAll(remotePath, "/", "\\")
			cmd = fmt.Sprintf("cmd.exe /C if exist \"%s\" del /f /q \"%s\"", rp, rp)
		} else {
			cmd = fmt.Sprintf("rm -f %s", shellQuote(remotePath))
		}
		if err := sshCli.RunCommand(cmd); err != nil {
			util.Default.Printf("❌ Failed to delete remote %s: %v\n", remotePath, err)
			deleteErrors++
		} else {
			util.Default.Printf("🗑️  Deleted remote file (not in local, include-scope): %s\n", remotePath)
			deleted++
		}
	}

	util.Default.Printf("🧹 Upload via !patterns (force): candidates=%d, deleted=%d, errors=%d\n", len(toDelete), deleted, deleteErrors)
	// Prune empty remote directories left behind by remote deletions (POSIX only)
	// Delegate to agent-side prune to handle platform differences and respect ignores.
	_ = pruneRemoteEmptyDirs(sshCli, cfg, nil)

	return uploaded, nil
}
