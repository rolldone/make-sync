package syncdata

import (
	"make-sync/internal/config"
	"make-sync/internal/deployagent"
	"make-sync/internal/sshclient"
	"make-sync/internal/util"
	"os"
	"strings"
)

// SafePullResult represents the result of a safe pull operation
type SafePullResult struct {
	Success         bool
	Output          string
	Error           error
	DownloadedFiles []string
}

// SafePushResult represents the result of a safe push operation
type SafePushResult struct {
	Success       bool
	Output        string
	Error         error
	UploadedFiles []string
}

// RunSafePull executes the complete safe pull workflow:
// 1. Build/find agent binary
// 2. Deploy agent and run remote indexing
// 3. Download remote index DB
// 4. Compare and download changed files by hash
func RunSafePull(cfg *config.Config, sshClient *sshclient.SSHClient) SafePullResult {
	util.Default.Println("🔁 safe_pull_sync selected — building and deploying agent for remote indexing...")

	// Determine target OS from config
	targetOS := cfg.Devsync.OSTarget
	if targetOS == "" {
		targetOS = "linux" // Default to linux
	}

	// Get project root - handles both development (go run) and production modes
	projectRoot, err := util.GetProjectRoot()
	if err != nil {
		util.Default.Printf("❌ Failed to get project root: %v\n", err)
		return SafePullResult{Success: false, Error: err}
	}

	// Create SSH adapter (assuming sshClient is *sshclient.SSHClient)
	sshAdapter := deployagent.NewSSHClientAdapter(sshClient)

	// Try to build agent first
	buildOpts := deployagent.BuildOptions{
		ProjectRoot: projectRoot,
		TargetOS:    targetOS,
		SSHClient:   sshAdapter, // For remote architecture detection
		Config:      cfg,        // Pass config for unique agent naming
	}

	agentPath, err := deployagent.BuildAgentForTarget(buildOpts)
	if err != nil {
		util.Default.Printf("⚠️  Build failed for agent: %v\n", err)

		// Try fallback using deployagent's fallback detection
		fallbackPath := deployagent.FindFallbackAgent(projectRoot, targetOS)
		if fallbackPath != "" {
			util.Default.Printf("ℹ️  Using fallback agent binary: %s\n", fallbackPath)
			agentPath = fallbackPath
		} else {
			util.Default.Printf("❌ No fallback agent found and build failed: %v\n", err)
			return SafePullResult{Success: false, Error: err}
		}
	}

	util.Default.Printf("✅ Agent ready: %s\n", agentPath)

	// Deploy agent+config and run remote indexing
	// No prefixes provided for full indexing
	_, out, err := RunAgentIndexingFlow(cfg, []string{agentPath}, false, nil)
	if err != nil {
		util.Default.Printf("❌ Remote indexing failed: %v\n", err)
		util.Default.Printf("🔍 Remote output (partial): %s\n", out)
		return SafePullResult{Success: false, Error: err, Output: out}
	}

	// Download indexing DB into local project .sync_temp
	// Prefer the configured LocalPath from config
	var downloadTarget string
	if cfg.LocalPath != "" {
		downloadTarget = cfg.LocalPath
	} else if cfg.Devsync.Auth.LocalPath != "" {
		downloadTarget = cfg.Devsync.Auth.LocalPath
	}

	localDBPath, derr := DownloadIndexDB(cfg, downloadTarget)
	if derr != nil {
		util.Default.Printf("⚠️  Indexing finished but failed to download DB: %v\n", derr)
	} else {
		util.Default.Printf("✅ Index DB downloaded to: %s\n", localDBPath)
	}

	// Ensure we have a concrete local root to compare against
	compareTarget := downloadTarget
	if compareTarget == "" {
		if cfg.LocalPath != "" {
			compareTarget = cfg.LocalPath
		} else if cfg.Devsync.Auth.LocalPath != "" {
			compareTarget = cfg.Devsync.Auth.LocalPath
		} else {
			// fallback to current working directory
			wd, werr := os.Getwd()
			if werr == nil {
				compareTarget = wd
			} else {
				compareTarget = "."
			}
		}
	}

	util.Default.Println("🔁 Comparing remote index with local files (by hash)...")
	downloadedFiles, cerr := CompareAndDownloadByHash(cfg, compareTarget)
	if cerr != nil {
		util.Default.Printf("❌ Compare/download failed: %v\n", cerr)
		return SafePullResult{Success: false, Error: cerr, Output: out}
	} else {
		if len(downloadedFiles) == 0 {
			util.Default.Println("✅ No files needed downloading — all hashes matched or remote entries empty.")
		} else {
			util.Default.Printf("⬇️  Downloaded %d files:\n", len(downloadedFiles))
			for _, f := range downloadedFiles {
				util.Default.Printf(" - %s\n", f)
			}
		}
	}

	util.Default.Printf("✅ Agent indexed successfully. Remote output:\n%s\n", out)
	return SafePullResult{
		Success:         true,
		Output:          out,
		DownloadedFiles: downloadedFiles,
	}
}

