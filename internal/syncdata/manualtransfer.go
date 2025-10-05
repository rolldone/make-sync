package syncdata

import (
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"make-sync/internal/config"
	"make-sync/internal/util"

	"github.com/cespare/xxhash/v2"
)

// CompareAndUploadManualTransfer uploads only within the provided manual-transfer
// prefixes. Each prefix is treated as its own subtree (two-head-loop), so we
// never prune ancestor directories unintentionally. Prefixes must be relative
// (forward-slash) to localRoot. If prefixes is empty, falls back to full upload.
func CompareAndUploadManualTransfer(cfg *config.Config, localRoot string, prefixes []string) ([]string, error) {
	if len(prefixes) == 0 {
		return CompareAndUploadByHash(cfg, localRoot)
	}

	// determine abs root
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

	// Download remote DB into local .sync_temp
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

	// Create IgnoreCache
	ic := NewIgnoreCache(absRoot)

	// Connect SSH for uploads
	sshCli, err := ConnectSSH(cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh connect failed: %v", err)
	}
	defer sshCli.Close()

	// Prepare checked file path
	syncTemp := filepath.Join(absRoot, ".sync_temp")
	if err := os.MkdirAll(syncTemp, 0755); err != nil {
		return nil, fmt.Errorf("failed to create local .sync_temp: %v", err)
	}
	// JSON checked file deprecated; no longer persisted here

	uploaded := make([]string, 0)
	visited := make(map[string]struct{}) // rels to avoid duplicates across prefixes

	var examined, skippedIgnored, skippedUpToDate, uploadErrors int

	// Helper function to check if a relative path belongs to an explicit manual transfer endpoint
	// If it does, ignore patterns should NOT be applied to this path
	isExplicitEndpoint := func(relPath string) bool {
		for _, pr := range prefixes {
			// Normalize prefix by removing trailing slash for comparison
			normalizedPr := strings.TrimSuffix(strings.TrimPrefix(pr, "/"), "/")
			if normalizedPr == "" || relPath == normalizedPr || strings.HasPrefix(relPath, normalizedPr+"/") {
				return true
			}
		}
		return false
	}

	// two-head-loop: iterate each subtree separately
	for _, pr := range prefixes {
		pr = strings.TrimPrefix(pr, "/")
		start := filepath.Join(absRoot, filepath.FromSlash(pr))

		info, err := os.Stat(start)
		if err != nil {
			// If start doesn't exist, nothing to upload from this subtree
			continue
		}

		if !info.IsDir() {
			// single file case
			rel, rerr := filepath.Rel(absRoot, start)
			if rerr != nil {
				continue
			}
			rel = filepath.ToSlash(rel)

			// Skip .sync_temp or ignored
			if rel == ".sync_temp" || strings.HasPrefix(rel, ".sync_temp/") || strings.Contains(rel, "/.sync_temp/") {
				continue
			}
			if ic.Match(start, false) {
				skippedIgnored++
				continue
			}

			if _, seen := visited[rel]; seen {
				continue
			}
			visited[rel] = struct{}{}

			examined++
			// compute local hash
			localHash := ""
			if f, ferr := os.Open(start); ferr == nil {
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
				util.Default.Printf("⬆️  Uploading %s -> %s\n", start, buildRemotePath(cfg, rel))
				if err := sshCli.SyncFile(start, buildRemotePath(cfg, rel)); err != nil {
					util.Default.Printf("❌ Failed to upload %s: %v\n", start, err)
					uploadErrors++
				} else {
					uploaded = append(uploaded, start)
				}
			} else {
				skippedUpToDate++
			}

			// no-op: JSON checked file removed
			continue
		}

		// directory subtree: WalkDir within start only
		filepath.WalkDir(start, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			// compute rel path
			rel, rerr := filepath.Rel(absRoot, p)
			if rerr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)

			if p == start && d.IsDir() {
				// Context-aware ignore check for root directory
				if !isExplicitEndpoint(rel) && ic.Match(p, true) {
					return filepath.SkipDir
				}
				return nil
			}

			// Skip .sync_temp always
			if rel == ".sync_temp" || strings.HasPrefix(rel, ".sync_temp/") || strings.Contains(rel, "/.sync_temp/") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			// Context-aware ignore check: don't apply ignore patterns to explicit endpoints
			if !isExplicitEndpoint(rel) && ic.Match(p, d.IsDir()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				skippedIgnored++
				return nil
			}

			if d.IsDir() {
				return nil
			}

			if _, seen := visited[rel]; seen {
				return nil
			}
			visited[rel] = struct{}{}

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

			// no-op: JSON checked file removed
			return nil
		})
	}

	util.Default.Printf("🔁 Local files examined: %d, uploaded: %d, skipped(ignored): %d, skipped(up-to-date): %d, upload errors: %d\n",
		examined, len(uploaded), skippedIgnored, skippedUpToDate, uploadErrors)

	return uploaded, nil
}

