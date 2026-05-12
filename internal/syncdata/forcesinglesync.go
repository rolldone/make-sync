package syncdata

import (
	"fmt"
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

func normalizeManualPrefix(prefix string) string {
	prefix = filepath.ToSlash(strings.TrimSpace(prefix))
	prefix = strings.TrimPrefix(prefix, "/")
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "." {
		return ""
	}
	return prefix
}

func buildManualIgnoreProfilesForSelection(cfg *config.Config, absRoot string, selectedPrefixes []string) map[string][]string {
	out := make(map[string][]string)
	if cfg == nil || len(cfg.Devsync.ManualTransferIgnores) == 0 || len(selectedPrefixes) == 0 {
		return out
	}

	selected := make(map[string]struct{}, len(selectedPrefixes))
	for _, p := range selectedPrefixes {
		selected[normalizeManualPrefix(p)] = struct{}{}
	}

	for rawPath, rules := range cfg.Devsync.ManualTransferIgnores {
		rels := normalizeToRelativePrefixes(absRoot, []string{rawPath})
		for _, rel := range rels {
			rel = normalizeManualPrefix(rel)
			if _, ok := selected[rel]; !ok {
				continue
			}

			clean := make([]string, 0, len(rules))
			for _, r := range rules {
				r = strings.TrimSpace(r)
				if r == "" {
					continue
				}
				clean = append(clean, r)
			}
			if len(clean) > 0 {
				out[rel] = clean
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
			items := make([]string, 0, len(registered)+3)
			items = append(items, registered...)
			items = append(items, "----------")
			// Add a manual refresh action so users can reload `make-sync.yaml` without
			// restarting the app.
			items = append(items, "Refresh sync folders")
			items = append(items, "All data registered in manual_sync only")
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

			// Handle Refresh action
			if folderChoice == "Refresh sync folders" {
				newCfg, cerr := config.LoadAndRenderConfig()
				if cerr != nil {
					util.Default.Printf("❌ Failed to reload config: %v\n", cerr)
					continue
				}
				// Replace caller-provided cfg contents so subsequent operations use fresh config
				*cfg = *newCfg
				registered = cfg.Devsync.ManualTransfer
				util.Default.Printf("🔁 Config reloaded — %d prefixes\n", len(registered))
				continue
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
			modeChoice, err := tui.ShowMenuWithPrints([]string{"Safe (no deletes)", "Force (may delete remote/local)", "Safe (Bypass)", "Force (Bypass)", "Back Previous / Exit"}, "? Single Sync : ["+choice+"] | Which mode :")
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
			manualProfiles := buildManualIgnoreProfilesForSelection(cfg, absRoot, prefixes)

			func() {
				if isBypassMode(modeChoice) {
					// Manual sync bypass mode: disable all ignore filtering.
					SetManualTransferIgnoreProfiles(map[string][]string{})
				} else {
					// Manual sync safe/force: use per-entry ignores from manual_transfer objects only.
					SetManualTransferIgnoreProfiles(manualProfiles)
				}
				defer ClearManualTransferIgnoreProfiles()

				switch choice {
				case "Download":
					util.Default.Printf("🔁 Running Download (%s) for prefixes: %v\n", modeChoice, prefixes)
					// Single-sync indexing intentionally bypasses remote .sync_ignore.
					if err := runRemoteIndexingForPull(cfg, true, prefixes); err != nil {
						util.Default.Printf("❌ Remote indexing (safe_pull) failed: %v\n", err)
						return
					}

					var downloaded []string
					if strings.Contains(modeChoice, "Force") {
						ok, cerr := tui.ConfirmWithCaptcha("This action may delete files on remote/local. Proceed?", 3)
						if cerr != nil {
							util.Default.Printf("❌ Captcha error: %v\n", cerr)
							return
						}
						if !ok {
							return
						}
						downloaded, err = CompareAndDownloadManualTransferForceParallel(cfg, localRoot, prefixes)
					} else if strings.Contains(modeChoice, "Bypass") {
						downloaded, err = CompareAndDownloadManualTransferBypassParallel(cfg, localRoot, prefixes)
					} else {
						downloaded, err = CompareAndDownloadManualTransferParallel(cfg, localRoot, prefixes)
					}

					if err != nil {
						util.Default.Printf("❌ Download failed: %v\n", err)
						return
					}
					if len(downloaded) == 0 {
						util.Default.Println("✅ No files downloaded (nothing matched or already up-to-date)")
						return
					}
					util.Default.Printf("⬇️  Downloaded %d files:\n", len(downloaded))
					for _, f := range downloaded {
						util.Default.Printf(" - %s\n", f)
					}

				case "Upload":
					util.Default.Printf("🔁 Running Upload (%s) for prefixes: %v\n", modeChoice, prefixes)
					// Single-sync indexing intentionally bypasses remote .sync_ignore.
					if err := runRemoteIndexingForPull(cfg, true, prefixes); err != nil {
						util.Default.Printf("❌ Remote indexing (safe_push) failed: %v\n", err)
						return
					}

					var uploaded []string
					if strings.Contains(modeChoice, "Force") {
						ok, cerr := tui.ConfirmWithCaptcha("This action may delete files on remote/local. Proceed?", 3)
						if cerr != nil {
							util.Default.Printf("❌ Captcha error: %v\n", cerr)
							return
						}
						if !ok {
							return
						}
						uploaded, err = CompareAndUploadManualTransferForceParallel(cfg, localRoot, prefixes)
					} else if strings.Contains(modeChoice, "Bypass") {
						uploaded, err = CompareAndUploadManualTransferBypassParallel(cfg, localRoot, prefixes)
					} else {
						uploaded, err = CompareAndUploadManualTransfer(cfg, localRoot, prefixes)
					}

					if err != nil {
						util.Default.Printf("❌ Upload failed: %v\n", err)
						return
					}
					if len(uploaded) == 0 {
						util.Default.Println("✅ No files uploaded (nothing matched or already up-to-date)")
						return
					}
					util.Default.Printf("⬆️  Uploaded %d files:\n", len(uploaded))
					for _, f := range uploaded {
						util.Default.Printf(" - %s\n", f)
					}
				}
			}()
			// after operation, return to folder selection (per stepwise behavior)
		}
	}
}

// runRemoteIndexingForPull mirrors the safe_pull_sync flow: it builds the agent
// (with fallback) using remote detection, uploads agent and config, and runs
// remote indexing so the DB is fresh before pulling.
func runRemoteIndexingForPull(cfg *config.Config, bypassIgnore bool, prefixes []string) error {
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
	_, out, err := RunAgentIndexingFlow(cfg, []string{agentPath}, bypassIgnore, prefixes)
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