// RunPullWithMode performs the pull workflow but selects the compare/download
// strategy based on mode. mode strings expected to contain substrings:
// "Force" (enable delete semantics), "Bypass" (bypass local ignore patterns).
func RunPullWithMode(cfg *config.Config, sshClient *sshclient.SSHClient, mode string) SafePullResult {
	util.Default.Println("🔁 pull selected — building and deploying agent for remote indexing...")

	// Determine target OS from config
	targetOS := cfg.Devsync.OSTarget
	if targetOS == "" {
		targetOS = "linux"
	}

	// Get project root
	projectRoot, err := util.GetProjectRoot()
	if err != nil {
		util.Default.Printf("❌ Failed to get project root: %v\n", err)
		return SafePullResult{Success: false, Error: err}
	}

	// Build agent
	sshAdapter := deployagent.NewSSHClientAdapter(sshClient)
	buildOpts := deployagent.BuildOptions{
		ProjectRoot: projectRoot,
		TargetOS:    targetOS,
		SSHClient:   sshAdapter,
		Config:      cfg,
	}
	agentPath, err := deployagent.BuildAgentForTarget(buildOpts)
	if err != nil {
		util.Default.Printf("⚠️  Build failed for agent: %v\n", err)
		fallbackPath := deployagent.FindFallbackAgent(projectRoot, targetOS)
		if fallbackPath != "" {
			util.Default.Printf("ℹ️  Using fallback agent binary: %s\n", fallbackPath)
			agentPath = fallbackPath
		} else {
			util.Default.Printf("❌ No fallback agent found and build failed: %v\n", err)
			return SafePullResult{Success: false, Error: err}
		}
	}
	util.Default.Printf("✅ Agent ready: %s\n", agentPath)

	// Run remote indexing for full project (no prefixes)
	_, out, err := RunAgentIndexingFlow(cfg, []string{agentPath}, strings.Contains(mode, "Bypass"), []string{""})
	if err != nil {
		util.Default.Printf("❌ Remote indexing failed: %v\n", err)
		util.Default.Printf("🔍 Remote output (partial): %s\n", out)
		return SafePullResult{Success: false, Error: err, Output: out}
	}

	// Determine compare target (local root)
	var compareTarget string
	if cfg.LocalPath != "" {
		compareTarget = cfg.LocalPath
	} else if cfg.Devsync.Auth.LocalPath != "" {
		compareTarget = cfg.Devsync.Auth.LocalPath
	} else {
		if wd, werr := os.Getwd(); werr == nil {
			compareTarget = wd
		} else {
			compareTarget = "."
		}
	}

	util.Default.Println("🔁 Comparing remote index with local files...")

	// dispatch strategy
	if strings.Contains(mode, "Force") {
		// Force mode: delete semantics across the whole project
		downloaded, cerr := CompareAndDownloadManualTransferForceParallel(cfg, compareTarget, []string{""})
		if cerr != nil {
			util.Default.Printf("❌ Compare/download (force) failed: %v\n", cerr)
			return SafePullResult{Success: false, Error: cerr, Output: out}
		}
		if len(downloaded) == 0 {
			util.Default.Println("✅ No files downloaded (nothing matched or already up-to-date)")
		} else {
			util.Default.Printf("⬇️  Downloaded %d files:\n", len(downloaded))
			for _, f := range downloaded {
				util.Default.Printf(" - %s\n", f)
			}
		}
		return SafePullResult{Success: true, Output: out, DownloadedFiles: downloaded}
	}

	// Safe variants (default)
	if strings.Contains(mode, "Bypass") {
		downloaded, cerr := CompareAndDownloadManualTransferBypassParallel(cfg, compareTarget, []string{""})
		if cerr != nil {
			util.Default.Printf("❌ Compare/download (bypass) failed: %v\n", cerr)
			return SafePullResult{Success: false, Error: cerr, Output: out}
		}
		if len(downloaded) == 0 {
			util.Default.Println("✅ No files downloaded (nothing matched or already up-to-date)")
		} else {
			util.Default.Printf("⬇️  Downloaded %d files:\n", len(downloaded))
			for _, f := range downloaded {
				util.Default.Printf(" - %s\n", f)
			}
		}
		return SafePullResult{Success: true, Output: out, DownloadedFiles: downloaded}
	}

	// default safe: by-hash
	downloaded, cerr := CompareAndDownloadByHash(cfg, compareTarget)
	if cerr != nil {
		util.Default.Printf("❌ Compare/download failed: %v\n", cerr)
		return SafePullResult{Success: false, Error: cerr, Output: out}
	}
	if len(downloaded) == 0 {
		util.Default.Println("✅ No files needed downloading — all hashes matched or remote entries empty.")
	} else {
		util.Default.Printf("⬇️  Downloaded %d files:\n", len(downloaded))
		for _, f := range downloaded {
			util.Default.Printf(" - %s\n", f)
		}
	}
	util.Default.Printf("✅ Pull finished. Remote output:\n%s\n", out)
	return SafePullResult{Success: true, Output: out, DownloadedFiles: downloaded}
}