// CompareAndDownloadManualTransfer downloads only entries whose rel starts with
// any of the provided manual-transfer prefixes. This is effectively a thin
// wrapper over CompareAndDownloadByHashWithFilter, but exists for clarity and
// future specialization.
func CompareAndDownloadManualTransfer(cfg *config.Config, localRoot string, prefixes []string) ([]string, error) {
	return CompareAndDownloadByHashWithFilter(cfg, localRoot, prefixes)
}

// CompareAndDownloadManualTransferParallel downloads files with bounded concurrency (max 5 parallel transfers)
// This is the parallel version of CompareAndDownloadManualTransfer
func CompareAndDownloadManualTransferParallel(cfg *config.Config, localRoot string, prefixes []string) ([]string, error) {
	// if prefixes empty, call existing
	if len(prefixes) == 0 {
		return CompareAndDownloadByHash(cfg, localRoot)
	}
	// reuse existing function but filter remote entries during download
	// determine local root (same as other function)
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
		// check if key matches any prefix
		matched := false
		for _, p := range prefixes {
			pp := strings.TrimPrefix(p, "/")
			if pp == "" {
				matched = true
				break
			}
			if strings.HasPrefix(key, pp) {
				matched = true
				break
			}
		}
		if matched {
			remoteByRel[key] = struct {
				Path  string
				Rel   string
				Size  int64
				Mod   int64
				Hash  string
				IsDir bool
			}{Path: pathStr, Rel: relStr, Size: sizeInt, Mod: modNano, Hash: hashStr, IsDir: isDirInt != 0}
		}
	}

	// SSH connection for downloads
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
	var mu sync.Mutex // mutex for thread-safe access to shared variables

	// Collect all files that need download
	var downloadTasks []util.ConcurrentTask

	for _, rel := range rels {
		rm := remoteByRel[rel]
		mu.Lock()
		examined++
		mu.Unlock()

		if rm.IsDir {
			continue
		}

		relNorm := filepath.ToSlash(rel)
		localPath := filepath.Join(absRoot, filepath.FromSlash(relNorm))

		// Bypass mode: skip .sync_temp check and ignore pattern checks
		if relNorm == ".sync_temp" || strings.HasPrefix(relNorm, ".sync_temp/") || strings.Contains(relNorm, "/.sync_temp/") {
			mu.Lock()
			skippedIgnored++
			mu.Unlock()
			continue
		}

		// check local file
		info, statErr := os.Stat(localPath)
		if statErr != nil {
			// File doesn't exist, needs download
			remotePath := buildRemotePath(cfg, relNorm)
			downloadTasks = append(downloadTasks, func() error {
				util.Default.Printf("⬇️  Downloading %s -> %s\n", remotePath, localPath)
				if err := sshCli.DownloadFile(localPath, remotePath); err != nil {
					util.Default.Printf("❌ Failed to download %s: %v\n", remotePath, err)
					mu.Lock()
					downloadErrors++
					mu.Unlock()
					return err
				}
				mu.Lock()
				downloaded = append(downloaded, localPath)
				mu.Unlock()
				return nil
			})
			continue
		}
		if info.IsDir() {
			continue
		}

		// compute local hash
		localHash := ""
		f, err := os.Open(localPath)
		if err == nil {
			h := xxhash.New()
			if _, err := io.Copy(h, f); err == nil {
				localHash = fmt.Sprintf("%x", h.Sum(nil))
			}
			f.Close()
		}

		if strings.TrimSpace(rm.Hash) == "" || rm.Hash != localHash {
			// File exists but different, needs download
			remotePath := buildRemotePath(cfg, relNorm)
			downloadTasks = append(downloadTasks, func() error {
				util.Default.Printf("⬇️  Downloading %s -> %s\n", remotePath, localPath)
				if err := sshCli.DownloadFile(localPath, remotePath); err != nil {
					util.Default.Printf("❌ Failed to download %s: %v\n", remotePath, err)
					mu.Lock()
					downloadErrors++
					mu.Unlock()
					return err
				}
				mu.Lock()
				downloaded = append(downloaded, localPath)
				mu.Unlock()
				return nil
			})
			continue
		}

		mu.Lock()
		skippedUpToDate++
		mu.Unlock()
	}

	// Execute all download tasks with bounded concurrency (max 5 parallel)
	const maxConcurrency = 5
	if err := util.RunConcurrent(downloadTasks, maxConcurrency); err != nil {
		util.Default.Printf("⚠️  Some downloads failed: %v\n", err)
	}

	util.Default.Printf("🔁 Bypass-Download phase: examined(remote entries)=%d, downloaded=%d, skipped(ignored)=%d, skipped(up-to-date)=%d, errors=%d\n",
		examined, len(downloaded), skippedIgnored, skippedUpToDate, downloadErrors)

	return downloaded, nil
}

