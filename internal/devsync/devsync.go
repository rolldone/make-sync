package devsync

import (
	"context"
	"fmt"
	"log"
	"make-sync/internal/config"
	"make-sync/internal/deployagent"
	"make-sync/internal/events"
	"make-sync/internal/sshclient"
	"make-sync/internal/syncdata"
	"make-sync/internal/tui"
	"make-sync/internal/util"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var watcher *Watcher

// ShowDevSyncModeMenu displays the DevSync mode selection menu
// and respects context cancellation.
func ShowDevSyncModeMenu(ctx context.Context, cfg *config.Config) string {
	// oldstate, err := term.MakeRaw(int(os.Stdin.Fd()))
	// if err != nil {
	// 	util.Default.Printf("❌ Failed to enable raw mode: %v\n", err)
	// 	return "error"
	// }
	// defer func() {
	// 	term.Restore(int(os.Stdin.Fd()), oldstate)
	// 	util.Default.Printf("✅ Terminal state restored\n")
	// }()
	// Loop the menu so when a session exits we return to the menu.
	for {
		select {
		case <-ctx.Done():
			util.Default.Println("⏹ DevSync menu canceled")
			events.GlobalBus.Publish(events.EventCleanupRequested) // Publish cleanup event instead of direct call
			return "cancelled"
		default:
		}
		// Clear screen before showing menu
		util.Default.Print("\033[2J\033[1;1H")
		util.Default.Println("🚀 DevSync Mode Selection")
		util.Default.Println("==========================")

		menuItems := []string{
			"safe_sync :: Basic sync with file watching",
			"safe_pull_sync :: Pull from remote then sync",
			"soft_push_sync :: Safe push to remote then sync",
			"force_manual_sync :: Single file/folder transfer",
			"remote_session :: New remote session",
			"back :: Return to main menu",
		}

		// // pause legacy keyboard handler while TUI runs and let TUI own the terminal
		// if watcher != nil {
		// 	select {
		// 	case watcher.keyboardStop <- true:
		// 	default:
		// 	}
		// 	// wait up to 500ms for keyboard handler to ack that it stopped
		// 	select {
		// 	case <-watcher.keyboardStopped:
		// 		// acknowledged
		// 	case <-time.After(500 * time.Millisecond):
		// 		// timeout - continue anyway
		// 	}
		// 	watcher.TUIActive = true
		// }
		// // inform util that TUI owns the terminal so global raw-mode helpers are no-ops
		// util.TUIActive = true

		// use TUI menu (bubbletea + bubbles/list) to show selection
		result, err := tui.ShowMenuWithPrints(menuItems, "Select DevSync Mode")
		if err != nil {
			util.Default.Printf("❌ Menu selection cancelled: %v\n", err)
			util.Default.Printf("🧹 Publishing cleanup event...\n")
			util.Default.ClearLine()
			events.GlobalBus.Publish(events.EventCleanupRequested) // Publish cleanup event instead of direct call
			util.Default.Printf("✅ Cleanup event published\n")
			return "cancelled"
		}

		// restore ownership back to legacy input handler
		if watcher != nil {
			select {
			case watcher.keyboardRestart <- true:
			default:
			}
			watcher.TUIActive = false
		}

		// derive index to keep existing switch logic
		i := -1
		for idx, it := range menuItems {
			if it == result {
				i = idx
				break
			}
		}
		if i == -1 {
			// fallback: try matching prefix
			for idx, it := range menuItems {
				if len(result) > 0 && len(it) >= len(result) && it[:len(result)] == result {
					i = idx
					break
				}
			}
		}

		util.Default.Printf("Selected mode: %s\n", result)
		util.Default.ClearLine()

		// Handle selection
		switch i {
		case 0: // safe_sync
			// Start watcher - create watcher and initialize remote resources once
			if watcher == nil {
				var err error
				watcher, err = NewWatcher(cfg)
				if err != nil {
					util.Default.Printf("❌ Failed to create watcher: %v\n", err)
					return "error"
				}
			}
			util.Default.Println("👀 Starting watcher (safe_sync). Press Ctrl-C to stop and return to menu.")
			if err := watcher.Start(); err != nil {
				util.Default.Printf("⚠️  Watcher exited with error: %v\n", err)
			}
			// After watcher stops, loop back to the menu
			continue
		case 1: // safe_pull_sync
			util.Default.Println("🔁 safe_pull_sync selected — building and deploying agent for remote indexing...")

			// Determine target OS from config
			targetOS := cfg.Devsync.OSTarget
			if targetOS == "" {
				targetOS = "linux" // Default to linux
			}

			// Get project root using same logic as watcher
			watchPath := cfg.LocalPath
			if watchPath == "" {
				watchPath = "."
			}
			absWatchPath, err := filepath.Abs(watchPath)
			if err != nil {
				util.Default.Printf("⚠️  Failed to get absolute watch path: %v\n", err)
				absWatchPath = watchPath
			}
			projectRoot := filepath.Dir(absWatchPath)

			// Connect SSH first
			sshClient, err := createSSHClient(cfg)
			if err != nil {
				util.Default.Printf("❌ Failed to connect SSH: %v\n", err)
				return "error"
			}
			defer sshClient.Close()

			// Create SSH adapter
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
					return "error"
				}
			}

			util.Default.Printf("✅ Agent ready: %s\n", agentPath)

			// Agent will be uploaded and indexing performed by RunAgentIndexingFlow

			// Deploy agent+config is handled by RunAgentIndexingFlow (which uploads
			// the agent/config then runs the remote 'indexing' command). Call it
			// with the locally-built agent path as the candidate.
			_, out, err := syncdata.RunAgentIndexingFlow(cfg, []string{agentPath})
			if err != nil {
				util.Default.Printf("❌ Remote indexing failed: %v\n", err)
				util.Default.Printf("🔍 Remote output (partial): %s\n", out)
				return "error"
			}

			// Download indexing DB into local project .sync_temp
			// Prefer the configured LocalPath from config (set during LoadAndRenderConfig).
			// Fallback order:
			// 1. cfg.LocalPath (top-level)
			// 2. cfg.Devsync.Auth.LocalPath
			// 3. current working directory via util.GetLocalWorkingDir()
			var downloadTarget string
			if cfg.LocalPath != "" {
				downloadTarget = cfg.LocalPath
			} else if cfg.Devsync.Auth.LocalPath != "" {
				downloadTarget = cfg.Devsync.Auth.LocalPath
			}

			localDBPath, derr := syncdata.DownloadIndexDB(cfg, downloadTarget)
			if derr != nil {
				util.Default.Printf("⚠️  Indexing finished but failed to download DB: %v\n", derr)
			} else {
				util.Default.Printf("✅ Index DB downloaded to: %s\n", localDBPath)
			}

			// Ensure we have a concrete local root to compare against.
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
			downloadedFiles, cerr := syncdata.CompareAndDownloadByHash(cfg, compareTarget)
			if cerr != nil {
				util.Default.Printf("❌ Compare/download failed: %v\n", cerr)
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
			return "safe_pull_sync"
		case 2: // soft_push_sync -> safe_push_sync
			util.Default.Println("🔁 safe_push_sync selected — building and deploying agent for remote indexing...")

			// Determine target OS from config
			targetOS := cfg.Devsync.OSTarget
			if targetOS == "" {
				targetOS = "linux"
			}

			// Get project root using same logic as watcher
			watchPath := cfg.LocalPath
			if watchPath == "" {
				watchPath = "."
			}
			absWatchPath, err := filepath.Abs(watchPath)
			if err != nil {
				util.Default.Printf("⚠️  Failed to get absolute watch path: %v\n", err)
				absWatchPath = watchPath
			}
			projectRoot := filepath.Dir(absWatchPath)

			// Connect SSH first
			sshClient, err := createSSHClient(cfg)
			if err != nil {
				util.Default.Printf("❌ Failed to connect SSH: %v\n", err)
				return "error"
			}
			defer sshClient.Close()

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
					return "error"
				}
			}

			util.Default.Printf("✅ Agent ready: %s\n", agentPath)

			// Run remote indexing and download DB
			_, out, err := syncdata.RunAgentIndexingFlow(cfg, []string{agentPath})
			if err != nil {
				util.Default.Printf("❌ Remote indexing failed: %v\n", err)
				util.Default.Printf("🔍 Remote output (partial): %s\n", out)
				return "error"
			}

			// Determine download target (local root)
			var downloadTarget string
			if cfg.LocalPath != "" {
				downloadTarget = cfg.LocalPath
			} else if cfg.Devsync.Auth.LocalPath != "" {
				downloadTarget = cfg.Devsync.Auth.LocalPath
			}

			localDBPath, derr := syncdata.DownloadIndexDB(cfg, downloadTarget)
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
			uploaded, uerr := syncdata.CompareAndUploadByHash(cfg, compareTarget)
			if uerr != nil {
				util.Default.Printf("❌ Compare/upload failed: %v\n", uerr)
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
			return "safe_push_sync"
		case 3: // force_manual_sync
			// Delegate interactive single-sync menu to syncdata package so devsync
			// stays small. Determine local root preference similar to other flows.
			localRoot := cfg.LocalPath
			if localRoot == "" {
				localRoot = cfg.Devsync.Auth.LocalPath
			}
			if localRoot == "" {
				// fallback to current working dir
				if wd, err := os.Getwd(); err == nil {
					localRoot = wd
				} else {
					localRoot = "."
				}
			}
			syncdata.ForceSingleSyncMenu(cfg, localRoot)
			// after single sync returns, continue to show devsync menu
			continue
		case 4: // remote_session
			err := basicNewSessionSSH(cfg)
			if err != nil {
				util.Default.Printf("❌ Remote session failed: %v\n", err)
				continue
			}
			continue
			// After the interactive session ends, loop back to the menu
		case 6: // local_sessions
			return "local_sessions"
		case 7: // back
			return "back"
		default:
			return "invalid"
		}
	}
}