// RunSafePush executes the complete safe push workflow:
// 1. Build/find agent binary
// 2. Deploy agent and run remote indexing
// 3. Download remote index DB
// 4. Compare and upload changed files by hash
func RunSafePush(cfg *config.Config, sshClient *sshclient.SSHClient) SafePushResult {
	util.Default.Println("🔁 safe_push_sync selected — building and deploying agent for remote indexing...")

	// Determine target OS from config
	targetOS := cfg.Devsync.OSTarget
	if targetOS == "" {
		targetOS = "linux"
	}

	// Get project root - handles both development (go run) and production modes
	projectRoot, err := util.GetProjectRoot()
	if err != nil {
		util.Default.Printf("❌ Failed to get project root: %v\n", err)
		return SafePushResult{Success: false, Error: err}
	}

	sshAdapter := deployagent.NewSSHClientAdapter(sshClient)

	// Try to build agent first
	buildOpts := deployagent.BuildOptions{
		ProjectRoot: projectRoot,
		TargetOS:    targetOS,
		SSHClient:   sshAdapter,
		Config:      cfg,
	}

	agentPath, err := deployagent.BuildAgentForTarget(buildOpts)
	if err != nil {
		util.Default.Printf("⚠️  Build failed for agent: %v\n", err)
		fallbackPath := deployagent.FindFallbackAgent(projectRoot, targetOS)
		if fallbackPath != "" {
			util.Default.Printf("ℹ️  Using fallback agent binary: %s\n", fallbackPath)
			agentPath = fallbackPath
		} else {
			util.Default.Printf("❌ No fallback agent found and build failed: %v\n", err)
			return SafePushResult{Success: false, Error: err}
		}
	}

	util.Default.Printf("✅ Agent ready: %s\n", agentPath)

	// Run remote indexing and download DB
	// No prefixes provided for full indexing
	_, out, err := RunAgentIndexingFlow(cfg, []string{agentPath}, false, nil)
	if err != nil {
		util.Default.Printf("❌ Remote indexing failed: %v\n", err)
		util.Default.Printf("🔍 Remote output (partial): %s\n", out)
		return SafePushResult{Success: false, Error: err, Output: out}
	}

	// Determine download target (local root)
	var downloadTarget string
	if cfg.LocalPath != "" {
		downloadTarget = cfg.LocalPath
	} else if cfg.Devsync.Auth.LocalPath != "" {
		downloadTarget = cfg.Devsync.Auth.LocalPath
	}

	localDBPath, derr := DownloadIndexDB(cfg, downloadTarget)
	if derr != nil {
		util.Default.Printf("⚠️  Indexing finished but failed to download DB: %v\n", derr)
	} else {
		util.Default.Printf("✅ Index DB downloaded to: %s\n", localDBPath)
	}

	compareTarget := downloadTarget
	if compareTarget == "" {
		if cfg.LocalPath != "" {
			compareTarget = cfg.LocalPath
		} else if cfg.Devsync.Auth.LocalPath != "" {
			compareTarget = cfg.Devsync.Auth.LocalPath
		} else {
			wd, werr := os.Getwd()
			if werr == nil {
				compareTarget = wd
			} else {
				compareTarget = "."
			}
		}
	}

	util.Default.Println("🔁 Comparing local files with remote index (by hash) and uploading changes...")
	uploaded, uerr := CompareAndUploadByHash(cfg, compareTarget)
	if uerr != nil {
		util.Default.Printf("❌ Compare/upload failed: %v\n", uerr)
		return SafePushResult{Success: false, Error: uerr, Output: out}
	} else {
		if len(uploaded) == 0 {
			util.Default.Println("✅ No files needed uploading — all hashes matched or remote entries empty.")
		} else {
			util.Default.Printf("⬆️  Uploaded %d files:\n", len(uploaded))
			for _, f := range uploaded {
				util.Default.Printf(" - %s\n", f)
			}
		}
	}

	util.Default.Printf("✅ Safe push completed. Remote output:\n%s\n", out)
	return SafePushResult{
		Success:       true,
		Output:        out,
		UploadedFiles: uploaded,
	}
}