// CompareAndDownloadManualTransferBypassParallel performs parallel download without ignore pattern checks
// Downloads all files within prefixes regardless of ignore patterns
func CompareAndDownloadManualTransferBypassParallel(cfg *config.Config, localRoot string, prefixes []string) ([]string, error) {
	return CompareAndDownloadManualTransferParallel(cfg, localRoot, prefixes)
}

// CompareAndDownloadManualTransferForce mirrors download with rsync --delete semantics
// limited to the given prefixes (manual_transfer scope). Steps:
//  1. Download remote index DB
//  2. Download changed/missing files only for remote entries matching prefixes
//  3. Delete local files inside the prefixes that are NOT present in remote DB
//     (skips .sync_temp and entries matched by local ignore rules)
func CompareAndDownloadManualTransferForce(cfg *config.Config, localRoot string, prefixes []string) ([]string, error) {
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

	// ensure prefixes normalized (trim leading /)
	normPrefixes := make([]string, 0, len(prefixes))
	for _, pr := range prefixes {
		pr = strings.TrimSpace(pr)
		pr = strings.TrimPrefix(pr, "/")
		normPrefixes = append(normPrefixes, pr)
	}

	// Helper function to check if a relative path belongs to an explicit manual transfer endpoint
	// If it does, ignore patterns should NOT be applied to this path
	isExplicitEndpoint := func(relPath string) bool {
		for _, pr := range normPrefixes {
			// Normalize prefix by removing trailing slash for comparison
			normalizedPr := strings.TrimSuffix(pr, "/")
			if normalizedPr == "" || relPath == normalizedPr || strings.HasPrefix(relPath, normalizedPr+"/") {
				return true
			}
		}
		return false
	}

	// 1) Download remote DB
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

	// Build filtered set of remote rels to process downloads for
	filteredRemote := make(map[string]struct {
		Path  string
		Rel   string
		Size  int64
		Mod   int64
		Hash  string
		IsDir bool
	})
	for rel, v := range remoteByRel {
		// match if rel starts with any prefix
		for _, pr := range normPrefixes {
			if pr == "" || strings.HasPrefix(rel, pr) {
				filteredRemote[rel] = v
				break
			}
		}
	}

	// Create IgnoreCache rooted at absRoot
	ic := NewIgnoreCache(absRoot)

	// SSH connection for downloads
	sshCli, err := ConnectSSH(cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh connect failed: %v", err)
	}
	defer sshCli.Close()

	// 2) Download phase for filtered entries
	// deterministically iterate
	rels := make([]string, 0, len(filteredRemote))
	for r := range filteredRemote {
		rels = append(rels, r)
	}
	// simple sort by path
	sort.Strings(rels)

	downloaded := []string{}
	examined := 0
	skippedIgnored := 0
	skippedUpToDate := 0
	downloadErrors := 0
	var mu sync.Mutex // mutex for thread-safe access to shared variables

	// Collect all files that need download
	var downloadTasks []util.ConcurrentTask

	for _, rel := range rels {
		rm := filteredRemote[rel]
		mu.Lock()
		examined++
		mu.Unlock()

		if rm.IsDir {
			continue
		}

		relNorm := filepath.ToSlash(rel)
		localPath := filepath.Join(absRoot, filepath.FromSlash(relNorm))

		if relNorm == ".sync_temp" || strings.HasPrefix(relNorm, ".sync_temp/") || strings.Contains(relNorm, "/.sync_temp/") {
			mu.Lock()
			skippedIgnored++
			mu.Unlock()
			continue
		}

		// Context-aware ignore check: don't apply ignore patterns to explicit endpoints
		if !isExplicitEndpoint(relNorm) && ic.Match(localPath, false) {
			mu.Lock()
			skippedIgnored++
			mu.Unlock()
			continue
		}

		// check local file
		info, statErr := os.Stat(localPath)
		if statErr != nil {
			// File doesn't exist, needs download
			remotePath := buildRemotePath(cfg, relNorm)
			downloadTasks = append(downloadTasks, func() error {
				util.Default.Printf("⬇️  Downloading %s -> %s\n", remotePath, localPath)
				if err := sshCli.DownloadFile(localPath, remotePath); err != nil {
					util.Default.Printf("❌ Failed to download %s: %v\n", remotePath, err)
					mu.Lock()
					downloadErrors++
					mu.Unlock()
					return err
				}
				mu.Lock()
				downloaded = append(downloaded, localPath)
				mu.Unlock()
				return nil
			})
			continue
		}
		if info.IsDir() {
			continue
		}

		// compute local hash
		localHash := ""
		f, err := os.Open(localPath)
		if err == nil {
			h := xxhash.New()
			if _, err := io.Copy(h, f); err == nil {
				localHash = fmt.Sprintf("%x", h.Sum(nil))
			}
			f.Close()
		}

		if strings.TrimSpace(rm.Hash) == "" || rm.Hash != localHash {
			// File exists but different, needs download
			remotePath := buildRemotePath(cfg, relNorm)
			downloadTasks = append(downloadTasks, func() error {
				util.Default.Printf("⬇️  Downloading %s -> %s\n", remotePath, localPath)
				if err := sshCli.DownloadFile(localPath, remotePath); err != nil {
					util.Default.Printf("❌ Failed to download %s: %v\n", remotePath, err)
					mu.Lock()
					downloadErrors++
					mu.Unlock()
					return err
				}
				mu.Lock()
				downloaded = append(downloaded, localPath)
				mu.Unlock()
				return nil
			})
			continue
		}

		mu.Lock()
		skippedUpToDate++
		mu.Unlock()
	}

	// Execute all download tasks with bounded concurrency (max 5 parallel)
	const maxConcurrency = 5
	if err := util.RunConcurrent(downloadTasks, maxConcurrency); err != nil {
		util.Default.Printf("⚠️  Some downloads failed: %v\n", err)
	}

	util.Default.Printf("🔁 Force-Download phase: examined(remote entries)=%d, downloaded=%d, skipped(ignored): %d, skipped(up-to-date): %d, errors: %d\n",
		examined, len(downloaded), skippedIgnored, skippedUpToDate, downloadErrors)

	// 3) Delete phase: iterate local files within prefixes; delete if not in remoteByRel
	deleted := 0
	deleteErrors := 0
	visited := make(map[string]struct{}) // avoid duplicates across overlapping prefixes

	for _, pr := range normPrefixes {
		start := filepath.Join(absRoot, filepath.FromSlash(pr))
		info, err := os.Stat(start)
		if err != nil {
			continue
		}

		if !info.IsDir() {
			rel, rerr := filepath.Rel(absRoot, start)
			if rerr != nil {
				continue
			}
			rel = filepath.ToSlash(rel)

			if rel == ".sync_temp" || strings.HasPrefix(rel, ".sync_temp/") || strings.Contains(rel, "/.sync_temp/") {
				continue
			}
			// Context-aware ignore check: don't apply ignore patterns to explicit endpoints
			if !isExplicitEndpoint(rel) && ic.Match(start, false) {
				continue
			}
			if _, seen := visited[rel]; seen {
				continue
			}
			visited[rel] = struct{}{}

			if _, exists := remoteByRel[rel]; !exists {
				// ensure rel within root (not ../)
				if strings.HasPrefix(rel, "../") || rel == ".." {
					continue
				}
				if err := os.Remove(start); err != nil {
					util.Default.Printf("❌ Failed to delete %s: %v\n", start, err)
					deleteErrors++
				} else {
					util.Default.Printf("🗑️  Deleted local file (not in remote): %s\n", start)
					deleted++
				}
			}
			continue
		}

		filepath.WalkDir(start, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			rel, rerr := filepath.Rel(absRoot, p)
			if rerr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)

			if p == start && d.IsDir() {
				if ic.Match(p, true) {
					return filepath.SkipDir
				}
				return nil
			}

			if rel == ".sync_temp" || strings.HasPrefix(rel, ".sync_temp/") || strings.Contains(rel, "/.sync_temp/") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			// Context-aware ignore check: don't apply ignore patterns to explicit endpoints
			if !isExplicitEndpoint(rel) && ic.Match(p, d.IsDir()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}

			if _, seen := visited[rel]; seen {
				return nil
			}
			visited[rel] = struct{}{}

			if _, exists := remoteByRel[rel]; !exists {
				// ensure within root
				if strings.HasPrefix(rel, "../") || rel == ".." {
					return nil
				}
				if err := os.Remove(p); err != nil {
					util.Default.Printf("❌ Failed to delete %s: %v\n", p, err)
					deleteErrors++
				} else {
					util.Default.Printf("🗑️  Deleted local file (not in remote): %s\n", p)
					deleted++
				}
			}
			return nil
		})
	}

	util.Default.Printf("🧹 Force-Delete summary: deleted=%d, errors=%d\n", deleted, deleteErrors)

	return downloaded, nil
}

