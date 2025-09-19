package devsync

import (
	"os"
	"strings"

	"make-sync/internal/util"
)

// handleKeyboardInput captures single-key shortcuts when the TUI is not active.
// It uses the global raw helpers to enable raw mode on stdin, but respects
// util.TUIActive so it doesn't interfere when the Bubble Tea TUI owns the
// terminal.
func (w *Watcher) handleKeyboardInput() {
	buffer := make([]byte, 16) // buffer for possible escape sequences

	// Try to enable raw mode using util helper so we can capture single keypresses
	// Do not enable if TUI currently owns the terminal.
	if !util.TUIActive {
		if _, err := util.EnableRawGlobalAuto(); err != nil {
			w.safePrintln("⚠️  keyboard handler: failed to enable raw mode:", err)
		}
	}

	// Ensure terminal state is restored when this handler returns
	defer func() {
		_ = util.RestoreGlobal()
	}()

	for {
		select {
		case <-w.keyboardStop:
			// Stop keyboard input during interactive session. Restore terminal so
			// the session can enable raw mode itself. Wait for restart signal.
			_ = util.RestoreGlobal()
			// acknowledge that keyboard input is paused so TUI can take over stdin
			select {
			case w.keyboardStopped <- struct{}{}:
			default:
			}
			<-w.keyboardRestart
			// Re-enable raw mode after session if possible
			if !util.TUIActive {
				if _, err := util.EnableRawGlobalAuto(); err != nil {
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
			if n == 0 {
				continue
			}

			// Quick checks for control bytes
			b0 := buffer[0]
			// Ctrl+R (0x12) - trigger notify reload
			if b0 == 0x12 {
				_ = util.RestoreGlobal()
				w.StopNotify()
				return
			}
			// Ctrl+C (0x03) - exit immediately
			if b0 == 0x03 {
				_ = util.RestoreGlobal()
				os.Exit(0)
				return
			}

			// If this is an escape sequence (Alt+key), forward to alt handler.
			// Do not trim spaces for alt handling.
			inputRaw := string(buffer[:n])
			if strings.HasPrefix(inputRaw, "\x1b") {
				// Restore terminal before running interactive handlers
				_ = util.RestoreGlobal()
				w.handleAltKey(inputRaw)
				// Re-enable raw mode after handler returns if the TUI isn't active
				if !util.TUIActive {
					if _, err := util.EnableRawGlobalAuto(); err != nil {
						w.safePrintln("⚠️  keyboard handler: failed to re-enable raw mode:", err)
					}
				}
				continue
			}

			// Regular single-key handlers: trim and compare
			input := strings.TrimSpace(inputRaw)
			switch input {
			case "R", "r":
				// _ = util.RestoreGlobal()
				w.HandleReloadCommand()
				if !util.TUIActive {
					if _, err := util.EnableRawGlobalAuto(); err != nil {
						w.safePrintln("⚠️  keyboard handler: failed to re-enable raw mode:", err)
					}
				}
			case "S", "s":
				_ = util.RestoreGlobal()
				w.HandleShowStatsCommand()
				if !util.TUIActive {
					if _, err := util.EnableRawGlobalAuto(); err != nil {
						w.safePrintln("⚠️  keyboard handler: failed to re-enable raw mode:", err)
					}
				}
			case "A", "a":
				_ = util.RestoreGlobal()
				w.HandleDeployAgentCommand()
				if !util.TUIActive {
					if _, err := util.EnableRawGlobalAuto(); err != nil {
						w.safePrintln("⚠️  keyboard handler: failed to re-enable raw mode:", err)
					}
				}
			default:
				// unhandled
			}
		}
	}
}

// handleAltKey handles escape-prefixed inputs such as Alt+Key or ESC+digit.
func (w *Watcher) handleAltKey(input string) {
	// Clear screen and redraw status line as necessary
	util.Default.PrintBlock("\033[2J\033[1;1H", false)

	switch input {
	case "\x1br", "\x1br\n", "\x1bR", "\x1bR\n": // Alt + R (reload)
		w.HandleReloadCommand()
		return
	case "\x1b3", "\x1b4", "\x1b5", "\x1b6", "\x1b7", "\x1b8", "\x1b9":
		// Alt + 3-9: map to slot numbers and show the command menu
		util.Default.PrintBlock("", true) // ensure any status line cleared
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
		w.Slot = &slot
		w.showCommandMenuDisplay()
		return
	default:
		// no-op for other escape sequences
	}
}
