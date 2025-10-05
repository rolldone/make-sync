package syncdata

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"make-sync/internal/config"
	"make-sync/internal/deployagent"
	"make-sync/internal/tui"
	"make-sync/internal/util"
)

// isBypassMode returns true if the modeChoice indicates bypass ignore patterns
func isBypassMode(modeChoice string) bool {
	return strings.Contains(modeChoice, "(Bypass)")
}

// readNegationPatterns reads .sync_ignore in the given root and returns lines
// starting with '!' with leading '!' and whitespace trimmed.
func readNegationPatterns(root string) []string {
	path := filepath.Join(root, ".sync_ignore")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0)
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "!") {
			p := strings.TrimSpace(strings.TrimPrefix(t, "!"))
			if p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// ForceSingleSyncMenu implements the nested Single Sync interactive flow.
// It uses cfg.Devsync.ManualTransfer as the list of registered paths.
func ForceSingleSyncMenu(cfg *config.Config, localRoot string) {
	// build a stable list of registered paths
	registered := cfg.Devsync.ManualTransfer
	// compute absolute local root once
	absRoot := localRoot
	if abs, err := filepath.Abs(localRoot); err == nil {
		absRoot = abs
	}

	for {
		// top: choose between Download / Upload / Back
		choice, err := tui.ShowMenuWithPrints([]string{"Download", "Upload", "Back"}, "Single Sync")
		if err != nil {
			util.Default.Printf("❌ Failed to show Single Sync menu: %v\n", err)
			return
		}
		if choice == "cancelled" { // Ctrl+C: exit entire flow
			return
		}
		if choice == "Back" {
			return
		}

		// folder selection menu
		for {
			// construct folder choices
			items := make([]string, 0, len(registered)+4)
			items = append(items, registered...)
			items = append(items, "----------")
			items = append(items, "All data registered in manual_sync only")
			items = append(items, "All Data Only In Your \"Sync Ignore\" File Pattern")
			items = append(items, "Back Previous / Exit")

			folderChoice, err := tui.ShowMenuWithPrints(items, "? Single Sync : Which folder :")
			if err != nil {
				util.Default.Printf("❌ Folder selection cancelled: %v\n", err)
				break
			}
			if folderChoice == "cancelled" { // Ctrl+C: exit entire flow
				return
			}
			if folderChoice == "Back Previous / Exit" {
				break // go back to Download/Upload selection
			}

			// determine prefixes list to pass to filter-based compare functions
			var prefixes []string
			switch folderChoice {
			case "All data registered in manual_sync only":
				// normalize registered paths into prefixes relative to localRoot
				prefixes = normalizeToRelativePrefixes(absRoot, registered)
				if len(prefixes) == 0 {
					util.Default.Println("ℹ️  No registered paths found.")
					continue
				}
			case "All Data Only In Your \"Sync Ignore\" File Pattern":
				// read negation patterns from localRoot
				neg := readNegationPatterns(localRoot)
				if len(neg) == 0 {
					util.Default.Println("ℹ️  No negation patterns found in .sync_ignore")
					continue
				}
				// use negation patterns as prefixes
				for _, p := range neg {
					prefixes = append(prefixes, filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(p), "/")))
				}
			default:
				// assume it's a registered path - normalize single entry
				single := strings.TrimSpace(folderChoice)
				if single == "" {
					util.Default.Println("ℹ️  Empty selection")
					continue
				}
				prefixes = normalizeToRelativePrefixes(absRoot, []string{single})
				if len(prefixes) == 0 {
					util.Default.Println("ℹ️  Selected path is outside project root or invalid.")
					continue
				}
			}

			// mode selection
			modeChoice, err := tui.ShowMenuWithPrints([]string{"Rsync Soft Mode", "Rsync Force Mode", "Rsync Soft Mode (Bypass)", "Rsync Force Mode (Bypass)", "Back Previous / Exit"}, "? Single Sync : ["+choice+"] | Which mode :")
			if err != nil {
				util.Default.Printf("❌ Mode selection cancelled: %v\n", err)
				continue
			}
			if modeChoice == "cancelled" { // Ctrl+C: exit entire flow
				return
			}
			if modeChoice == "Back Previous / Exit" {
				continue // go back to folder selection
			}

			// execute selected action
			// small debug: show prefixes used
			util.Default.Printf("🔎 Using prefixes: %v\n", prefixes)

			switch choice {
			case "Download":
				util.Default.Printf("🔁 Running Download (%s) for prefixes: %v\n", modeChoice, prefixes)
				// Mirror safe_pull_sync: build agent (with fallback) and run remote indexing first
				if err := runRemoteIndexingForPull(cfg, isBypassMode(modeChoice)); err != nil {
					util.Default.Printf("❌ Remote indexing (safe_pull) failed: %v\n", err)
					continue
				}
				var downloaded []string
				// If selection is "All Data Only In Your \"Sync Ignore\" File Pattern", we should use include-pattern logic
				if folderChoice == "All Data Only In Your \"Sync Ignore\" File Pattern" {
					neg := readNegationPatterns(localRoot)
					if modeChoice == "Rsync Force Mode" {
						downloaded, err = CompareAndDownloadByIgnoreIncludesForce(cfg, localRoot, neg)
					} else {
						downloaded, err = CompareAndDownloadByIgnoreIncludes(cfg, localRoot, neg)
					}
				} else {
					if modeChoice == "Rsync Force Mode" {
						// Force mode: rsync --delete semantics within prefixes (parallel)
						downloaded, err = CompareAndDownloadManualTransferForceParallel(cfg, localRoot, prefixes)
					} else if modeChoice == "Rsync Soft Mode" {
						// Soft mode: rsync semantics within prefixes (parallel)
						downloaded, err = CompareAndDownloadManualTransferParallel(cfg, localRoot, prefixes)
					} else if modeChoice == "Rsync Force Mode (Bypass)" {
						// Force bypass mode: rsync --delete semantics within prefixes, bypass ignore patterns
						downloaded, err = CompareAndDownloadManualTransferForceParallel(cfg, localRoot, prefixes)
					} else if modeChoice == "Rsync Soft Mode (Bypass)" {
						// Soft bypass mode: rsync semantics within prefixes, bypass ignore patterns
						downloaded, err = CompareAndDownloadManualTransferBypassParallel(cfg, localRoot, prefixes)
					} else {
						downloaded, err = CompareAndDownloadManualTransferParallel(cfg, localRoot, prefixes)
					}
				}
				if err != nil {
					util.Default.Printf("❌ Download failed: %v\n", err)
				} else {
					if len(downloaded) == 0 {
						util.Default.Println("✅ No files downloaded (nothing matched or already up-to-date)")
					} else {
						util.Default.Printf("⬇️  Downloaded %d files:\n", len(downloaded))
						for _, f := range downloaded {
							util.Default.Printf(" - %s\n", f)
						}
					}
				}
			case "Upload":
				util.Default.Printf("🔁 Running Upload (%s) for prefixes: %v\n", modeChoice, prefixes)
				// Mirror safe_push_sync: run remote indexing first so DB is fresh
				if err := runRemoteIndexingForPull(cfg, isBypassMode(modeChoice)); err != nil {
					util.Default.Printf("❌ Remote indexing (safe_push) failed: %v\n", err)
					continue
				}
				var uploaded []string
				// If selection is "All Data Only In Your \"Sync Ignore\" File Pattern", use include-pattern Upload
				if folderChoice == "All Data Only In Your \"Sync Ignore\" File Pattern" {
					neg := readNegationPatterns(localRoot)
					if modeChoice == "Rsync Force Mode" {
						uploaded, err = CompareAndUploadByIgnoreIncludesForce(cfg, localRoot, neg)
					} else {
						uploaded, err = CompareAndUploadByIgnoreIncludes(cfg, localRoot, neg)
					}
				} else {
					if modeChoice == "Rsync Force Mode" {
						uploaded, err = CompareAndUploadManualTransferForceParallel(cfg, localRoot, prefixes)
					} else if modeChoice == "Rsync Soft Mode" {
						uploaded, err = CompareAndUploadManualTransfer(cfg, localRoot, prefixes)
					} else if modeChoice == "Rsync Force Mode (Bypass)" {
						// Force bypass mode: rsync --delete semantics within prefixes, bypass ignore patterns
						uploaded, err = CompareAndUploadManualTransferForceParallel(cfg, localRoot, prefixes)
					} else if modeChoice == "Rsync Soft Mode (Bypass)" {
						// Soft bypass mode: rsync semantics within prefixes, bypass ignore patterns
						uploaded, err = CompareAndUploadManualTransferBypassParallel(cfg, localRoot, prefixes)
					} else {
						uploaded, err = CompareAndUploadManualTransfer(cfg, localRoot, prefixes)
					}
				}
				if err != nil {
					util.Default.Printf("❌ Upload failed: %v\n", err)
				} else {
					if len(uploaded) == 0 {
						util.Default.Println("✅ No files uploaded (nothing matched or already up-to-date)")
					} else {
						util.Default.Printf("⬆️  Uploaded %d files:\n", len(uploaded))
						for _, f := range uploaded {
							util.Default.Printf(" - %s\n", f)
						}
					}
				}
			default:
				// no-op
			}
			// after operation, return to folder selection (per stepwise behavior)
		}
	}
}