// CompareAndDownloadManualTransferForceParallel performs force download with parallel transfers
// and serial delete phase. It downloads all files within prefixes (respecting ignore patterns)
// and then deletes local files not present remotely.
func CompareAndDownloadManualTransferForceParallel(cfg *config.Config, localRoot string, prefixes []string) ([]string, error) {
	return CompareAndDownloadManualTransferForce(cfg, localRoot, prefixes)
}

// CompareAndUploadManualTransferForce performs upload (local-first) and then deletes
func CompareAndUploadManualTransferForceParallel(cfg *config.Config, localRoot string, prefixes []string) ([]string, error) {
	return CompareAndUploadManualTransferForce(cfg, localRoot, prefixes)
}

// CompareAndUploadManualTransferForce performs upload (local-first) and then deletes
// remote files within the selected prefixes that were not seen locally (checked=0).
// It marks checked=1 in the downloaded remote DB for each processed rel (or falls back
// to in-memory set if the column is missing). It respects .sync_temp and local ignores.
func CompareAndUploadManualTransferForce(cfg *config.Config, localRoot string, prefixes []string) ([]string, error) {
	if len(prefixes) == 0 {
		// Fall back to non-filtered upload + no deletion; to keep semantics scoped, require prefixes
		return CompareAndUploadByHash(cfg, localRoot)
	}

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

	// normalize prefixes
	normPrefixes := make([]string, 0, len(prefixes))
	for _, pr := range prefixes {
		pr = strings.TrimSpace(pr)
		pr = strings.TrimPrefix(pr, "/")
		normPrefixes = append(normPrefixes, pr)
	}

	// Helper function to check if a relative path belongs to an explicit manual transfer endpoint
	// If it does, ignore patterns should NOT be applied to this path
	isExplicitEndpoint := func(relPath string) bool {
		for _, pr := range normPrefixes {
			// Normalize prefix by removing trailing slash for comparison
			normalizedPr := strings.TrimSuffix(pr, "/")
			if normalizedPr == "" || relPath == normalizedPr || strings.HasPrefix(relPath, normalizedPr+"/") {
				return true
			}
		}
		return false
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

	// Create IgnoreCache
	ic := NewIgnoreCache(absRoot)

	// Connect SSH for uploads and deletions
	sshCli, err := ConnectSSH(cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh connect failed: %v", err)
	}
	defer sshCli.Close()

	// Prepare checked record (in-memory fallback)
	checkedSet := make(map[string]struct{})

	// No JSON checked file persistence; rely on DB/in-memory only

	uploaded := make([]string, 0)
	var examined, skippedIgnored, skippedUpToDate, uploadErrors int
	var mu sync.Mutex // mutex for thread-safe access to shared variables

	// Collect all files that need upload
	var uploadTasks []util.ConcurrentTask

	// Helper to mark checked in DB and memory
	markChecked := func(rel string) {
		rel = filepath.ToSlash(rel)
		checkedSet[rel] = struct{}{}
		if hasChecked {
			if _, err := db.Exec(`UPDATE files SET checked=1 WHERE rel=?`, rel); err != nil {
				// fallback to in-memory if update fails
				hasChecked = false
			}
		}
		// no-op: JSON checked file removed
	}

	// two-head-loop over prefixes
	for _, pr := range normPrefixes {
		pr = strings.TrimPrefix(pr, "/")
		start := filepath.Join(absRoot, filepath.FromSlash(pr))
		info, err := os.Stat(start)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			rel, rerr := filepath.Rel(absRoot, start)
			if rerr != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if rel == ".sync_temp" || strings.HasPrefix(rel, ".sync_temp/") || strings.Contains(rel, "/.sync_temp/") {
				continue
			}
			// Context-aware ignore check: don't apply ignore patterns to explicit endpoints
			if !isExplicitEndpoint(rel) && ic.Match(start, false) {
				skippedIgnored++
				// still mark checked to avoid remote delete of ignored?
				// Design: treat ignored as excluded (do not mark checked)
				continue
			}
			mu.Lock()
			examined++
			mu.Unlock()
			// compute local hash
			localHash := ""
			if f, ferr := os.Open(start); ferr == nil {
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
				// File doesn't exist remotely or is different, needs upload
				localPath := start
				remotePath := buildRemotePath(cfg, rel)
				uploadTasks = append(uploadTasks, func() error {
					util.Default.Printf("⬆️  Uploading %s -> %s\n", localPath, remotePath)
					if err := sshCli.UploadFile(localPath, remotePath); err != nil {
						util.Default.Printf("❌ Failed to upload %s: %v\n", localPath, err)
						mu.Lock()
						uploadErrors++
						mu.Unlock()
						return err
					}
					mu.Lock()
					uploaded = append(uploaded, localPath)
					mu.Unlock()
					return nil
				})
			} else {
				mu.Lock()
				skippedUpToDate++
				mu.Unlock()
			}
			// Mark checked after processing
			markChecked(rel)
			continue
		}

		filepath.WalkDir(start, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			rel, rerr := filepath.Rel(absRoot, p)
			if rerr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)

			if p == start && d.IsDir() {
				// Context-aware ignore check for root directory
				if !isExplicitEndpoint(rel) && ic.Match(p, true) {
					return filepath.SkipDir
				}
				return nil
			}
			if rel == ".sync_temp" || strings.HasPrefix(rel, ".sync_temp/") || strings.Contains(rel, "/.sync_temp/") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			// Context-aware ignore check: don't apply ignore patterns to explicit endpoints
			if !isExplicitEndpoint(rel) && ic.Match(p, d.IsDir()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				mu.Lock()
				skippedIgnored++
				mu.Unlock()
				return nil
			}
			if d.IsDir() {
				return nil
			}

			mu.Lock()
			examined++
			mu.Unlock()
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
				// File doesn't exist remotely or is different, needs upload
				localPath := p
				remotePath := buildRemotePath(cfg, rel)
				uploadTasks = append(uploadTasks, func() error {
					util.Default.Printf("⬆️  Uploading %s -> %s\n", localPath, remotePath)
					if err := sshCli.UploadFile(localPath, remotePath); err != nil {
						util.Default.Printf("❌ Failed to upload %s: %v\n", localPath, err)
						mu.Lock()
						uploadErrors++
						mu.Unlock()
						return err
					}
					mu.Lock()
					uploaded = append(uploaded, localPath)
					mu.Unlock()
					return nil
				})
			} else {
				mu.Lock()
				skippedUpToDate++
				mu.Unlock()
			}
			markChecked(rel)
			return nil
		})
	}

	util.Default.Printf("🔁 Local files examined: %d, uploaded: %d, skipped(ignored): %d, skipped(up-to-date): %d, upload errors: %d\n",
		examined, len(uploaded), skippedIgnored, skippedUpToDate, uploadErrors)

	// Execute all upload tasks with bounded concurrency (max 5 parallel)
	const maxConcurrency = 5
	if err := util.RunConcurrent(uploadTasks, maxConcurrency); err != nil {
		util.Default.Printf("⚠️  Some uploads failed: %v\n", err)
	}

	util.Default.Printf("🔁 Force-Upload phase: examined(local files)=%d, uploaded=%d, skipped(ignored)=%d, skipped(up-to-date)=%d, errors=%d\n",
		examined, len(uploaded), skippedIgnored, skippedUpToDate, uploadErrors)

	// Deletion candidates: remote entries within prefixes not checked
	toDelete := make([]string, 0)
	// Build a slice of rel keys from remoteByRel to respect deterministic behavior
	for rel, meta := range remoteByRel {
		if meta.IsDir {
			continue
		}
		// scope by prefixes
		inScope := false
		for _, pr := range normPrefixes {
			if pr == "" || strings.HasPrefix(rel, pr) {
				inScope = true
				break
			}
		}
		if !inScope {
			continue
		}
		// skip .sync_temp and ignore
		if rel == ".sync_temp" || strings.HasPrefix(rel, ".sync_temp/") || strings.Contains(rel, "/.sync_temp/") {
			continue
		}
		localPath := filepath.Join(absRoot, filepath.FromSlash(rel))
		// Context-aware ignore check: don't apply ignore patterns to explicit endpoints
		if !isExplicitEndpoint(rel) && ic.Match(localPath, false) {
			continue
		}
		// checked determination
		checked := false
		if hasChecked {
			// query single
			var c int
			if err := db.QueryRow(`SELECT checked FROM files WHERE rel=?`, rel).Scan(&c); err == nil {
				checked = (c != 0)
			} else {
				// on query error, fallback to in-memory
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
			// Use cmd.exe del; ensure backslashes and quoting are correct
			rp := strings.ReplaceAll(remotePath, "/", "\\")
			cmd = fmt.Sprintf("cmd.exe /C if exist \"%s\" del /f /q \"%s\"", rp, rp)
		} else {
			// POSIX rm -f
			cmd = fmt.Sprintf("rm -f %s", shellQuote(remotePath))
		}
		if err := sshCli.RunCommand(cmd); err != nil {
			util.Default.Printf("❌ Failed to delete remote %s: %v\n", remotePath, err)
			deleteErrors++
		} else {
			util.Default.Printf("🗑️  Deleted remote file (not in local): %s\n", remotePath)
			deleted++
		}
	}

	util.Default.Printf("🧹 Force-Upload delete summary: candidates=%d, deleted=%d, errors=%d\n", len(toDelete), deleted, deleteErrors)

	return uploaded, nil
}

