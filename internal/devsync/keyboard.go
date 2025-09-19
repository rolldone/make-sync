package devsync

// ...existing code...
import (
	"os"
	"strings"

	"make-sync/internal/util"
)

// handleKeyboardInput moved here to keep view/keyboard code together.
func (w *Watcher) handleKeyboardInput() {
	buffer := make([]byte, 10) // Increase buffer for escape sequences
	util.Default.Printf("DEBUG keyboard handler start pid=%d\n", os.Getpid())
	defer util.Default.Printf("DEBUG keyboard handler exit pid=%d\n", os.Getpid())
	var rawEnabled bool

	// Try to enable raw mode using util helper so we can capture single keypresses
	// Do not enable if TUI currently owns the terminal.
	if !util.TUIActive {
		if _, err := util.EnableRawGlobalAuto(); err == nil {
			rawEnabled = true
		} else {
			w.safePrintln("⚠️  keyboard handler: failed to enable raw mode:", err)
		}
	}

	// Ensure terminal state is restored when this handler returns
	restore := func() {
		_ = util.RestoreGlobal()
	}
	defer restore()

	for {
		select {
		case <-w.keyboardStop:
			// Stop keyboard input during session, wait for restart
			// Before pausing for session, restore terminal state so session can set raw mode
			_ = util.RestoreGlobal()
			<-w.keyboardRestart
			// Re-enable raw mode after session if possible
			if !rawEnabled {
				if _, err := util.EnableRawGlobalAuto(); err == nil {
					rawEnabled = true
				} else {
					w.safePrintln("⚠️  keyboard handler: failed to re-enable raw mode:", err)
				}
			}
			continue
		default:
			n, err := os.Stdin.Read(buffer)
			if err != nil {
				// On read error, restore terminal and exit handler
				_ = util.RestoreGlobal()
				return
			}
			if n > 0 {
				raw := buffer[:n]
				input := string(raw)
				// Handle escape sequences for Alt + key (don't trim spaces)
				if strings.HasPrefix(input, "\x1b") {
					// Try to create a two-byte sequence if available (ESC + key)
					if n >= 2 {
						seq := string([]byte{raw[0], raw[1]})
						_ = util.RestoreGlobal()
						w.handleAltKey(seq)
						// Re-enable raw mode after handler returns if possible
						if !rawEnabled && !util.TUIActive {
							if _, err := util.EnableRawGlobalAuto(); err == nil {
								rawEnabled = true
							}
						}
						continue
					}

					// Fallback: pass full input string to handler
					_ = util.RestoreGlobal()
					w.handleAltKey(input)
					if !rawEnabled && !util.TUIActive {
						if _, err := util.EnableRawGlobalAuto(); err == nil {
							rawEnabled = true
						}
					}
					continue
				}

				// Check for Ctrl+R (0x12) raw byte
				if buffer[0] == 0x12 {
					// ensure terminal restored before stopping notify so UI is sane
					_ = util.RestoreGlobal()
					w.StopNotify()
					return
				}
				// Check for Ctrl+C (0x03) - force stop and exit
				if buffer[0] == 0x03 {
					_ = util.RestoreGlobal()
					os.Exit(0)
					return
				}
				// Trim spaces for regular keys
				input = strings.TrimSpace(input)
				// Check for regular keys
				switch input {
				case "R", "r":
					_ = util.RestoreGlobal()
					w.HandleReloadCommand()
					if !rawEnabled && !util.TUIActive {
						if _, err := util.EnableRawGlobalAuto(); err == nil {
							rawEnabled = true
						}
					}
				case "S", "s":
					_ = util.RestoreGlobal()
					w.HandleShowStatsCommand()
					if !rawEnabled && !util.TUIActive {
						if _, err := util.EnableRawGlobalAuto(); err == nil {
							rawEnabled = true
						}
					}
				case "A", "a":
					_ = util.RestoreGlobal()
					w.HandleDeployAgentCommand()
					if !rawEnabled && !util.TUIActive {
						if _, err := util.EnableRawGlobalAuto(); err == nil {
							rawEnabled = true
						}
					}
				}
			}
		}
	}
}

// handleAltKey handles Alt + key combinations (moved to view)
func (w *Watcher) handleAltKey(input string) {

	// Clear screen and display menu in one atomic block
	w.printer.PrintBlock("\033[2J\033[1;1H", false)

	switch input {
	case "\x1br", "\x1br\n", "\x1bR", "\x1bR\n": // Alt + R (reload)
		w.HandleReloadCommand()
	case "\x1b3", "\x1b4", "\x1b5", "\x1b6", "\x1b7", "\x1b8", "\x1b9": // Alt + 3-9
		// Ensure terminal is in normal (cooked) mode before launching promptui
		// so the interactive selector can read arrow keys and render properly.
		w.printer.PrintBlock("", true) // ensure any status line cleared

		// Detect slot number (ESC + digit). Keep current behavior (show menu)
		// for now and reserve slot-handling for PTY manager integration.
		var slot int
		if strings.HasPrefix(input, "\x1b") && len(input) >= 2 {
			switch input[1] {
			case '3':
				slot = 3
			case '4':
				slot = 4
			case '5':
				slot = 5
			case '6':
				slot = 6
			case '7':
				slot = 7
			case '8':
				slot = 8
			case '9':
				slot = 9
			}
		}

		_ = slot // placeholder for future PTY manager behavior
		w.Slot = &slot
		w.showCommandMenuDisplay()
	default:
	}
}
