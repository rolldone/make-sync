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
		util.Default.Println(lines[i])
		util.Default.ClearLine()
	}
}

// showCommandMenuDisplay moved to view (keeps promptui usage local)
func (w *Watcher) showCommandMenuDisplay() {
	callback := func(slotNew int) {
		// Disable raw mode to allow promptui to work
		util.RestoreGlobal()
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
		util.Default.Suspend()

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
			util.Default.Resume()
			util.Default.Printf("❌ Menu selection cancelled: %v\n", err)
			// re-enable raw mode for keyboard loop and return
			_, _ = util.EnableRawGlobalAuto()
			return
		}

		// re-enable raw mode after prompt so keyboard loop can continue
		_, _ = util.EnableRawGlobalAuto()

		// Handle special items: Exit should return to watcher main menu
		if result == "Exit" {
			util.Default.PrintBlock("", true)
			util.Default.Resume()
			util.RestoreGlobal()
			w.displayMainMenu()
			return
		}

		if result == "Local Console" {
			util.Default.PrintBlock("🔗 Attaching to local console ...", true)
			util.Default.Resume()
			w.showLocalCommandMenuDisplay()
			continue
		}

		util.Default.Printf("Selected: %s (index %d)\n", result, i)
		util.Default.Resume()

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
				util.Default.Println("➕ Creating new slot", *slot, "...")
				if err := w.ptyMgr.OpenRemoteSlot(*slot, initialCmd); err != nil {
					util.Default.Printf("⚠️  Failed to open slot %d: %v - falling back to single-run\n", *slot, err)
					w.runRemoteCommand(result)
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

		continue
	}
}

func (w *Watcher) showLocalCommandMenuDisplay() {
	for {
		var localCmds []string
		if w != nil && w.config != nil && w.config.Devsync.Script.Local.Commands != nil {
			localCmds = append(localCmds, w.config.Devsync.Script.Local.Commands...)
		}

		// fallback defaults if none configured
		if len(localCmds) == 0 {
			localCmds = []string{
				"bash -l",
				"tail -f storage/log/*.log >>> my.log",
				"echo \"Hello from local\"",
			}
		}

		items := make([]string, 0, len(localCmds)+1)
		items = append(items, localCmds...)
		items = append(items, "Exit")

		// Suspend background printing while the interactive menu is active
		util.Default.Suspend()

		// Ensure terminal is in cooked mode for promptui
		_ = util.RestoreGlobal()

		prompt := promptui.Select{
			Label: "? Local Console Mode",
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
			util.Default.Resume()
			util.Default.Printf("❌ Menu selection cancelled: %v\n", err)
			// re-enable raw mode for keyboard loop and return
			_, _ = util.EnableRawGlobalAuto()
			return
		}

		// re-enable raw mode after prompt so keyboard loop can continue
		_, _ = util.EnableRawGlobalAuto()

		// Handle Exit
		if result == "Exit" {
			util.Default.PrintBlock("", true)
			util.Default.Resume()
			util.RestoreGlobal()
			return
		}

		util.Default.Printf("Selected: %s (index %d)\n", result, i)
		util.Default.Resume()

		// NOTE: eksekusi perintah lokal belum di-run di sini.
		// Jika mau, kita bisa panggil fungsi eksekusi (mis. w.runLocalCommand(result))
		// atau membuat helper executeLocalCommandWithOutput.
		continue
	}
}