// CompareAndUploadManualTransferBypassParallel performs parallel upload without ignore pattern checks
// Uploads all files within prefixes regardless of ignore patterns
func CompareAndUploadManualTransferBypassParallel(cfg *config.Config, localRoot string, prefixes []string) ([]string, error) {
	if len(prefixes) == 0 {
		return CompareAndUploadByHash(cfg, localRoot)
	}

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

	// ensure prefixes normalized (trim leading /)
	normPrefixes := make([]string, 0, len(prefixes))
	for _, pr := range prefixes {
		pr = strings.TrimSpace(pr)
		pr = strings.TrimPrefix(pr, "/")
		normPrefixes = append(normPrefixes, pr)
	}

	// 1) Download remote DB to check what exists remotely
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

	// SSH connection for uploads
	sshCli, err := ConnectSSH(cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh connect failed: %v", err)
	}
	defer sshCli.Close()

	// 2) Upload phase for filtered entries
	// deterministically iterate
	uploaded := []string{}
	examined := 0
	skippedIgnored := 0
	skippedUpToDate := 0
	uploadErrors := 0
	var mu sync.Mutex // mutex for thread-safe access to shared variables

	// Collect all files that need upload
	var uploadTasks []util.ConcurrentTask

	for _, pr := range normPrefixes {
		start := filepath.Join(absRoot, filepath.FromSlash(pr))
		info, err := os.Stat(start)
		if err != nil {
			continue
		}

		if !info.IsDir() {
			continue
		}

		filepath.WalkDir(start, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			rel, rerr := filepath.Rel(absRoot, p)
			if rerr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)

			// Bypass mode: skip ignore pattern checks
			if d.IsDir() {
				return nil
			}

			if rel == ".sync_temp" || strings.HasPrefix(rel, ".sync_temp/") || strings.Contains(rel, "/.sync_temp/") {
				return nil
			}

			mu.Lock()
			examined++
			mu.Unlock()

			relNorm := filepath.ToSlash(rel)
			localPath := p
			remotePath := buildRemotePath(cfg, relNorm)

			// compute local hash
			localHash := ""
			if f, err := os.Open(localPath); err == nil {
				h := xxhash.New()
				if _, err := io.Copy(h, f); err == nil {
					localHash = fmt.Sprintf("%x", h.Sum(nil))
				}
				f.Close()
			}

			rm, exists := remoteByRel[relNorm]
			if !exists || strings.TrimSpace(rm.Hash) == "" || rm.Hash != localHash {
				// File doesn't exist remotely or is different, needs upload
				uploadTasks = append(uploadTasks, func() error {
					util.Default.Printf("⬆️  Uploading %s -> %s\n", localPath, remotePath)
					if err := sshCli.UploadFile(localPath, remotePath); err != nil {
						util.Default.Printf("❌ Failed to upload %s: %v\n", localPath, err)
						mu.Lock()
						uploadErrors++
						mu.Unlock()
						return err
					}
					mu.Lock()
					uploaded = append(uploaded, localPath)
					mu.Unlock()
					return nil
				})
				return nil
			}

			mu.Lock()
			skippedUpToDate++
			mu.Unlock()
			return nil
		})
	}

	// Execute all upload tasks with bounded concurrency (max 5 parallel)
	const maxConcurrency = 5
	if err := util.RunConcurrent(uploadTasks, maxConcurrency); err != nil {
		util.Default.Printf("⚠️  Some uploads failed: %v\n", err)
	}

	util.Default.Printf("🔁 Bypass-Upload phase: examined(local files)=%d, uploaded=%d, skipped(ignored)=%d, skipped(up-to-date)=%d, errors=%d\n",
		examined, len(uploaded), skippedIgnored, skippedUpToDate, uploadErrors)

	return uploaded, nil
}
