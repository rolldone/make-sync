package devsync

import (
	"context"
	"fmt"
	"log"
	"make-sync/internal/config"
	"make-sync/internal/events"
	"make-sync/internal/sshclient"
	"make-sync/internal/syncdata"
	"make-sync/internal/tui"
	"make-sync/internal/util"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

var watcher *Watcher

// ShowDevSyncModeMenu displays the DevSync mode selection menu
// and respects context cancellation.
func ShowDevSyncModeMenu(ctx context.Context, cfg *config.Config) string {
	oldStage, err := term.GetState(int(os.Stdin.Fd()))
	if err != nil {
		util.Default.Printf("❌ Failed to get current terminal state: %v\n", err)
	}
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
mainMenuLoop:
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
			"Sync: Pull (from remote)",
			"Sync: Push (to remote)",
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
		case 1: // Sync: Pull (from remote)
			util.ResetRaw(oldStage)

			// Mode submenu: show fast menu first, defer SSH connect until user confirms
			// Note: bypass options removed from top-level Pull menu (bypass remains available in other flows)
			mode, err := tui.ShowMenuWithPrints([]string{"Safe (no deletes)", "Force (may delete remote/local)", "Back"}, "Pull — choose mode")
			if err != nil {
				util.Default.Printf("❌ Mode selection cancelled: %v\n", err)
				continue
			}
			if mode == "Back" || mode == "cancelled" {
				continue
			}

			// If force selected, require captcha up front (no SSH needed for captcha)
			if strings.Contains(mode, "Force") {
				ok, cerr := tui.ConfirmWithCaptcha("This action may delete files on remote/local. Proceed?", 3)
				if cerr != nil {
					util.Default.Printf("❌ Captcha error: %v\n", cerr)
					continue
				}
				if !ok {
					continue
				}
			}

			// Now connect SSH (may be slower) only when we're actually going to run the operation
			sshClient, err := createSSHClient(cfg)
			if err != nil {
				util.Default.Printf("❌ Failed to connect SSH: %v\n", err)
				return "error"
			}
			defer sshClient.Close()

			// Run pull with chosen mode. Allow the user to retry the same
			// operation from the post-operation menu without reconnecting.
		pullLoop:
			for {
				result := syncdata.RunPullWithMode(cfg, sshClient, mode)
				if !result.Success {
					util.Default.Printf("❌ Pull failed: %v\n", result.Error)
					return "error"
				}

				// Show post-operation menu; if user requests retry, loop again
				action := syncdata.ShowPostSafePullMenu()
				if action == syncdata.RetryOperation {
					util.Default.Println("🔄 Retrying safe pull as requested by post-menu...")
					continue pullLoop
				}
				break
			}
			// after leaving pullLoop, return to main menu
			continue mainMenuLoop
		case 2: // Sync: Push (to remote)
			util.ResetRaw(oldStage)

			// Mode submenu first to avoid connecting SSH until user confirms
			// Note: bypass options removed from top-level Push menu (bypass remains available in other flows)
			mode, err := tui.ShowMenuWithPrints([]string{"Safe (no deletes)", "Force (may delete remote/local)", "Back"}, "Push — choose mode")
			if err != nil {
				util.Default.Printf("❌ Mode selection cancelled: %v\n", err)
				continue
			}
			if mode == "Back" || mode == "cancelled" {
				continue
			}

			if strings.Contains(mode, "Force") {
				ok, cerr := tui.ConfirmWithCaptcha("This action may delete files on remote/local. Proceed?", 3)
				if cerr != nil {
					util.Default.Printf("❌ Captcha error: %v\n", cerr)
					continue
				}
				if !ok {
					continue
				}
			}

			// Connect only when needed
			sshClient, err := createSSHClient(cfg)
			if err != nil {
				util.Default.Printf("❌ Failed to connect SSH: %v\n", err)
				return "error"
			}
			defer sshClient.Close()

			// Run push with chosen mode. Allow retry from the post-operation menu
			// so user can re-run the same push without reconnecting.
		pushLoop:
			for {
				result := syncdata.RunPushWithMode(cfg, sshClient, mode)
				if !result.Success {
					util.Default.Printf("❌ Push failed: %v\n", result.Error)
					return "error"
				}

				action := syncdata.ShowPostSafePushMenu()
				if action == syncdata.RetryOperation {
					util.Default.Println("🔄 Retrying safe push as requested by post-menu...")
					continue pushLoop
				}
				break
			}
			continue mainMenuLoop
		case 3: // force_manual_sync
			util.ResetRaw(oldStage)
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
	oldstate, err := util.NewRaw()
	if err != nil {
		util.Default.Printf("❌ Failed to enable raw mode: %v\n", err)
	}
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
		// time.Sleep(500 * time.Millisecond)
		// bridge.GetStdinWriter().Write([]byte("\033[2J\033[1;1H")) // Clear screen then send newline
	}()

	if err := bridge.StartInteractiveShell(); err != nil {
		util.Default.Printf("❌ Failed to start interactive shell: %v\n", err)
	}

	// Ensure bridge and client are closed before returning to menu
	bridge.Close()
	sshClient.Close()

	util.ResetRaw(oldstate)
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