func basicNewSessionSSH(cfg *config.Config) error {
	// Get absolute path for private key
	privateKeyPath := strings.TrimSpace(cfg.Devsync.Auth.PrivateKey)
	// Only resolve to absolute path if a private key path is actually provided
	if privateKeyPath != "" && !filepath.IsAbs(privateKeyPath) {
		absPath, err := filepath.Abs(privateKeyPath)
		if err != nil {
			util.Default.Printf("❌ Failed to get absolute path for private key: %v\n", err)
			// continue to menu
			return err
		}
		privateKeyPath = absPath
	}

	// Create SSH client directly
	sshClient, err := sshclient.NewPersistentSSHClient(
		cfg.Devsync.Auth.Username,
		privateKeyPath,
		cfg.Devsync.Auth.Password,
		cfg.Devsync.Auth.Host,
		cfg.Devsync.Auth.Port,
	)
	if err != nil {
		util.Default.Printf("❌ Failed to initialize SSH client: %v\n", err)
		// continue to menu
		os.Exit(1)
		return err
	}

	// Connect to SSH server
	if err := sshClient.Connect(); err != nil {
		util.Default.Printf("❌ Failed to connect SSH server: %v\n", err)
		sshClient.Close()
		os.Exit(1)
		// continue to menu
		return err
	}
	util.Default.Printf("🔗 SSH client connected successfully\n")
	util.Default.ClearLine()

	// Build the remote command that sets working directory and launches a shell
	remotePath := cfg.Devsync.Auth.RemotePath
	osTarget := strings.ToLower(strings.TrimSpace(cfg.Devsync.OSTarget))
	if remotePath == "" {
		if strings.Contains(osTarget, "win") {
			// use temp on remote Windows if not provided
			remotePath = "%TEMP%"
		} else {
			remotePath = "/tmp"
		}
	}

	var remoteCommand string
	if strings.Contains(osTarget, "win") {
		// Build a robust cmd.exe command:
		// Try to change directory first (handles existing directory and drive changes).
		// If that fails, create the directory and cd into it. Avoid wrapping the whole
		// command in an extra pair of double-quotes because that can prevent cmd from
		// parsing inner quoted paths correctly and cause the session to land in the
		// user's home directory instead.
		// Example result:
		// cmd.exe /K cd /d "C:\path\to\dir" 2>nul || (mkdir "C:\path\to\dir" && cd /d "C:\path\to\dir")
		remoteCommand = fmt.Sprintf("cmd.exe /K cd /d \"%s\" 2>nul || (mkdir \"%s\" && cd /d \"%s\")", remotePath, remotePath, remotePath)
		log.Printf("DEBUG: remoteCommand=%s\n", remoteCommand)
	} else {
		// Unix-like: mkdir + cd + bash
		remoteCommand = fmt.Sprintf("mkdir -p %s || true && cd %s && exec bash", remotePath, remotePath)
	}
	// Create PTY-SSH bridge with initial command so working dir is set
	bridge, err := sshclient.NewPTYSSHBridgeWithCommand(sshClient, remoteCommand)
	if err != nil {
		util.Default.Printf("❌ Failed to create PTY-SSH bridge: %v\n", err)

		os.Exit(1)
		sshClient.Close()
		// continue to menu

		return err
	}
	// Start the interactive shell
	util.Default.Println("🔗 Starting interactive SSH session with PTY bridge...")

	bridge.SetOnExitListener(func() {
		// close(routerStop)
		log.Println("🔗 SSH session exited, closing bridge...")
	})

	bridge.SetOnInputHitCodeListener(func(code string) {
		log.Printf("DEBUG: Input hit code: 0x%02x\n", code)
	})

	bridge.SetOnInputListener(func(data []byte) {
		// Uncomment to debug all input data
		// util.Default.Printf("DEBUG: Input data: %q\n", data)

	})

	go func() {
		time.Sleep(500 * time.Millisecond)
		bridge.GetStdinWriter().Write([]byte("\033[2J\033[1;1H")) // Clear screen then send newline
	}()

	if err := bridge.StartInteractiveShell(); err != nil {
		util.Default.Printf("❌ Failed to start interactive shell: %v\n", err)
	}

	// Ensure bridge and client are closed before returning to menu
	bridge.Close()
	sshClient.Close()

	flushStdin()
	sendKeyA()
	time.Sleep(70 * time.Millisecond)
	return nil
}

// platform-specific implementations of flushStdin() and sendEnter()
// are provided in separate files with build tags (termio_windows.go / termio_unix.go)

// createSSHClient creates and connects an SSH client using values from cfg.Devsync.Auth
func createSSHClient(cfg *config.Config) (*sshclient.SSHClient, error) {
	auth := cfg.Devsync.Auth
	username := auth.Username
	privateKey := auth.PrivateKey
	password := auth.Password
	host := auth.Host
	port := auth.Port
	if port == "" {
		port = "22"
	}

	client, err := sshclient.NewPersistentSSHClient(username, privateKey, password, host, port)
	if err != nil {
		return nil, fmt.Errorf("failed to create ssh client: %v", err)
	}
	if err := client.Connect(); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to connect ssh client: %v", err)
	}
	return client, nil
}
