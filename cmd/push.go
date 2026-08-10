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
	pushForce        bool
	pushGitChanges   bool
	pushBypassIgnore bool
	pushPaths        []string
)

var pushCmd = &cobra.Command{
	Use:   "push",
	Short: "Push (upload) files to remote — one-shot sync",
	Long: `Upload local files to the remote server.

Modes:
  make-sync push                  Soft push — upload only changed files (safe, no deletes)
  make-sync push --force          Force push — upload + delete remote files not present locally
  make-sync push --path <path>    Scoped push — only sync specific files/folders (repeatable)
  make-sync push --bypass-ignore  Bypass .sync_ignore filtering (include ignored files)

Catatan:
  --git-changes sementara dinonaktifkan. Gunakan --path <path> untuk scope
  file/folder tertentu, atau tanpa flag scope untuk full project sync.`,
	Run: func(cmd *cobra.Command, args []string) {
		// --git-changes sementara dinonaktifkan
		if pushGitChanges {
			fmt.Fprintln(os.Stderr, "❌ --git-changes sementara dinonaktifkan. Gunakan --path <path> untuk scope, atau tanpa flag untuk full project sync.")
			os.Exit(1)
		}

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
		hasScope := len(pushPaths) > 0 || pushGitChanges
		prefixes := resolvePushPullPrefixes(localRoot, pushPaths, pushGitChanges, pushBypassIgnore)

		if hasScope {
			// Scoped push (--path / --git-changes) — single-sync style
			absRoot, _ := filepath.Abs(localRoot)
			if pushBypassIgnore {
				// Bypass mode: disable all ignore filtering
				syncdata.SetManualTransferIgnoreProfiles(map[string][]string{})
			} else {
				manualProfiles := syncdata.BuildManualIgnoreProfilesForSelection(cfg, absRoot, prefixes)
				syncdata.SetManualTransferIgnoreProfiles(manualProfiles)
			}
			defer syncdata.ClearManualTransferIgnoreProfiles()

			if pushForce {
				ok, cerr := tui.ConfirmWithCaptcha("Force push may delete remote files. Proceed?", 3)
				if cerr != nil {
					fmt.Fprintf(os.Stderr, "❌ Captcha error: %v\n", cerr)
					os.Exit(1)
				}
				if !ok {
					util.Default.Println("⚠️  Force push cancelled.")
					return
				}
			}

			// Indexing: bypass remote .sync_ignore (single-sync behavior)
			util.Default.Printf("🔁 Indexing remote for prefixes: %v\n", prefixes)
			if err := syncdata.RunRemoteIndexingForPull(cfg, true, prefixes); err != nil {
				fmt.Fprintf(os.Stderr, "❌ Remote indexing failed: %v\n", err)
				os.Exit(1)
			}

			var uploaded []string
			if pushForce {
				uploaded, err = syncdata.CompareAndUploadManualTransferForceParallel(cfg, localRoot, prefixes)
			} else if pushBypassIgnore {
				uploaded, err = syncdata.CompareAndUploadManualTransferBypassParallel(cfg, localRoot, prefixes)
			} else {
				uploaded, err = syncdata.CompareAndUploadManualTransfer(cfg, localRoot, prefixes)
			}

			if err != nil {
				fmt.Fprintf(os.Stderr, "❌ Push failed: %v\n", err)
				os.Exit(1)
			}

			if len(uploaded) == 0 {
				util.Default.Println("✅ No files uploaded (already up-to-date).")
			} else {
				util.Default.Printf("⬆️  Uploaded %d files:\n", len(uploaded))
				for _, f := range uploaded {
					util.Default.Printf(" - %s\n", f)
				}
			}
		} else {
			// Full-project push (no --path, no --git-changes)
			sshClient, err := syncdata.ConnectSSH(cfg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "❌ SSH connect failed: %v\n", err)
				os.Exit(1)
			}
			defer sshClient.Close()

			mode := "Safe"
			if pushForce {
				ok, cerr := tui.ConfirmWithCaptcha("Force push may delete remote files across entire project. Proceed?", 3)
				if cerr != nil {
					fmt.Fprintf(os.Stderr, "❌ Captcha error: %v\n", cerr)
					os.Exit(1)
				}
				if !ok {
					util.Default.Println("⚠️  Force push cancelled.")
					return
				}
				if pushBypassIgnore {
					mode = "Force (Bypass)"
				} else {
					mode = "Force"
				}
			} else if pushBypassIgnore {
				mode = "Bypass"
			}

			result := syncdata.RunPushWithMode(cfg, sshClient, mode)
			if !result.Success {
				if result.Error != nil {
					fmt.Fprintf(os.Stderr, "❌ Push failed: %v\n", result.Error)
				}
				os.Exit(1)
			}
		}
	},
}

func init() {
	pushCmd.Flags().BoolVar(&pushForce, "force", false, "Force mode — may delete remote files not present locally")
	pushCmd.Flags().BoolVar(&pushGitChanges, "git-changes", false, "Sementara dinonaktifkan — akan ditolak jika dipakai")
	pushCmd.Flags().BoolVar(&pushBypassIgnore, "bypass-ignore", false, "Bypass .sync_ignore filtering (include ignored/untracked files)")
	pushCmd.Flags().StringSliceVar(&pushPaths, "path", nil, "Scope to specific file/folder (repeatable, e.g. --path src/index.php --path public)")
}
