package devsync

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"

	"make-sync/internal/util"

	"github.com/manifoldco/promptui"
	"golang.org/x/term"
)

// promptMu serializes creation/running of promptui/readline prompts because
// the underlying readline package registers global handlers that are not
// safe to initialize concurrently.
var promptMu sync.Mutex

// displayMainMenu moved to view so watcher UI code is grouped
func (w *Watcher) displayMainMenu() {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		w.safePrintln("⚠️  failed to snapshot terminal state:", err)
		os.Exit(1)
	}
	w.oldState = oldState
	lines := []string{
		"🔧 DevSync Main Menu",
		"====================",
		"R  - Reload configuration",
		"S  - Show cache stats",
		"A  - Deploy agent",
		"Alt+1 - This menu",
		"Alt+2 - New remote session (no menu)  (TBD)",
		"Alt+3..9 - Command menus (dynamic per-config). Press one to open command picker.",
		"Alt+B - Background current session",
		"Alt+0 - Close current session",
		"> ",
	}
	for i := range lines {
		util.Default.Println(lines[i])
		util.Default.ClearLine()
	}
}

// showCommandMenuDisplay moved to view (keeps promptui usage local)
func (w *Watcher) showCommandMenuDisplay() {
	if w == nil || w.config == nil {
		util.Default.Printf("❌ watcher or config not available for command menu\n")
		return
	}
	callback := func(slotNew int) {
		// Disable raw mode to allow promptui to work
		fmt.Println("Callback: new slot is", slotNew)
		w.Slot = &slotNew
	}

	// Snapshot config for local use in this function to avoid repeated nil-checks
	cfg := w.config
	for {
		slot := w.Slot
		oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			w.safePrintln("⚠️  failed to enable raw mode:", err)
			os.Exit(1)
		}
		w.oldState = oldState
		// if w.oldState != nil {
		// 	err := term.Restore(int(os.Stdin.Fd()), w.oldState)
		// 	if err != nil {
		// 		w.safePrintln("⚠️  failed to re-enable raw mode:", err)
		// 		os.Exit(1)
		// 	}
		// 	fmt.Println("33333333333333333333pty: re-enabled raw mode for command menu")
		// }
		if *slot == 1 {
			w.displayMainMenu()
			break
		}

		if *slot == 2 {
			w.enterShellNonCommand()
			break
		}

		if w.ptyMgr != nil && w.ptyMgr.HasSlot(*slot) {
			if err := w.ptyMgr.Focus(*slot, true, callback); err != nil {
				w.safePrintf("❌ Failed to focus slot %d: %v\n", *slot, err)
			}
			continue
		} else {
			fmt.Println("1.wwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwww")
		}

		var items []string
		// If in local submenu mode, show local commands
		if w != nil && w.isLocal {
			if cfg != nil && cfg.Devsync.Script.Local.Commands != nil {
				items = append(items, cfg.Devsync.Script.Local.Commands...)
			}
			// fallback for local if none
			if len(items) == 0 {
				items = []string{"Enter Local Shell"}
			}
			items = append(items, "Exit")
		} else {
			var remoteCmds []string
			if cfg != nil && cfg.Devsync.Script.Remote.Commands != nil {
				remoteCmds = append(remoteCmds, cfg.Devsync.Script.Remote.Commands...)
			}
			// fallback defaults if none configured
			if len(remoteCmds) == 0 {
				remoteCmds = []string{}
			}
			items = make([]string, 0, len(remoteCmds)+2)
			items = append(items, remoteCmds...)
			items = append(items, "Local Console")
			items = append(items, "Exit")
		}

		// Suspend background printing while the interactive menu is active
		util.Default.Suspend()

		promptMu.Lock()
		prompt := promptui.Select{
			Label: "? Remote Console Mode",
			Items: items,
			Size:  10,
			Templates: &promptui.SelectTemplates{
				Label:    "{{ . }}",
				Active:   "▸ {{ . | cyan }}",
				Inactive: "  {{ . }}",
				Selected: "Selected: {{ . }}",
			},
			HideHelp: true,
		}
		i, result, err := prompt.Run()
		promptMu.Unlock()

		if err != nil {
			util.Default.Resume()
			util.Default.Printf("❌ Menu selection cancelled: %v\n", err)
			util.RestoreGlobal()
			return
		}

		oldState, err = term.GetState(int(os.Stdin.Fd()))
		if err != nil {
			w.safePrintln("⚠️  failed to enable raw mode:", err)
			os.Exit(1)
		}
		w.oldState = oldState

		// Handle special items when not in local submenu
		if !w.isLocal {
			if result == "Local Console" {
				// Enter local submenu mode
				w.isLocal = true
				util.Default.PrintBlock("� Switched to Local Console submenu", true)
				util.Default.Resume()
				continue
			}
		}

		// Handle Exit (works for both modes)
		if result == "Exit" {
			if w.isLocal {
				// exit local submenu back to remote
				w.isLocal = false
				util.Default.PrintBlock("↩️ Returning to remote menu...", true)
				util.Default.Resume()
				continue
			}
			util.Default.PrintBlock("", true)
			util.Default.Resume()
			util.RestoreGlobal()
			w.displayMainMenu()
			break
		}

		util.Default.Printf("Selected: %s (index %d)\n", result, i)
		util.Default.Resume()

		// If we're in local submenu, handle local command selection
		if w.isLocal {
			// persistent slot behavior if user currently on a slot
			if *slot >= 3 && *slot <= 9 && w != nil && w.ptyMgr != nil {
				localPath := cfg.Devsync.Auth.LocalPath
				if localPath == "" {
					localPath = "."
				}
				if result == "Enter Local Shell" {
					result = ""
				}
				// Choose local initial command template based on HOST OS
				// (local shell should reflect the OS running this watcher)
				isWindowsTarget := runtime.GOOS == "windows"
				var initialCmd string
				if isWindowsTarget {
					initialCmd = ""
				} else {
					initialCmd = fmt.Sprintf("cd %s && bash -c %s ; exec bash", shellEscape(localPath), shellEscape(result))
				}
				isExist := false
				if !w.ptyMgr.HasSlot(*slot) {
					util.Default.ClearLine()
					util.Default.Println("➕ Creating new local slot", *slot, "...")
					if err := w.ptyMgr.OpenLocalSlot(*slot, initialCmd); err != nil {
						util.Default.Printf("⚠️  Failed to open local slot %d: %v - returning to menu\n", *slot, err)
						util.Default.Resume()
						continue
					}
				} else {
					util.Default.Println("🔄 Reusing existing slot", *slot, "...")
					isExist = true
				}

				util.Default.Suspend()
				util.Default.PrintBlock(fmt.Sprintf("🔗 Attaching to slot %d ...", *slot), true)
				if err := w.ptyMgr.Focus(*slot, isExist, callback); err != nil {
					util.Default.Printf("⚠️  Failed to focus local slot %d: %v\n", *slot, err)
					util.Default.Resume()
				} else {
					util.Default.Resume()
				}
				continue
			}

			// ephemeral local execution for non-slot contexts
			out, err := w.executeLocalCommandWithOutput(result)
			if err != nil {
				util.Default.Printf("❌ Local command failed: %v\n", err)
			} else {
				util.Default.PrintBlock(out, true)
			}
			util.Default.Resume()
			continue
		}

		// PTY slot handling (3..9) or run once
		if *slot >= 3 && *slot <= 9 && w != nil && w.ptyMgr != nil {
			remotePath := "/tmp"
			if cfg != nil && cfg.Devsync.Auth.RemotePath != "" {
				remotePath = cfg.Devsync.Auth.RemotePath
			}
			// choose remote command template based on target OS
			targetOS := ""
			if cfg != nil {
				targetOS = strings.ToLower(cfg.Devsync.OSTarget)
			}
			isWindowsTarget := strings.Contains(targetOS, "windows")
			var initialCmd string
			if isWindowsTarget {
				// Use cmd.exe on remote Windows targets instead of PowerShell
				// escCmd := func(s string) string {
				// 	s = strings.ReplaceAll(s, "%", "%%")
				// 	s = strings.ReplaceAll(s, "^", "^^")
				// 	return s
				// }
				// cmdPart := escCmd(result)
				// // body := fmt.Sprintf("if not exist \"%s\" mkdir \"%s\" & cd /d \"%s\" & %s", remotePath, remotePath, remotePath, cmdPart)
				// escapeInner := func(s string) string { return strings.ReplaceAll(s, `"`, `\\"`) }
				initialCmd = ""
			} else {
				initialCmd = fmt.Sprintf("mkdir -p %s || true && cd %s && bash -c %s ; exec bash",
					shellEscape(remotePath), shellEscape(remotePath), shellEscape(result))
			}
			isExist := false
			if !w.ptyMgr.HasSlot(*slot) {
				util.Default.Println("➕ Creating new slot", *slot, "...")
				if err := w.ptyMgr.OpenRemoteSlot(*slot, initialCmd); err != nil {
					util.Default.Printf("⚠️  Failed to open slot %d: %v - falling back to single-run\n", *slot, err)
					continue
				}
			} else {
				fmt.Println("🔄 Reusing existing slot", *slot, "...")
				isExist = true
			}

			util.Default.Suspend()
			util.Default.PrintBlock(fmt.Sprintf("🔗 Attaching to slot %d ...", *slot), true)
			if err := w.ptyMgr.Focus(*slot, isExist, callback); err != nil {
				util.Default.Printf("⚠️  Failed to focus slot %d: %v\n", *slot, err)
				util.Default.Resume()
			} else {
				util.Default.Resume()
			}
		}
		fmt.Println("fffffffffffffffffffffffffffffffffff")
		continue
	}
}

