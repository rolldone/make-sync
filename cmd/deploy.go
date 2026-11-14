package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"make-sync/internal/config"
	"make-sync/internal/sshclient"
	"make-sync/internal/syncdata"
	"make-sync/internal/tui"
	"make-sync/internal/util"

	"github.com/spf13/cobra"
)

// deployBatchSize limits how many remote paths are passed to a single delete
// command so the command line stays reasonable.
const deployBatchSize = 100

var deploySinceCommit string

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploy files changed since a git commit (delta push, no indexing)",
	Long: `Deploy the files touched by a commit range to the remote server.

Unlike 'push', deploy does not index the remote and git is the source of truth:
files changed in <rev>..HEAD (plus uncommitted working tree changes) are the
candidates, and files deleted in that range are deleted on the remote after an
explicit confirmation.

This is a delta push: a single SSH exec hashes the candidate files with
sha256sum and files already identical on the remote are skipped, so only files
whose content differs are transferred. Manual edits made directly on the server
are still overwritten. Deletions are scoped to the commit range only — deploy
never sweeps "missing locally" files.

If the remote pre-check cannot run (sha256sum unavailable, exec error, or
unparseable output) deploy falls back to a blind push, and Windows targets are
uploaded blindly because sha256sum is not standard on cmd.exe.

Examples:
  make-sync deploy --since-commit HEAD~1
  make-sync deploy --since-commit v1.2.0
  make-sync deploy --since-commit 8d9020b`,
	Run: func(cmd *cobra.Command, args []string) {
		if err := runDeploy(deploySinceCommit); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Deploy failed: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	deployCmd.Flags().StringVar(&deploySinceCommit, "since-commit", "", "Deploy changes since this git revision (required, e.g. HEAD~1, a tag, or a commit hash)")
}

// runDeploy executes the deploy flow. All validation happens before the SSH
// connection is opened.
func runDeploy(sinceCommit string) error {
	sinceCommit = strings.TrimSpace(sinceCommit)
	if sinceCommit == "" {
		return fmt.Errorf("--since-commit is required (e.g. --since-commit HEAD~1)")
	}

	if !config.ConfigExists() {
		return fmt.Errorf("config file not found — run 'make-sync init' first")
	}

	cfg, err := config.LoadAndRenderConfigQuiet()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	localRoot := resolveLocalRoot(cfg)
	absRoot, err := filepath.Abs(localRoot)
	if err != nil {
		return fmt.Errorf("cannot resolve local root %q: %w", localRoot, err)
	}

	// Git is the source of truth: resolve the range before touching the remote.
	lists, err := computeCommitRangeLists(absRoot, sinceCommit)
	if err != nil {
		return err
	}

	if len(lists.Upload) == 0 && len(lists.Deletion) == 0 {
		util.Default.Printf("✅ No files changed since %s — already up-to-date.\n", sinceCommit)
		return nil
	}

	remotePath := strings.TrimSpace(cfg.Devsync.Auth.RemotePath)
	if remotePath == "" {
		return fmt.Errorf("remotePath is empty in config — cannot deploy")
	}
	osTarget := strings.ToLower(strings.TrimSpace(cfg.Devsync.OSTarget))

	util.Default.Printf("🚀 Deploy since %s\n", sinceCommit)
	util.Default.Printf("   Remote root : %s\n", remotePath)
	util.Default.Printf("   Upload files: %d\n", len(lists.Upload))
	util.Default.Printf("   Delete files: %d\n", len(lists.Deletion))

	// Destructive deletions require explicit confirmation, rendered after the
	// list is visible to the operator.
	deleteConfirmed := true
	if len(lists.Deletion) > 0 {
		util.Default.Println("⚠️  These files will be DELETED on the remote:")
		for _, p := range lists.Deletion {
			util.Default.Printf("   - %s\n", p)
		}
		ok, cerr := tui.ConfirmWithCaptcha("Deploy will delete the files listed above on the remote. Proceed?", 3)
		if cerr != nil {
			return fmt.Errorf("captcha error: %w", cerr)
		}
		if !ok {
			deleteConfirmed = false
			util.Default.Println("⚠️  Deletions skipped — uploads will still run.")
		}
	}

	sshClient, err := syncdata.ConnectSSH(cfg)
	if err != nil {
		return fmt.Errorf("SSH connect failed: %w", err)
	}
	defer sshClient.Close()

	// 1. Delta pre-check, then upload only the files that need transferring.
	// POSIX targets hash the remote in one exec; Windows (no standard
	// sha256sum on cmd.exe) falls back directly to a blind upload.
	if len(lists.Upload) > 0 {
		uploadList := lists.Upload
		if strings.Contains(osTarget, "win") {
			util.Default.Println("   Delta check : skipped (Windows target → blind upload)")
		} else {
			delta := planDeployDelta(sshClient, absRoot, remotePath, lists.Upload)
			uploadList = delta.Send
			util.Default.Printf("   Delta check : SKIP %d / SEND %d\n", len(delta.Skip), len(delta.Send))
			if len(delta.Send) == 0 {
				util.Default.Println("✅ up-to-date (semua hash sama) — tidak ada file yang perlu dikirim.")
			}
		}

		if len(uploadList) > 0 {
			pairs := buildDeployUploadPairs(absRoot, remotePath, osTarget, uploadList)
			if strings.Contains(osTarget, "win") {
				for _, pair := range pairs {
					if uerr := sshClient.UploadFile(pair.Local, pair.Remote); uerr != nil {
						return fmt.Errorf("upload %s failed: %w", pair.Local, uerr)
					}
					util.Default.Printf("⬆️  %s\n", pair.Remote)
				}
			} else {
				uploaded, uerr := sshClient.UploadFilesSFTP(pairs, 4)
				if uerr != nil {
					return fmt.Errorf("upload failed (%d/%d ok): %w", len(uploaded), len(pairs), uerr)
				}
				util.Default.Printf("⬆️  Uploaded %d files.\n", len(uploaded))
			}
		}
	}

	// 2. Scoped deletions (never a full sweep).
	if deleteConfirmed && len(lists.Deletion) > 0 {
		for _, batch := range buildDeployDeleteCommands(remotePath, osTarget, lists.Deletion) {
			if derr := sshClient.RunCommand(batch); derr != nil {
				return fmt.Errorf("delete failed: %w", derr)
			}
		}
		util.Default.Printf("🗑️  Deleted %d files on remote.\n", len(lists.Deletion))
	}

	util.Default.Println("✅ Deploy finished.")
	return nil
}