// RunPushWithMode performs the push workflow but selects the compare/upload
// strategy based on mode. mode strings expected to contain substrings:
// "Force" (enable delete semantics), "Bypass" (bypass local ignore patterns).
func RunPushWithMode(cfg *config.Config, sshClient *sshclient.SSHClient, mode string) SafePushResult {
	util.Default.Println("🔁 push selected — building and deploying agent for remote indexing...")

	// Determine target OS from config
	targetOS := cfg.Devsync.OSTarget
	if targetOS == "" {
		targetOS = "linux"
	}

	// Get project root
	projectRoot, err := util.GetProjectRoot()
	if err != nil {
		util.Default.Printf("❌ Failed to get project root: %v\n", err)
		return SafePushResult{Success: false, Error: err}
	}

	sshAdapter := deployagent.NewSSHClientAdapter(sshClient)
	buildOpts := deployagent.BuildOptions{
		ProjectRoot: projectRoot,
		TargetOS:    targetOS,
		SSHClient:   sshAdapter,
		Config:      cfg,
	}
	agentPath, err := deployagent.BuildAgentForTarget(buildOpts)
	if err != nil {
		util.Default.Printf("⚠️  Build failed for agent: %v\n", err)
		fallbackPath := deployagent.FindFallbackAgent(projectRoot, targetOS)
		if fallbackPath != "" {
			util.Default.Printf("ℹ️  Using fallback agent binary: %s\n", fallbackPath)
			agentPath = fallbackPath
		} else {
			util.Default.Printf("❌ No fallback agent found and build failed: %v\n", err)
			return SafePushResult{Success: false, Error: err}
		}
	}
	util.Default.Printf("✅ Agent ready: %s\n", agentPath)

	// Run remote indexing for full project (no prefixes)
	_, out, err := RunAgentIndexingFlow(cfg, []string{agentPath}, strings.Contains(mode, "Bypass"), []string{""})
	if err != nil {
		util.Default.Printf("❌ Remote indexing failed: %v\n", err)
		util.Default.Printf("🔍 Remote output (partial): %s\n", out)
		return SafePushResult{Success: false, Error: err, Output: out}
	}

	// Determine compare target (local root)
	var compareTarget string
	if cfg.LocalPath != "" {
		compareTarget = cfg.LocalPath
	} else if cfg.Devsync.Auth.LocalPath != "" {
		compareTarget = cfg.Devsync.Auth.LocalPath
	} else {
		if wd, werr := os.Getwd(); werr == nil {
			compareTarget = wd
		} else {
			compareTarget = "."
		}
	}

	util.Default.Println("🔁 Comparing local files with remote index and uploading changes...")

	if strings.Contains(mode, "Force") {
		// Force mode: upload + delete remote entries across project
		uploaded, cerr := CompareAndUploadManualTransferForceParallel(cfg, compareTarget, []string{""})
		if cerr != nil {
			util.Default.Printf("❌ Compare/upload (force) failed: %v\n", cerr)
			return SafePushResult{Success: false, Error: cerr, Output: out}
		}
		if len(uploaded) == 0 {
			util.Default.Println("✅ No files uploaded (nothing matched or already up-to-date)")
		} else {
			util.Default.Printf("⬆️  Uploaded %d files:\n", len(uploaded))
			for _, f := range uploaded {
				util.Default.Printf(" - %s\n", f)
			}
		}
		return SafePushResult{Success: true, Output: out, UploadedFiles: uploaded}
	}

	if strings.Contains(mode, "Bypass") {
		uploaded, cerr := CompareAndUploadManualTransferBypassParallel(cfg, compareTarget, []string{""})
		if cerr != nil {
			util.Default.Printf("❌ Compare/upload (bypass) failed: %v\n", cerr)
			return SafePushResult{Success: false, Error: cerr, Output: out}
		}
		if len(uploaded) == 0 {
			util.Default.Println("✅ No files uploaded (nothing matched or already up-to-date)")
		} else {
			util.Default.Printf("⬆️  Uploaded %d files:\n", len(uploaded))
			for _, f := range uploaded {
				util.Default.Printf(" - %s\n", f)
			}
		}
		return SafePushResult{Success: true, Output: out, UploadedFiles: uploaded}
	}

	// default safe: by-hash
	uploaded, cerr := CompareAndUploadByHash(cfg, compareTarget)
	if cerr != nil {
		util.Default.Printf("❌ Compare/upload failed: %v\n", cerr)
		return SafePushResult{Success: false, Error: cerr, Output: out}
	}
	if len(uploaded) == 0 {
		util.Default.Println("✅ No files needed uploading — all hashes matched or remote entries empty.")
	} else {
		util.Default.Printf("⬆️  Uploaded %d files:\n", len(uploaded))
		for _, f := range uploaded {
			util.Default.Printf(" - %s\n", f)
		}
	}
	util.Default.Printf("✅ Push finished. Remote output:\n%s\n", out)
	return SafePushResult{Success: true, Output: out, UploadedFiles: uploaded}
}
