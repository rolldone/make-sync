package devsync

import (
	"fmt"

	"make-sync/internal/util"

	"github.com/manifoldco/promptui"
)

// displayMainMenu moved to view so watcher UI code is grouped
func (w *Watcher) displayMainMenu() {
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
		w.printer.Println(lines[i])
		w.printer.ClearLine()
	}
}

// showCommandMenuDisplay moved to view (keeps promptui usage local)
func (w *Watcher) showCommandMenuDisplay() {
	callback := func(slotNew int) {
		// w.ptyMgr.PauseSlot(*w.Slot)
		w.Slot = &slotNew
		w.ptyMgr.Pendingchan <- "pause"
	}
	for {
		slot := w.Slot

		if *slot == 1 {
			w.displayMainMenu()
			break
		}

		if w.ptyMgr != nil && w.ptyMgr.HasSlot(*slot) {
			if err := w.ptyMgr.Focus(*slot, true, callback); err != nil {
				w.safePrintf("❌ Failed to focus slot %d: %v\n", *slot, err)
			}
			continue
		}

		var remoteCmds []string
		if w != nil && w.config != nil && w.config.Devsync.Script.Remote.Commands != nil {
			remoteCmds = append(remoteCmds, w.config.Devsync.Script.Remote.Commands...)
		}

		// fallback defaults if none configured
		if len(remoteCmds) == 0 {
			remoteCmds = []string{
				"docker-compose up",
				"docker-compose down && docker-compose up --build",
				"docker-compose down && docker-compose up",
				"tail -f storage/log/*.log >>> my.log",
				"docker-compose exec app bash -l",
			}
		}

		items := make([]string, 0, len(remoteCmds)+2)
		items = append(items, remoteCmds...)
		items = append(items, "Local Console")
		items = append(items, "Exit")

		// Suspend background printing while the interactive menu is active
		if w != nil && w.printer != nil {
			w.printer.Suspend()
		} else {
			fmt.Print("\x1b[2J\x1b[1;1H")
		}

		// Ensure terminal is in cooked mode for promptui
		_ = util.RestoreGlobal()

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
		if err != nil {
			if w != nil && w.printer != nil {
				w.printer.Resume()
				w.printer.Printf("❌ Menu selection cancelled: %v\n", err)
			} else {
				fmt.Printf("❌ Menu selection cancelled: %v\n", err)
			}
			// re-enable raw mode for keyboard loop and return
			_, _ = util.EnableRawGlobalAuto()
			return
		}

		// re-enable raw mode after prompt so keyboard loop can continue
		_, _ = util.EnableRawGlobalAuto()

		// Handle special items: Exit should return to watcher main menu
		if result == "Exit" {
			if w != nil && w.printer != nil {
				w.printer.PrintBlock("", true)
				w.printer.Resume()
				w.displayMainMenu()
			} else {
				fmt.Print("\x1b[2J\x1b[1;1H")
				w.displayMainMenu()
			}
			return
		}

		if w != nil && w.printer != nil {
			w.printer.Printf("Selected: %s (index %d)\n", result, i)
		} else {
			fmt.Printf("Selected: %s (index %d)\n", result, i)
		}

		if w != nil && w.printer != nil {
			w.printer.Resume()
		}

		// PTY slot handling (3..9) or run once
		if *slot >= 3 && *slot <= 9 && w != nil && w.ptyMgr != nil {
			remotePath := w.config.Devsync.Auth.RemotePath
			if remotePath == "" {
				remotePath = "/tmp"
			}
			initialCmd := fmt.Sprintf("mkdir -p %s || true && cd %s && bash -c %s ; exec bash",
				shellEscape(remotePath), shellEscape(remotePath), shellEscape(result))
			isExist := false
			if !w.ptyMgr.HasSlot(*slot) {
				fmt.Println("➕ Creating new slot", *slot, "...")
				if err := w.ptyMgr.OpenRemoteSlot(*slot, initialCmd); err != nil {
					if w.printer != nil {
						w.printer.Printf("⚠️  Failed to open slot %d: %v - falling back to single-run\n", *slot, err)
					} else {
						fmt.Printf("⚠️  Failed to open slot %d: %v - falling back to single-run\n", *slot, err)
					}
					w.runRemoteCommand(result)
					continue
				}
			} else {
				fmt.Println("🔄 Reusing existing slot", *slot, "...")
				isExist = true
			}

			if w.printer != nil {
				w.printer.Suspend()
			}
			fmt.Println("🔗 Attaching to slot", *slot, "...")
			if err := w.ptyMgr.Focus(*slot, isExist, callback); err != nil {
				if w.printer != nil {
					w.printer.Printf("⚠️  Failed to focus slot %d: %v\n", *slot, err)
					w.printer.Resume()
				} else {
					fmt.Printf("⚠️  Failed to focus slot %d: %v\n", *slot, err)
				}
			} else {
				if w.printer != nil {
					w.printer.Resume()
				}
			}
		}

		continue
	}
}
