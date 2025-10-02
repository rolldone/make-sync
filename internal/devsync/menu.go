package devsync

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"make-sync/internal/config"
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
		util.Default.ClearLine()
		fmt.Println("Callback: new slot is", slotNew)
		w.Slot = &slotNew
	}

	// Snapshot config for local use in this function to avoid repeated nil-checks
	cfg := w.config
	for {
		slot := w.Slot
		// oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
		// if err != nil {
		// 	w.safePrintln("⚠️  failed to enable raw mode:", err)
		// 	os.Exit(1)
		// }
		// w.oldState = oldState

		if *slot == 1 {
			w.displayMainMenu()
			break
		}

		if *slot == 2 {
			w.enterShellNonCommand()
			break
		}

		if w.ptyMgr != nil && w.ptyMgr.HasSlot(*slot) {
			log.Println("DEBUG: Reusing existing slot", *slot)
			if err := w.ptyMgr.Focus(*slot, true, callback); err != nil {
				w.safePrintf("❌ Failed to focus slot %d: %v\n", *slot, err)
			}
			continue
		} else {
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
		sendCtrlArrowDown()
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
			util.Default.ClearLine()
			return
		}

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
				// Support optional local log capture marker: "command >>> file"
				baseCmd, logFile := parseCommandLogSpec(result)
				// Choose local initial command template based on HOST OS
				// (local shell should reflect the OS running this watcher)
				isWindowsTarget := runtime.GOOS == "windows"
				var initialCmd string
				if isWindowsTarget {
					// On Windows host: if baseCmd provided, execute it and keep shell open; else open shell only
					if strings.TrimSpace(baseCmd) == "" {
						initialCmd = ""
					} else {
						// Escape percent and caret to survive cmd parsing
						esc := func(s string) string {
							s = strings.ReplaceAll(s, "%", "%%")
							s = strings.ReplaceAll(s, "^", "^^")
							return s
						}
						cmdPart := esc(baseCmd)
						initialCmd = fmt.Sprintf("cmd.exe /K cd /d \"%s\" & %s", localPath, cmdPart)
					}
				} else {
					initialCmd = fmt.Sprintf("cd %s && bash -c %s ; exec bash", shellEscape(localPath), shellEscape(baseCmd))
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
					// If logging requested, attach an output tap to this slot
					if logFile != "" {
						tap := makeFileLogTap(cfg, logFile)
						if tap != nil {
							_ = w.ptyMgr.SetOutputTapForSlot(*slot, tap)
						}
					}
				} else {
					isExist = true
					// Update/attach logging tap on existing local slot as well (best-effort)
					if logFile != "" {
						tap := makeFileLogTap(cfg, logFile)
						if tap != nil {
							_ = w.ptyMgr.SetOutputTapForSlot(*slot, tap)
						}
					}
				}

				util.Default.Suspend()
				util.Default.PrintBlock(fmt.Sprintf("🔗 Attaching to slot %d ...", *slot), true)
				log.Println("DEBUG: attaching to local slot", *slot, "isExist=", isExist)
				if err := w.ptyMgr.Focus(*slot, isExist, callback); err != nil {
					util.Default.Printf("⚠️  Failed to focus local slot %d: %v\n", *slot, err)
					util.Default.Resume()
				} else {
					util.Default.Resume()
				}
				// sendKeyA()
				continue
			}

			// ephemeral local execution for non-slot contexts
			out, err := w.executeLocalCommandWithOutput(result)
			if err != nil {
				util.Default.Printf("❌ Local command failed: %v\n", err)
			} else {
				util.Default.PrintBlock(out, true)
			}
			// sendKeyA()
			util.Default.Resume()
			continue
		}

		// PTY slot handling (3..9) or run once
		if *slot >= 3 && *slot <= 9 && w != nil && w.ptyMgr != nil {
			remotePath := "/tmp"
			if cfg != nil && cfg.Devsync.Auth.RemotePath != "" {
				remotePath = cfg.Devsync.Auth.RemotePath
			}
			// Parse optional local log capture marker in the selected command
			baseCmd, logFile := parseCommandLogSpec(result)
			// choose remote command template based on target OS
			targetOS := ""
			if cfg != nil {
				targetOS = strings.ToLower(cfg.Devsync.OSTarget)
			}
			isWindowsTarget := strings.Contains(targetOS, "windows")
			var initialCmd string
			if isWindowsTarget {
				// Normalize remotePath for Windows: convert '/c/Users' -> 'C:\Users' and
				// replace forward slashes with backslashes so cmd.exe accepts it.
				winPath := remotePath
				if strings.HasPrefix(winPath, "/") && len(winPath) > 2 && winPath[2] == '/' {
					// pattern like /c/Users -> drive letter at pos 1
					d := strings.ToUpper(string(winPath[1]))
					rest := winPath[2:]
					rest = strings.ReplaceAll(rest, "/", "\\\\")
					winPath = d + ":" + rest
				} else {
					winPath = strings.ReplaceAll(winPath, "/", "\\\\")
				}

				// Use cmd.exe on remote Windows targets. If the selected result came
				// from the user's config (cfg.Devsync.Script.Remote.Commands), do NOT
				// apply any automatic mapping or escaping — respect the exact command
				// provided by the configuration. Only apply mapping for dynamic or
				// fallback menu entries.
				inConfig := false
				if cfg != nil && cfg.Devsync.Script.Remote.Commands != nil {
					for _, c := range cfg.Devsync.Script.Remote.Commands {
						if c == result {
							inConfig = true
							break
						}
					}
				}
				// Escape percent signs and carets so the command survives cmd parsing.
				escCmd := func(s string) string {
					s = strings.ReplaceAll(s, "%", "%%")
					s = strings.ReplaceAll(s, "^", "^^")
					return s
				}
				// Only apply escaping for non-config items; do not rewrite or map
				// commands like `pwd`/`ls` to Windows equivalents — respect user
				// config and dynamic entries verbatim except for escaping percent
				// and caret characters which would be consumed by cmd.exe parsing.
				cmdPart := baseCmd
				if !inConfig {
					cmdPart = escCmd(baseCmd)
				}
				// Run the user's command; assume the directory already exists and
				// just change directory into it before running the command.
				// body := fmt.Sprintf("cd /d \"%s\" & %s", winPath, cmdPart)
				body := fmt.Sprintf("cd /d \"%s\" & %s", winPath, cmdPart)
				// Do not add extra outer quoting or backslash-escaped quotes here;
				// pass the body (which already contains quoted paths) directly to cmd.exe
				initialCmd = fmt.Sprintf("cmd.exe /K %s", body)
			} else {
				initialCmd = fmt.Sprintf("mkdir -p %s || true && cd %s && bash -c %s ; exec bash",
					shellEscape(remotePath), shellEscape(remotePath), shellEscape(baseCmd))
			}
			isExist := false
			if !w.ptyMgr.HasSlot(*slot) {
				util.Default.Println("➕ Creating new slot", *slot, "...")
				// Debug: print before opening remote slot to inspect values seen at runtime
				log.Printf("DEBUG: targetOS=%q remotePath=%q initialCmd=%q\n", targetOS, remotePath, initialCmd)
				if err := w.ptyMgr.OpenRemoteSlot(*slot, initialCmd); err != nil {
					util.Default.Printf("⚠️  Failed to open slot %d: %v - falling back to single-run\n", *slot, err)
					continue
				}
				// If logging requested, attach an output tap to this slot
				if logFile != "" {
					tap := makeFileLogTap(cfg, logFile)
					if tap != nil {
						_ = w.ptyMgr.SetOutputTapForSlot(*slot, tap)
					}
				}
			} else {
				isExist = true
				// Update/attach logging tap on existing slot as well (best-effort)
				if logFile != "" {
					tap := makeFileLogTap(cfg, logFile)
					if tap != nil {
						_ = w.ptyMgr.SetOutputTapForSlot(*slot, tap)
					}
				}
			}

			util.Default.Suspend()
			log.Println("DEBUG: attaching to slot", *slot, "isExist=", isExist)
			util.Default.PrintBlock(fmt.Sprintf("🔗 Attaching to slot %d ...", *slot), true)
			if err := w.ptyMgr.Focus(*slot, isExist, callback); err != nil {
				util.Default.Printf("⚠️  Failed to focus slot %d: %v\n", *slot, err)
				util.Default.Resume()
			} else {
				util.Default.Resume()
			}
		}
	}
}