func (w *Watcher) enterShellNonCommand() {
	util.Default.ClearLine()
	slot := 2
	if w == nil || w.ptyMgr == nil {
		util.Default.Println("❌ PTY manager not initialized")
		return
	}
	// Snapshot config and derive remote path safely
	cfg := w.config
	remotePath := "/tmp"
	if cfg != nil {
		if cfg.Devsync.Auth.RemotePath != "" {
			remotePath = cfg.Devsync.Auth.RemotePath
		}
	}
	isExist := false
	targetOS := ""
	if cfg != nil {
		targetOS = strings.ToLower(cfg.Devsync.OSTarget)
	}
	isWindowsTarget := strings.Contains(targetOS, "windows")
	var initialCmd string
	if isWindowsTarget {
		// Start an interactive cmd.exe shell in the desired directory
		body := fmt.Sprintf("if not exist \"%s\" mkdir \"%s\" & cd /d \"%s\" & cmd.exe", remotePath, remotePath, remotePath)
		escapeInner := func(s string) string { return strings.ReplaceAll(s, `"`, `\\"`) }
		initialCmd = fmt.Sprintf("cmd.exe /K \"%s\"", escapeInner(body))
	} else {
		initialCmd = fmt.Sprintf("mkdir -p %s || true && cd %s && bash -l", shellEscape(remotePath), shellEscape(remotePath))
	}
	if !w.ptyMgr.HasSlot(slot) {
		util.Default.Println("➕ Creating new remote slot", slot, "...")
		if err := w.ptyMgr.OpenRemoteSlot(slot, initialCmd); err != nil {
			util.Default.Printf("⚠️  Failed to open remote slot %d: %v - returning to menu\n", slot, err)
			util.Default.Resume()
			return
		}
	} else {
		isExist = true
	}
	fmt.Println("🔄 Reusing existing slot", slot, "...")
	util.Default.Suspend()
	util.Default.PrintBlock(fmt.Sprintf("🔗 Attaching to slot %d ...", slot), true)
	if err := w.ptyMgr.Focus(slot, isExist, func(slotNew int) {
		w.Slot = &slotNew
	}); err != nil {
		util.Default.Printf("⚠️  Failed to focus slot %d: %v\n", slot, err)
		util.Default.Resume()
	} else {
		util.Default.Resume()
	}

	if *w.Slot == 1 {
		w.displayMainMenu()
		return
	}
	if *w.Slot >= 3 {
		w.showCommandMenuDisplay()
		return
	}
	w.displayMainMenu()
}
