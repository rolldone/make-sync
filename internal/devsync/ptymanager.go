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
	created time.Time
}

// PTYManager manages multiple persistent PTY sessions (slots 3..9)
type PTYManager struct {
	w           *Watcher
	sessions    map[int]*PTYSession
	Pendingchan chan string  // channel to send pause/unpause/exit commands
	mu          sync.RWMutex // protect sessions map
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

	// add session
	m.mu.Lock()
	m.sessions[slot] = s
	m.mu.Unlock()
	return nil
}

// Focus attaches current terminal to the given slot's PTY and starts interactive session.
// This call will block until the interactive session exits. Caller must ensure keyboard
// handler isn't concurrently reading (watcher should restore terminal before calling Focus).
func (m *PTYManager) Focus(slot int, isExist bool, callback func(slotNew int)) error {
	m.mu.RLock()
	s, ok := m.sessions[slot]
	m.mu.RUnlock()
	if !ok || s == nil {
		return fmt.Errorf("no session in slot %d", slot)
	}

	// mark running
	s.Running = true

	if m.w != nil && m.w.printer != nil {
		m.w.printer.Resume()
	}

	// Build the bridge callback that handles Ctrl+G and ESC+digit
	cb := func(gg []byte) {
		for i := 0; i < len(gg); i++ {
			b := gg[i]
			if b == 0x07 { // Ctrl+G
				// debug marker
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
					if m != nil && m.w != nil && m.w.eventBus != nil {
						go func(s int) { m.w.eventBus.Publish("devsync.pty.alt", s) }(slotNum)
					}
					go func(sInt int) {
						fname := fmt.Sprintf("/tmp/make-sync-%v.log", sInt)
						f, err := os.OpenFile(fname, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
						if err == nil {
							defer f.Close()
							f.WriteString(fmt.Sprintf("%d alt pressed\n", sInt))
						}
						switch sInt {
						case slot:
							// same slot -> ignore
						case 1:
							if callback != nil {
								callback(sInt)
							}
						case 3, 4, 5, 6, 7, 8, 9:
							if callback != nil {
								callback(sInt)
							}
						default:
							// ignore
						}
					}(slotNum)
				}
			}
		}
	}

	// Save previous matcher/callback and install combined matcher+callback using thread-safe setters
	var prevMatcher func([]byte) bool
	var prevCallback func([]byte)
	var prevObserver func([]byte)

	if s.Bridge != nil {
		prevMatcher = s.Bridge.GetStdinMatcher()
		prevCallback = s.Bridge.GetStdinCallback()
		// Install matcher that detects Ctrl+G or ESC+digit anywhere
		s.Bridge.SetStdinMatcher(func(b []byte) bool {
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
		})
		// install simple observer for debug
		prevObserver = s.Bridge.GetStdinObserver()
		s.Bridge.SetStdinObserver(func(b []byte) {
			go func(data []byte) {
				f, _ := os.OpenFile("/tmp/make-sync-pty-input.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
				if f == nil {
					return
				}
				defer f.Close()
				f.Write(data)
			}(b)
		})
		// install callback
		s.Bridge.SetStdinCallback(cb)
	}

	// prepare pending channel
	m.Pendingchan = make(chan string, 1)
	if !isExist {
		m.Pendingchan <- "start"
	} else {
		m.Pendingchan <- "unpause"
	}

	// message loop (blocks until Pendingchan closed)
	for msg := range m.Pendingchan {
		switch msg {
		case "pause":
			if m.w != nil && m.w.printer != nil {
				m.w.printer.PrintBlock("⏸️  Press any key to return to menu...", true)
				m.w.printer.ClearLine()
			}
			// clear callback while paused
			if s.Bridge != nil {
				s.Bridge.SetStdinCallback(nil)
			}

			if err := s.Bridge.Pause(); err != nil {
				if m.w != nil && m.w.printer != nil {
					m.w.printer.Printf("❌ Failed to pause PTY session: %v\n", err)
				} else {
					fmt.Printf("❌ Failed to pause PTY session: %v\n", err)
				}
			}
			close(m.Pendingchan)

		case "unpause":
			if err := m.ResumeSlot(slot); err != nil {
				if m.w != nil && m.w.printer != nil {
					m.w.printer.Printf("❌ Failed to resume PTY session: %v\n", err)
				} else {
					fmt.Printf("❌ Failed to resume PTY session: %v\n", err)
				}
				os.Exit(1)
			}
			if m.w != nil && m.w.printer != nil {
				m.w.printer.PrintBlock(fmt.Sprintf("✅ You are in slot %d. Press any key to resume\n", slot), true)
				m.w.printer.ClearLine()
			}

		case "start":
			go func() {
				if err := s.Bridge.StartInteractiveShell(cb); err != nil {
					s.Running = false
					// restore previous observer if bridge still exists
					if s.Bridge != nil {
						s.Bridge.SetStdinObserver(prevObserver)
					}
				}
				_ = m.CloseSlot(slot)
			}()
		}
	}

	// restore previous observer/matcher/callback
	if s.Bridge != nil {
		s.Bridge.SetStdinObserver(prevObserver)
		s.Bridge.SetStdinMatcher(prevMatcher)
		s.Bridge.SetStdinCallback(prevCallback)
	}

	s.Running = false
	return nil
}

// PauseSlot pauses the PTY session in the given slot.
func (m *PTYManager) PauseSlot(slot int) error {
	m.mu.RLock()
	s, ok := m.sessions[slot]
	m.mu.RUnlock()
	if !ok || s == nil || s.Bridge == nil {
		return fmt.Errorf("no session in slot %d", slot)
	}
	// clear callback then pause
	s.Bridge.SetStdinCallback(nil)
	return s.Bridge.Pause()
}

// ResumeSlot resumes the PTY session in the given slot.
func (m *PTYManager) ResumeSlot(slot int) error {
	m.mu.RLock()
	s, ok := m.sessions[slot]
	m.mu.RUnlock()
	if !ok || s == nil || s.Bridge == nil {
		return fmt.Errorf("no session in slot %d", slot)
	}
	return s.Bridge.Resume()
}

// CloseSlot closes and removes the session in slot (remote bridge closed).
func (m *PTYManager) CloseSlot(slot int) error {
	// show message
	if m.w != nil && m.w.printer != nil {
		m.w.printer.ClearScreen()
		time.Sleep(400 * time.Millisecond)
		m.w.printer.Resume()
		m.w.printer.PrintBlock("", true)
		m.w.printer.PrintBlock("🔌 Remote PTY session closed. Press Enter to return menu...", true)
		if m.w.eventBus != nil {
			go func() { m.w.eventBus.Publish("devsync.menu.show") }()
		}
	} else {
		fmt.Println("🔌 Remote PTY session closed. Press Enter to return menu...")
	}

	// cleanup
	if m.Pendingchan != nil {
		close(m.Pendingchan)
		m.Pendingchan = nil
	}
	m.mu.Lock()
	s, ok := m.sessions[slot]
	if ok {
		delete(m.sessions, slot)
	}
	m.mu.Unlock()
	if !ok || s == nil {
		return nil
	}

	if s.Bridge != nil {
		_ = s.Bridge.Close()
	}
	s.Running = false
	return nil
}

// HasSlot returns whether there's a session in slot
func (m *PTYManager) HasSlot(slot int) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.sessions[slot]
	return ok
}

func (m *PTYManager) ListSlots() []int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make([]int, 0, len(m.sessions))
	for k := range m.sessions {
		res = append(res, k)
	}
	return res
}