// runRemoteIndexingForPull mirrors the safe_pull_sync flow: it builds the agent
// (with fallback) using remote detection, uploads agent and config, and runs
// remote indexing so the DB is fresh before pulling.
func runRemoteIndexingForPull(cfg *config.Config, bypassIgnore bool) error {
	// Determine target OS
	targetOS := cfg.Devsync.OSTarget
	if strings.TrimSpace(targetOS) == "" {
		targetOS = "linux"
	}

	// Determine project root similarly to safe_pull_sync implementation
	watchPath := cfg.LocalPath
	if strings.TrimSpace(watchPath) == "" {
		watchPath = "."
	}
	// Get project root - handles both development (go run) and production modes
	projectRoot, err := util.GetProjectRoot()
	if err != nil {
		return fmt.Errorf("failed to get project root: %v", err)
	}

	// Connect SSH for remote detection (adapter used by build)
	sshClient, err := ConnectSSH(cfg)
	if err != nil {
		return err
	}
	// Ensure close after build
	defer sshClient.Close()

	sshAdapter := deployagent.NewSSHClientAdapter(sshClient)
	buildOpts := deployagent.BuildOptions{
		ProjectRoot: projectRoot,
		TargetOS:    targetOS,
		SSHClient:   sshAdapter,
		Config:      cfg,
	}

	agentPath, bErr := deployagent.BuildAgentForTarget(buildOpts)
	if bErr != nil {
		util.Default.Printf("⚠️  Build failed for agent: %v\n", bErr)
		// Fallback
		fallbackPath := deployagent.FindFallbackAgent(projectRoot, targetOS)
		if fallbackPath != "" {
			util.Default.Printf("ℹ️  Using fallback agent binary: %s\n", fallbackPath)
			agentPath = fallbackPath
		} else {
			return bErr
		}
	}
	util.Default.Printf("✅ Agent ready: %s\n", agentPath)

	// Run remote indexing via existing flow (handles upload agent+config, execute, etc.)
	_, out, err := RunAgentIndexingFlow(cfg, []string{agentPath}, bypassIgnore)
	if err != nil {
		util.Default.Printf("🔍 Remote output (partial): %s\n", out)
		return err
	}
	util.Default.Printf("✅ Remote indexing finished.\n")
	return nil
}

// normalizeToRelativePrefixes converts a list of configured paths (absolute or relative)
// into relative forward-slash prefixes under absRoot. Entries outside absRoot are skipped.
func normalizeToRelativePrefixes(absRoot string, paths []string) []string {
	dedup := make(map[string]struct{})
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		var abs string
		if filepath.IsAbs(p) {
			a, err := filepath.Abs(p)
			if err != nil {
				continue
			}
			abs = a
		} else {
			a := filepath.Join(absRoot, p)
			if r, err := filepath.Abs(a); err == nil {
				abs = r
			} else {
				abs = a
			}
		}
		rel, err := filepath.Rel(absRoot, abs)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		// skip outside root (starts with ../)
		if strings.HasPrefix(rel, "../") || rel == ".." {
			continue
		}
		rel = strings.TrimPrefix(rel, "./")
		if rel == "." || rel == "" {
			// selecting root means everything under root; use empty prefix to match all
			rel = ""
		}
		if _, seen := dedup[rel]; !seen {
			dedup[rel] = struct{}{}
			out = append(out, rel)
		}
	}
	return out
}
