package devsync

import (
	"context"
	"fmt"
	"make-sync/internal/config"
	"make-sync/internal/devsync/sshclient"
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
			"force_single_sync :: Single file/folder transfer",
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
			util.Default.ClearLine()
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
			return "safe_pull_sync"
		case 2: // soft_push_sync
			return "soft_push_sync"
		case 3: // force_single_sync
			return "force_single_sync"
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
	privateKeyPath := cfg.Devsync.Auth.PrivateKey
	if !filepath.IsAbs(privateKeyPath) {
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
		util.Default.Printf("DEBUG: remoteCommand=%s\n", remoteCommand)
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
	// Install a small debug callback so we can verify the matcher runs
	cb := func(_ []byte) {
		// Print a visible debug marker to stderr
		util.Default.Printf("DEBUG CALLBACK: Ctrl+G pressed (direct session)\n")
		// Write a marker file with timestamp
		fname := "/tmp/make-sync-direct-callback.log"
		if f, err := os.OpenFile(fname, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
			defer f.Close()
			f.WriteString(time.Now().Format(time.RFC3339) + " callback fired\n")
		}
	}

	bridge.SetOnExitListener(func() {
		// close(routerStop)
	})

	bridge.SetOnInputHitCodeListener(func(code string) {
		util.Default.Printf("DEBUG: Input hit code: 0x%02x\n", code)
	})

	bridge.SetOnInputListener(func(data []byte) {
		// Uncomment to debug all input data
		// util.Default.Printf("DEBUG: Input data: %q\n", data)
	})

	if err := bridge.StartInteractiveShell(cb); err != nil {
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