// parseCommandLogSpec detects a trailing ">>> filename" marker and returns the base command and sanitized filename.
func parseCommandLogSpec(cmd string) (string, string) {
	s := strings.TrimSpace(cmd)
	idx := strings.LastIndex(s, ">>>")
	if idx == -1 {
		return s, ""
	}
	left := strings.TrimSpace(s[:idx])
	right := strings.TrimSpace(s[idx+3:])
	if left == "" || right == "" {
		return s, ""
	}
	// strip optional quotes
	if (strings.HasPrefix(right, "\"") && strings.HasSuffix(right, "\"")) || (strings.HasPrefix(right, "'") && strings.HasSuffix(right, "'")) {
		right = strings.Trim(right, "'\"")
	}
	// sanitize filename: basename only, whitelist chars
	right = filepath.Base(right)
	// replace spaces with underscore
	right = strings.ReplaceAll(right, " ", "_")
	// whitelist filter
	cleaned := make([]rune, 0, len(right))
	for _, r := range right {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			cleaned = append(cleaned, r)
		}
	}
	fname := string(cleaned)
	if fname == "" {
		return left, ""
	}
	return left, fname
}

var logFileLocks sync.Map // map[string]*sync.Mutex

// makeFileLogTap builds an output tap that appends bytes to .sync_temp/<file> in the local workspace.
func makeFileLogTap(cfg *config.Config, file string) func([]byte, bool) {
	// Determine local root
	root := "."
	if cfg != nil && cfg.Devsync.Auth.LocalPath != "" {
		root = cfg.Devsync.Auth.LocalPath
	}
	syncTemp := filepath.Join(root, ".sync_temp")
	if err := os.MkdirAll(syncTemp, 0755); err != nil {
		util.Default.Printf("⚠️  Failed to ensure .sync_temp: %v\n", err)
		return nil
	}
	path := filepath.Join(syncTemp, file)
	// Acquire per-file lock
	var muIface any
	if v, ok := logFileLocks.Load(path); ok {
		muIface = v
	} else {
		nm := &sync.Mutex{}
		muIface, _ = logFileLocks.LoadOrStore(path, nm)
	}
	mu := muIface.(*sync.Mutex)

	// Write a header once per tap creation
	writeHeader := func(f *os.File) {
		ts := time.Now().Format(time.RFC3339)
		_ = appendLine(f, []byte("\n===== session start "+ts+" =====\n"))
	}

	// Open (append) and write header
	func() {
		mu.Lock()
		defer mu.Unlock()
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err == nil {
			writeHeader(f)
			_ = f.Close()
		}
	}()

	// Use a stateful ANSI stripper so sequences crossing chunk boundaries are handled.
	stripper := util.NewANSIStripper()

	return func(b []byte, isErr bool) {
		mu.Lock()
		defer mu.Unlock()
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return
		}
		// Optionally tag stderr
		if isErr {
			_ = appendLine(f, []byte("[ERR] "))
		}
		clean := stripper.Strip(b)
		_, _ = f.Write(clean)
		_ = f.Close()
	}
}

