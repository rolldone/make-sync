package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"make-sync/internal/config"
	"make-sync/internal/syncdata"
	"make-sync/internal/tui"
	"make-sync/internal/util"

	"github.com/spf13/cobra"
)

var (
	pullForce        bool
	pullGitChanges   bool
	pullBypassIgnore bool
	pullPaths        []string
)

var pullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Pull (download) files from remote — one-shot sync",
	Long: `Download remote files to local.

Modes:
  make-sync pull                  Soft pull — download only changed files (safe, no deletes)
  make-sync pull --force          Force pull — download + delete local files not present on remote
  make-sync pull --path <path>    Scoped pull — only sync specific files/folders (repeatable)
  make-sync pull --git-changes    Git-aware pull — sync only files modified in git working tree
  make-sync pull --bypass-ignore  Bypass .sync_ignore filtering for --git-changes (include ignored files)

Flags can be combined:
  make-sync pull --force --git-changes        Force pull only git-modified files
  make-sync pull --path src --git-changes     Intersection: git-modified files inside src/
  make-sync pull --git-changes --bypass-ignore  Git-modified files including ignored/untracked ones`,
	Run: func(cmd *cobra.Command, args []string) {
		// Validate config exists
		if !config.ConfigExists() {
			fmt.Fprintln(os.Stderr, "Config file not found. Run 'make-sync init' first.")
			os.Exit(1)
		}

		cfg, err := config.LoadAndRenderConfigQuiet()
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to load config: %v\n", err)
			os.Exit(1)
		}

		// Determine local root
		localRoot := resolveLocalRoot(cfg)

		// Resolve prefixes from --path and/or --git-changes
		hasScope := len(pullPaths) > 0 || pullGitChanges
		prefixes := resolvePushPullPrefixes(localRoot, pullPaths, pullGitChanges, pullBypassIgnore)

		if hasScope {
			// Scoped pull (--path / --git-changes) — single-sync style
			absRoot, _ := filepath.Abs(localRoot)
			if pullBypassIgnore {
				// Bypass mode: disable all ignore filtering
				syncdata.SetManualTransferIgnoreProfiles(map[string][]string{})
			} else {
				manualProfiles := syncdata.BuildManualIgnoreProfilesForSelection(cfg, absRoot, prefixes)
				syncdata.SetManualTransferIgnoreProfiles(manualProfiles)
			}
			defer syncdata.ClearManualTransferIgnoreProfiles()

			if pullForce {
				ok, cerr := tui.ConfirmWithCaptcha("Force pull may delete local files. Proceed?", 3)
				if cerr != nil {
					fmt.Fprintf(os.Stderr, "❌ Captcha error: %v\n", cerr)
					os.Exit(1)
				}
				if !ok {
					util.Default.Println("⚠️  Force pull cancelled.")
					return
				}
			}

			// Indexing: bypass remote .sync_ignore (single-sync behavior)
			util.Default.Printf("🔁 Indexing remote for prefixes: %v\n", prefixes)
			if err := syncdata.RunRemoteIndexingForPull(cfg, true, prefixes); err != nil {
				fmt.Fprintf(os.Stderr, "❌ Remote indexing failed: %v\n", err)
				os.Exit(1)
			}

			var downloaded []string
			if pullForce {
				downloaded, err = syncdata.CompareAndDownloadManualTransferForceParallel(cfg, localRoot, prefixes)
			} else if pullBypassIgnore {
				downloaded, err = syncdata.CompareAndDownloadManualTransferBypassParallel(cfg, localRoot, prefixes)
			} else {
				downloaded, err = syncdata.CompareAndDownloadManualTransferParallel(cfg, localRoot, prefixes)
			}

			if err != nil {
				fmt.Fprintf(os.Stderr, "❌ Pull failed: %v\n", err)
				os.Exit(1)
			}

			if len(downloaded) == 0 {
				util.Default.Println("✅ No files downloaded (already up-to-date).")
			} else {
				util.Default.Printf("⬇️  Downloaded %d files:\n", len(downloaded))
				for _, f := range downloaded {
					util.Default.Printf(" - %s\n", f)
				}
			}
		} else {
			// Full-project pull (no --path, no --git-changes)
			sshClient, err := syncdata.ConnectSSH(cfg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "❌ SSH connect failed: %v\n", err)
				os.Exit(1)
			}
			defer sshClient.Close()

			mode := "Safe"
			if pullForce {
				ok, cerr := tui.ConfirmWithCaptcha("Force pull may delete local files across entire project. Proceed?", 3)
				if cerr != nil {
					fmt.Fprintf(os.Stderr, "❌ Captcha error: %v\n", cerr)
					os.Exit(1)
				}
				if !ok {
					util.Default.Println("⚠️  Force pull cancelled.")
					return
				}
				if pullBypassIgnore {
					mode = "Force (Bypass)"
				} else {
					mode = "Force"
				}
			} else if pullBypassIgnore {
				mode = "Bypass"
			}

			result := syncdata.RunPullWithMode(cfg, sshClient, mode)
			if !result.Success {
				if result.Error != nil {
					fmt.Fprintf(os.Stderr, "❌ Pull failed: %v\n", result.Error)
				}
				os.Exit(1)
			}
		}
	},
}

func init() {
	pullCmd.Flags().BoolVar(&pullForce, "force", false, "Force mode — may delete local files not present on remote")
	pullCmd.Flags().BoolVar(&pullGitChanges, "git-changes", false, "Sync only files modified in git working tree (Modified, Added, Untracked)")
	pullCmd.Flags().BoolVar(&pullBypassIgnore, "bypass-ignore", false, "Bypass .sync_ignore filtering (include ignored/untracked files)")
	pullCmd.Flags().StringSliceVar(&pullPaths, "path", nil, "Scope to specific file/folder (repeatable, e.g. --path src/index.php --path public)")
}
