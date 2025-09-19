package devsync

import (
	"fmt"
	"make-sync/internal/devsync/sshclient"
	"os"
	"sync"
	"time"
)

// PTYSession represents one persistent PTY session (remote via SSH bridge)
type PTYSession struct {
	Slot    int
	Cmd     string
	Bridge  *sshclient.PTYSSHBridge
	Running bool
	mu      sync.Mutex
	created time.Time
}

// PTYManager manages multiple persistent PTY sessions (slots 3..9)
type PTYManager struct {
	w           *Watcher
	sessions    map[int]*PTYSession
	mu          sync.Mutex
	Pendingchan chan string // channel to send pause/unpause/exit commands
}

// NewPTYManager creates a manager bound to a watcher instance
func NewPTYManager(w *Watcher) *PTYManager {
	return &PTYManager{
		w:        w,
		sessions: make(map[int]*PTYSession),
	}
}

// OpenRemoteSlot creates (but does not attach) a remote PTY session in the given slot.
func (m *PTYManager) OpenRemoteSlot(slot int, remoteCmd string) error {
	if slot < 3 || slot > 9 {
		return fmt.Errorf("slot must be 3..9")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.sessions[slot]; ok && s != nil && s.Running {
		return fmt.Errorf("slot %d already has active session", slot)
	}

	if m.w == nil || m.w.sshClient == nil {
		return fmt.Errorf("no SSH client available")
	}

	bridge, err := sshclient.NewPTYSSHBridgeWithCommand(m.w.sshClient, remoteCmd)
	if err != nil {
		return fmt.Errorf("failed to create bridge: %w", err)
	}

	s := &PTYSession{
		Slot:    slot,
		Cmd:     remoteCmd,
		Bridge:  bridge,
		Running: false,
		created: time.Now(),
	}
	m.sessions[slot] = s
	return nil
}

// Focus attaches current terminal to the given slot's PTY and starts interactive session.
// This call will block until the interactive session exits. Caller must ensure keyboard
// handler isn't concurrently reading (watcher should restore terminal before calling Focus).
func (m *PTYManager) Focus(slot int, isExist bool, callback func(slotNew int)) error {

	m.mu.Lock()
	s, ok := m.sessions[slot]
	m.mu.Unlock()
	if !ok || s == nil {
		return fmt.Errorf("no session in slot %d", slot)
	}

	// <-- CHANGED: only lock briefly to set Running; do NOT hold s.mu across the message loop
	s.mu.Lock()
	s.Running = true
	s.mu.Unlock()

	if m.w != nil && m.w.printer != nil {
		m.w.printer.Resume()
	}

	// Provide a callback which will be invoked when the bridge stdin matcher
	// detects the configured sequence (default Ctrl+G). The callback will
	// publish an event to request showing the menu in the watcher if available.
	// Build a callback that handles Ctrl+G (0x07) and Alt+1..9 (ESC + digit)
	cb := func(gg []byte) {
		// scan for patterns inside gg
		for i := 0; i < len(gg); i++ {
			b := gg[i]
			if b == 0x07 { // Ctrl+G
				// write a debug marker to /tmp so the user can verify the callback ran
				go func() {
					fname := fmt.Sprintf("/tmp/make-sync-callback-fired-slot-%d.log", slot)
					f, err := os.OpenFile(fname, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
					if err == nil {
						defer f.Close()
						f.WriteString(time.Now().Format(time.RFC3339) + " callback fired (Ctrl+G)\n")
					}
				}()
				if m != nil && m.w != nil && m.w.eventBus != nil {
					go func() { m.w.eventBus.Publish("devsync.menu.show") }()
				}
			}
			if b == 0x1b && i+1 < len(gg) { // ESC
				d := gg[i+1]
				if d >= '1' && d <= '9' {
					slotNum := int(d - '0')
					// publish event with the slot number
					if m != nil && m.w != nil && m.w.eventBus != nil {
						go func(s int) { m.w.eventBus.Publish("devsync.pty.alt", s) }(slotNum)
					}
					// fmt.Println("slotNum :: ", slotNum)
					// write a marker for debug
					go func(sInt int) {
						fname := fmt.Sprintf("/tmp/make-sync-%v.log", sInt)
						f, err := os.OpenFile(fname, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
						if err == nil {
							defer f.Close()
							f.WriteString(fmt.Sprintf("%d alt pressed\n", sInt))
						}
						fmt.Println("vvvvvvvvvvvvvvvvvvvv")
						switch sInt {
						case slot:
						case 1:
							if callback != nil {
								callback(sInt)
							}
						// same slot, ignore
						case 3, 4, 5, 6, 7, 8, 9:
							// valid slot, attempt to focus it
							if callback != nil {
								callback(sInt)
							}
						default:
							// invalid slot, ignore
						}
					}(slotNum)
				}
			}
		}
	}

	// Save previous matcher/callback and install combined matcher+callback
	var prevMatcher func([]byte) bool
	var prevCallback func([]byte)
	if s.Bridge != nil {
		prevMatcher = s.Bridge.StdinMatcher
		prevCallback = s.Bridge.StdinCallback
		s.Bridge.StdinMatcher = func(b []byte) bool {
			// detect Ctrl+G or ESC + digit anywhere
			for i := 0; i < len(b); i++ {
				if b[i] == 0x07 {
					return true
				}
				if b[i] == 0x1b && i+1 < len(b) {
					d := b[i+1]
					if d >= '1' && d <= '9' {
						return true
					}
				}
			}
			return false
		}
	}
	// Install a simple observer that logs stdin chunks to a temp file for debugging
	var prevObserver func([]byte)
	if s.Bridge != nil {
		prevObserver = s.Bridge.StdinObserver
		s.Bridge.StdinObserver = func(b []byte) {
			// append to temp file (best-effort)
			go func(data []byte) {
				f, _ := os.OpenFile("/tmp/make-sync-pty-input.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
				if f == nil {
					return
				}
				defer f.Close()
				f.Write(data)
			}(b)
		}
	}
	s.Bridge.StdinCallback = cb

	m.Pendingchan = make(chan string, 1)
	if !isExist {
		m.Pendingchan <- "start" // start unpaused
	} else {
		m.Pendingchan <- "unpause" // start unpaused
	}
	for msg := range m.Pendingchan {
		switch {
		case msg == "pause":
			m.w.printer.PrintBlock("⏸️  Press any key to return to menu...", true)
			m.w.printer.ClearLine()
			s.Bridge.StdinCallback = nil

			err := s.Bridge.Pause()
			if err != nil {
				if m.w != nil && m.w.printer != nil {
					m.w.printer.Printf("❌ Failed to pause PTY session: %v\n", err)
				} else {
					fmt.Printf("❌ Failed to pause PTY session: %v\n", err)
				}
			}
			close(m.Pendingchan)
		case msg == "unpause":
			err := m.ResumeSlot(slot)
			if err != nil {
				if m.w != nil && m.w.printer != nil {
					m.w.printer.Printf("❌ Failed to pause PTY session: %v\n", err)
				} else {
					fmt.Printf("❌ Failed to pause PTY session: %v\n", err)
				}
				os.Exit(1)
			}
			m.w.printer.PrintBlock(fmt.Sprintf("✅ You are in slot %d. Press any key to resume\n", slot), true)
			m.w.printer.ClearLine()
		case msg == "start":
			go func() {
				if err := s.Bridge.StartInteractiveShell(cb); err != nil {
					s.Running = false
					if s.Bridge != nil {
						s.Bridge.StdinObserver = prevObserver
					}
				}
				m.CloseSlot(slot)
			}()
		}
	}
	// restore previous observer, matcher and callback when Focus returns
	if s.Bridge != nil {
		s.Bridge.StdinObserver = prevObserver
		s.Bridge.StdinMatcher = prevMatcher
		s.Bridge.StdinCallback = prevCallback
	}
	s.mu.Lock()
	s.Running = false
	s.mu.Unlock()
	return nil
}

// PauseSlot pauses the PTY session in the given slot.
func (m *PTYManager) PauseSlot(slot int) error {
	m.mu.Lock()
	s, ok := m.sessions[slot]
	s.Bridge.StdinCallback = nil
	m.mu.Unlock()
	if !ok || s == nil || s.Bridge == nil {
		return fmt.Errorf("no session in slot %d", slot)
	}
	return s.Bridge.Pause()
}

// ResumeSlot resumes the PTY session in the given slot.
func (m *PTYManager) ResumeSlot(slot int) error {
	m.mu.Lock()
	s, ok := m.sessions[slot]
	m.mu.Unlock()
	if !ok || s == nil || s.Bridge == nil {
		return fmt.Errorf("no session in slot %d", slot)
	}
	return s.Bridge.Resume()
}

// CloseSlot closes and removes the session in slot (remote bridge closed).
func (m *PTYManager) CloseSlot(slot int) error {
	m.w.printer.ClearScreen()
	time.Sleep(400 * time.Millisecond)
	// Jangan clear layar langsung — gunakan PrintBlock dan minta Watcher redraw.
	if m.w != nil && m.w.printer != nil {
		// pastikan printer aktif
		m.w.printer.Resume()

		// tampilkan pesan tanpa mengosongkan menu secara permanen
		m.w.printer.PrintBlock("", true)
		m.w.printer.PrintBlock("🔌 Remote PTY session closed. Press Enter to return menu...", true)

		// best-effort: minta Watcher merender ulang menu (jika ada eventBus)
		if m.w.eventBus != nil {
			go func() { m.w.eventBus.Publish("devsync.menu.show") }()
		}
	} else {
		fmt.Println("🔌 Remote PTY session closed. Press Enter to return menu...")
	}
	m.mu.Lock()
	if m.Pendingchan != nil {
		close(m.Pendingchan)
		m.Pendingchan = nil
	}
	s, ok := m.sessions[slot]
	if !ok || s == nil {
		m.mu.Unlock()
		return nil
	}
	delete(m.sessions, slot)
	m.mu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Bridge != nil {
		_ = s.Bridge.Close()
	}
	s.Running = false
	return nil
}

// HasSlot returns whether there's a session in slot
func (m *PTYManager) HasSlot(slot int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.sessions[slot]
	return ok
}

// ListSlots returns active slots
func (m *PTYManager) ListSlots() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]int, 0, len(m.sessions))
	for k := range m.sessions {
		res = append(res, k)
	}
	return res
}