func appendLine(f *os.File, b []byte) error {
	_, err := f.Write(b)
	return err
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
		// Normalize remotePath for Windows and build cmd.exe body
		winPath := remotePath
		if strings.HasPrefix(winPath, "/") && len(winPath) > 2 && winPath[2] == '/' {
			d := strings.ToUpper(string(winPath[1]))
			rest := winPath[2:]
			rest = strings.ReplaceAll(rest, "/", "\\\\")
			winPath = d + ":" + rest
		} else {
			winPath = strings.ReplaceAll(winPath, "/", "\\\\")
		}
		// Start an interactive cmd.exe shell in the desired directory
		body := fmt.Sprintf("cd /d \"%s\"", winPath)
		// Pass body directly to cmd.exe (body contains quoted paths)
		initialCmd = fmt.Sprintf("cmd.exe /K %s", body)
	} else {
		initialCmd = fmt.Sprintf("mkdir -p %s || true && cd %s && exec bash", shellEscape(remotePath), shellEscape(remotePath))
	}
	if !w.ptyMgr.HasSlot(slot) {
		util.Default.Println("➕ Creating new remote slot", slot, "...")
		util.Default.ClearLine()
		// Debug: print values before opening remote slot (slot 2)
		util.Default.Printf("DEBUG: enterShellNonCommand targetOS=%q remotePath=%q initialCmd=%q\n", targetOS, remotePath, initialCmd)
		if err := w.ptyMgr.OpenRemoteSlot(slot, initialCmd); err != nil {
			util.Default.Printf("⚠️  Failed to open remote slot %d: %v - returning to menu\n", slot, err)
			util.Default.Resume()
			return
		}
	} else {
		isExist = true
	}
	util.Default.ClearLine()
	util.Default.Println("🔄 Reusing existing slot for non command ", slot, "...")
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
