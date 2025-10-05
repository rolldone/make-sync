package syncdata

import (
	"make-sync/internal/config"
	"make-sync/internal/deployagent"
	"make-sync/internal/sshclient"
	"make-sync/internal/util"
	"os"
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
	_, out, err := RunAgentIndexingFlow(cfg, []string{agentPath}, false)
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
	_, out, err := RunAgentIndexingFlow(cfg, []string{agentPath}, false)
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
