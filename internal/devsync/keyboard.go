package devsync

import (
	"os"
	"strings"

	"make-sync/internal/util"

	"golang.org/x/term"
)

// handleKeyboardInput captures single-key shortcuts when the TUI is not active.
// It uses the global raw helpers to enable raw mode on stdin, but respects
// util.TUIActive so it doesn't interfere when the Bubble Tea TUI owns the
// terminal.
func (w *Watcher) handleKeyboardInput() {
	buffer := make([]byte, 16) // buffer for possible escape sequences

	for {
		select {
		case <-w.keyboardStop:
			select {
			case w.keyboardStopped <- struct{}{}:
			default:
			}
			<-w.keyboardRestart
			continue
		default:
			n, err := os.Stdin.Read(buffer)
			if err != nil {
				// On read error, restore terminal and exit handler
				return
			}
			if n == 0 {
				continue
			}

			// Quick checks for control bytes
			b0 := buffer[0]
			// Ctrl+R (0x12) - trigger notify reload
			if b0 == 0x12 {
				w.StopNotify()
				return
			}
			// Ctrl+C (0x03) - exit immediately
			if b0 == 0x03 {
				term.Restore(int(os.Stdin.Fd()), w.firstOld)
				os.Exit(0)
				return
			}

			// If this is an escape sequence (Alt+key), forward to alt handler.
			// Do not trim spaces for alt handling.
			inputRaw := string(buffer[:n])
			if strings.HasPrefix(inputRaw, "\x1b") {
				// Restore terminal before running interactive handlers
				w.handleAltKey(inputRaw)
				continue
			}

			// Regular single-key handlers: trim and compare
			input := strings.TrimSpace(inputRaw)
			switch input {
			case "R", "r":
				// _ = util.RestoreGlobal()
				w.HandleReloadCommand()
			case "S", "s":
				// _ = util.RestoreGlobal()
				// w.HandleShowStatsCommand()
				// if !util.TUIActive {
				// 	if _, err := util.EnableRawGlobalAuto(); err != nil {
				// 		w.safePrintln("⚠️  keyboard handler: failed to re-enable raw mode:", err)
				// 	}
				// }
			case "A", "a":
				w.HandleDeployAgentCommand()
			default:
				// unhandled
				// Ignore arrow-key fragments and other stray control bytes that may arrive
				// as separate reads (e.g. "[" or "A"/"B"/"C"/"D") to avoid blanking the screen.
				if input == "" || input == "[" || input == "O" ||
					input == "A" || input == "B" || input == "C" || input == "D" {
					continue
				}
				// unhandled
				util.Default.ClearScreen()
				w.displayMainMenu()
			}
		}
	}
}

// handleAltKey handles escape-prefixed inputs such as Alt+Key or ESC+digit.
func (w *Watcher) handleAltKey(input string) {
	// Clear screen and redraw status line as necessary
	util.Default.PrintBlock("\033[2J\033[1;1H", false)

	// Ignore arrow keys and common CSI/SS3 sequences so they don't clear the screen.
	// Examples: ESC [ A  (cursor up), ESC O A (keypad up)
	if input == "\x1b[A" || input == "\x1b[B" || input == "\x1b[C" || input == "\x1b[D" ||
		input == "\x1bOA" || input == "\x1bOB" || input == "\x1bOC" || input == "\x1bOD" {
		w.displayMainMenu()
		return
	}

	switch input {
	case "\x1br", "\x1br\n", "\x1bR", "\x1bR\n": // Alt + R (reload)
		w.HandleReloadCommand()
		return
	case "\x1b1", "\x1b2", "\x1b3", "\x1b4", "\x1b5", "\x1b6", "\x1b7", "\x1b8", "\x1b9":
		// Alt + 2-9: map to slot numbers and show the command menu
		util.Default.PrintBlock("", true) // ensure any status line cleared
		var slot int
		if strings.HasPrefix(input, "\x1b") && len(input) >= 1 {
			switch input[1] {
			case '1':
				slot = 1
			case '2':
				slot = 2
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
		if slot == 2 {
			w.enterShellNonCommand()
			return
		}
		if slot == 1 {
			w.displayMainMenu()
			return
		}
		w.showCommandMenuDisplay()
		return
	default:
		// no-op for other escape sequences
	}
}
