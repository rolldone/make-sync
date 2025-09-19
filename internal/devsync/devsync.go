package devsync

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"make-sync/internal/config"
	"make-sync/internal/devsync/sshclient"
	"make-sync/internal/tui"
)

var watcher *Watcher

// ShowDevSyncModeMenu displays the DevSync mode selection menu
func ShowDevSyncModeMenu(cfg *config.Config) string {
	// Loop the menu so when a session exits we return to the menu.
	for {
		// Clear screen before showing menu
		if watcher != nil && watcher.printer != nil {
			watcher.printer.Print("\033[2J\033[1;1H")
			watcher.printer.Println("🚀 DevSync Mode Selection")
			watcher.printer.Println("==========================")
		} else {
			fmt.Print("\033[2J\033[1;1H")
			fmt.Println("🚀 DevSync Mode Selection")
			fmt.Println("==========================")
		}

		menuItems := []string{
			"safe_sync :: Basic sync with file watching",
			"safe_pull_sync :: Pull from remote then sync",
			"soft_push_sync :: Safe push to remote then sync",
			"force_single_sync :: Single file/folder transfer",
			"remote_session :: New remote session (no menu)",
			"remote_sessions :: Remote sessions menu",
			"local_sessions :: Local sessions menu",
			"back :: Return to main menu",
		}

		// pause legacy keyboard handler while TUI runs
		if watcher != nil {
			watcher.TUIActive = true
			select {
			case watcher.keyboardStop <- true:
			default:
			}
		}

		// use TUI menu (bubbletea + bubbles/list) to show selection
		result, err := tui.ShowMenu(menuItems, "Select DevSync Mode")
		if err != nil {
			if watcher != nil && watcher.printer != nil {
				watcher.printer.Printf("❌ Menu selection cancelled: %v\n", err)
			} else {
				fmt.Printf("❌ Menu selection cancelled: %v\n", err)
			}
			return "cancelled"
		}

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

		if watcher != nil && watcher.printer != nil {
			watcher.printer.Printf("Selected mode: %s\n", result)
		} else {
			fmt.Printf("Selected mode: %s\n", result)
		}

		// Handle selection
		switch i {
		case 0: // safe_sync
			// Start watcher - create watcher and initialize remote resources once
			if watcher == nil {
				var err error
				watcher, err = NewWatcher(cfg)
				if err != nil {
					if watcher != nil && watcher.printer != nil {
						watcher.printer.Printf("❌ Failed to create watcher: %v\n", err)
					} else {
						fmt.Printf("❌ Failed to create watcher: %v\n", err)
					}
					return "error"
				}
			}
			watcher.printer.Println("👀 Starting watcher (safe_sync). Press Ctrl-C to stop and return to menu.")
			if err := watcher.Start(); err != nil {
				watcher.printer.Printf("⚠️  Watcher exited with error: %v\n", err)
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
			if watcher != nil && watcher.printer != nil {
				watcher.printer.Println("🔗 Creating new remote session...")
			} else {
				fmt.Println("🔗 Creating new remote session...")
			}

			// Get absolute path for private key
			privateKeyPath := cfg.Devsync.Auth.PrivateKey
			if !filepath.IsAbs(privateKeyPath) {
				absPath, err := filepath.Abs(privateKeyPath)
				if err != nil {
					if watcher != nil && watcher.printer != nil {
						watcher.printer.Printf("❌ Failed to get absolute path for private key: %v\n", err)
					} else {
						fmt.Printf("❌ Failed to get absolute path for private key: %v\n", err)
					}
					// continue to menu
					continue
				}
				privateKeyPath = absPath
			}

			// Create SSH client directly
			sshClient, err := sshclient.NewPersistentSSHClient(
				cfg.Devsync.Auth.Username,
				privateKeyPath,
				cfg.Devsync.Auth.Host,
				cfg.Devsync.Auth.Port,
			)
			if err != nil {
				if watcher != nil && watcher.printer != nil {
					watcher.printer.Printf("❌ Failed to initialize SSH client: %v\n", err)
				} else {
					fmt.Printf("❌ Failed to initialize SSH client: %v\n", err)
				}
				// continue to menu
				continue
			}

			// Connect to SSH server
			if err := sshClient.Connect(); err != nil {
				if watcher != nil && watcher.printer != nil {
					watcher.printer.Printf("❌ Failed to connect SSH server: %v\n", err)
				} else {
					fmt.Printf("❌ Failed to connect SSH server: %v\n", err)
				}
				sshClient.Close()
				// continue to menu
				continue
			}
			if watcher != nil && watcher.printer != nil {
				watcher.printer.Printf("🔗 SSH client connected successfully\n")
			} else {
				fmt.Printf("🔗 SSH client connected successfully\n")
			}

			// Build the remote command that sets working directory and launches a shell
			remotePath := cfg.Devsync.Auth.RemotePath
			if remotePath == "" {
				remotePath = "/tmp"
			}
			remoteCommand := fmt.Sprintf("mkdir -p %s || true && cd %s && bash -l", remotePath, remotePath)

			// Create PTY-SSH bridge with initial command so working dir is set
			bridge, err := sshclient.NewPTYSSHBridgeWithCommand(sshClient, remoteCommand)
			if err != nil {
				if watcher != nil && watcher.printer != nil {
					watcher.printer.Printf("❌ Failed to create PTY-SSH bridge: %v\n", err)
				} else {
					fmt.Printf("❌ Failed to create PTY-SSH bridge: %v\n", err)
				}
				sshClient.Close()
				// continue to menu
				continue
			}
			// Start the interactive shell
			if watcher != nil && watcher.printer != nil {
				watcher.printer.Println("🔗 Starting interactive SSH session with PTY bridge...")
			} else {
				fmt.Println("🔗 Starting interactive SSH session with PTY bridge...")
			}
			// Install a small debug callback so we can verify the matcher runs
			cb := func(_ []byte) {
				// Print a visible debug marker to stderr
				fmt.Fprintf(os.Stderr, "DEBUG CALLBACK: Ctrl+G pressed (direct session)\n")
				// Write a marker file with timestamp
				fname := "/tmp/make-sync-direct-callback.log"
				if f, err := os.OpenFile(fname, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
					defer f.Close()
					f.WriteString(time.Now().Format(time.RFC3339) + " callback fired\n")
				}
			}

			if err := bridge.StartInteractiveShell(cb); err != nil {
				if watcher != nil && watcher.printer != nil {
					watcher.printer.Printf("❌ Failed to start interactive shell: %v\n", err)
				} else {
					fmt.Printf("❌ Failed to start interactive shell: %v\n", err)
				}
			}
			// Ensure bridge and client are closed before returning to menu
			bridge.Close()
			sshClient.Close()

			// After the interactive session ends, loop back to the menu
			continue
		case 5: // remote_sessions
			return "remote_sessions"
		case 6: // local_sessions
			return "local_sessions"
		case 7: // back
			return "back"
		default:
			return "invalid"
		}
	}
}