// buildDeployUploadPairs maps local absolute paths to remote paths under
// remotePath, preserving the relative structure.
func buildDeployUploadPairs(absRoot, remotePath, osTarget string, uploads []string) []sshclient.UploadPair {
	pairs := make([]sshclient.UploadPair, 0, len(uploads))
	for _, rel := range uploads {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
			continue
		}
		pairs = append(pairs, sshclient.UploadPair{
			Local:  filepath.Join(absRoot, filepath.FromSlash(rel)),
			Remote: deployRemotePath(remotePath, rel, osTarget),
		})
	}
	return pairs
}

// deployRemotePath joins the remote root with a relative forward-slash path.
// Windows targets use backslashes; POSIX targets use forward slashes.
func deployRemotePath(remotePath, rel, osTarget string) string {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if strings.Contains(strings.ToLower(osTarget), "win") {
		base := strings.ReplaceAll(strings.TrimSpace(remotePath), "/", "\\")
		base = strings.TrimRight(base, "\\")
		return base + "\\" + strings.ReplaceAll(rel, "/", "\\")
	}
	base := filepath.ToSlash(strings.TrimSpace(remotePath))
	base = strings.TrimRight(base, "/")
	return base + "/" + rel
}

// buildDeployDeleteCommands returns shell commands that delete exactly the
// given paths on the remote. Paths are quoted for the remote shell; POSIX
// paths are batched to keep command lines bounded.
func buildDeployDeleteCommands(remotePath, osTarget string, deletions []string) []string {
	if len(deletions) == 0 {
		return nil
	}

	if strings.Contains(strings.ToLower(osTarget), "win") {
		commands := make([]string, 0, len(deletions))
		for _, rel := range deletions {
			remote := deployRemotePath(remotePath, rel, osTarget)
			commands = append(commands, fmt.Sprintf("cmd.exe /C del /F /Q %s", quoteWindows(remote)))
		}
		return commands
	}

	commands := make([]string, 0, (len(deletions)+deployBatchSize-1)/deployBatchSize)
	for start := 0; start < len(deletions); start += deployBatchSize {
		end := start + deployBatchSize
		if end > len(deletions) {
			end = len(deletions)
		}
		quoted := make([]string, 0, end-start)
		for _, rel := range deletions[start:end] {
			quoted = append(quoted, shellEscapePOSIX(deployRemotePath(remotePath, rel, osTarget)))
		}
		commands = append(commands, "rm -f -- "+strings.Join(quoted, " "))
	}
	return commands
}
