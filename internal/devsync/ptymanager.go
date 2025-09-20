package devsync

import (
	"fmt"
	"make-sync/internal/util"
	"os"
	"sync"
	"time"
)

// PTYSession represents one persistent PTY session (remote via SSH bridge)
type PTYSession struct {
	Slot    int
	Cmd     string
	Bridge  Bridge
	created time.Time
}

// PTYManager manages multiple persistent PTY sessions (slots 3..9)
type PTYManager struct {
	w            *Watcher
	sessions     map[int]*PTYSession
	Pendingchan  chan string  // channel to send pause/unpause/exit commands
	mu           sync.RWMutex // protect sessions map
	pendingMu    sync.Mutex   // protect Pendingchan creation/close
	bridgeActive Bridge       // currently active bridge (if any)
	routerStop   chan struct{}
	wgGroup      sync.WaitGroup
}

// setPending sets the Pendingchan under lock
func (m *PTYManager) setPending(ch chan string) {
	m.pendingMu.Lock()
	m.Pendingchan = ch
	m.pendingMu.Unlock()
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
	// if slot < 3 || slot > 9 {
	// 	return fmt.Errorf("slot must be 3..9")
	// }

	if m.w == nil || m.w.sshClient == nil {
		return fmt.Errorf("no SSH client available")
	}

	// do not overwrite existing slot
	m.mu.RLock()
	_, exists := m.sessions[slot]
	m.mu.RUnlock()
	if exists {
		return fmt.Errorf("slot %d already exists", slot)
	}

	bridge, err := CreateSSHBridgeWithCommand(m.w.sshClient, remoteCmd)
	if err != nil {
		return fmt.Errorf("failed to create bridge: %w", err)
	}

	s := &PTYSession{
		Slot:    slot,
		Cmd:     remoteCmd,
		Bridge:  bridge,
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

	m.bridgeActive = s.Bridge

	util.Default.Resume()

	// NOTE: Shortcut detection (Ctrl+G / ESC+digit) is handled centrally by the
	// Watcher/PTYManager keyboard router. Do not install per-bridge stdin
	// matchers/callbacks here to avoid ownership/race issues. Bridges act as
	// dumb IO and receive stdin writes from the manager.

	// prepare pending channel
	ch := make(chan string, 1)
	m.setPending(ch)
	if !isExist {
		// m.safeSend("start")
		ch <- "start"
	} else {
		// m.safeSend("unpause")
		ch <- "unpause"
	}

	// central stdin router: reads os.Stdin and forwards to active bridge's stdin writer.
	// It also detects Ctrl+G (0x07) to pause and ESC+digit (Alt+[n]) to trigger slot switch.
	m.routerStop = make(chan struct{})
	// register exit listener so that when the bridge exits we stop router and
	// close the pending channel to unblock Focus.
	if m.bridgeActive != nil {
		m.bridgeActive.SetOnExitListener(func() {
			// close pending and router in a goroutine to avoid blocking bridge
			go m.CloseSlot(slot)
		})
	}
	m.wgGroup.Add(1)
	go func() {
		defer func() {
			m.wgGroup.Done()
			// fmt.Println("<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<< router goroutine exiting for slot ", slot)
		}()
		// defer routerWg.Done()
		buf := make([]byte, 4096)
		// escPending holds whether last read ended with ESC (0x1b)
		// escPending := false
		for {
			select {
			case <-m.routerStop:
				// fmt.Println(">>>>>>>>>>>>>>>>>>>>>>>>>>>>>> router stop detected for slot ", slot)
				return
			default:
				n, err := os.Stdin.Read(buf)
				if n <= 0 {
					if err != nil {
						return
					}
					continue
				}
				data := buf[:n]
				i := 0

				for i < len(data) {
					b := data[i]
					if b == 0x1b {
						nb := data[i+1]
						if nb == '1' || nb == '2' {
							slotNum := int(nb - '0')
							callback(slotNum)
							m.PauseSlot(slot)
							close(m.routerStop)
							i += 2
							continue
						}
						if nb == '0' {
							// Alt+0 - close current session
							m.CloseSlot(slot)
							i += 2
							continue
						}
						if nb >= '3' && nb <= '9' {
							slotNum := int(nb - '0')
							if slotNum == slot {
								// already in this slot: ignore
							} else {
								if callback != nil {
									// fmt.Println(">>>>>>>>>>>>>>>>>>>>>>>>>>>>>> xxxxxxxxxxxxxxxxxxxxxxxxxx :: ", slotNum)
									callback(slotNum)
									m.PauseSlot(slot)
									close(m.routerStop)
								}
							}
							// m.safeSend("pause")
							i += 2
							continue
						}
						// unknown sequence: forward ESC normally
						w := m.bridgeActive.GetStdinWriter()
						if w != nil {
							_, _ = w.Write([]byte{0x1b})
						}
						i++
						continue
					}
					// otherwise forward normal byte to bridge
					w := m.bridgeActive.GetStdinWriter()
					if w != nil {
						_, _ = w.Write(data[i : i+1])
					}
					i++
				}
				// fmt.Println("kkkkkkkkkkkkkk :: ", slot, data)
			}
		}
	}()

	// message loop (blocks until Pendingchan closed)
	for msg := range m.Pendingchan {
		switch msg {
		case "pause":
			util.Default.PrintBlock("⏸️  Press any key to return to menu...", true)
			util.Default.ClearLine()
			return nil

		case "unpause":
			if err := m.ResumeSlot(slot); err != nil {
				util.Default.Printf("❌ Failed to resume PTY session: %v\n", err)
				os.Exit(1)
			}
			util.Default.PrintBlock(fmt.Sprintf("✅ You are in slot %d. Press any key to resume\n", slot), true)
			util.Default.ClearLine()
		case "start":
			go func() {
				if err := m.bridgeActive.StartInteractiveShell(nil); err != nil {
					util.Default.Printf("❌ Failed to start interactive shell: %v\n", err)
					// os.Exit(1)
				}
				// _ = m.CloseSlot(slot)
			}()
		}
	}
	m.wgGroup.Wait()
	// fmt.Println("<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<< Focus exiting for slot ", slot)
	// No per-bridge stdin matchers/callbacks to restore; keyboard shortcuts
	// are handled by the Watcher/PTYManager input router.

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
	err := s.Bridge.Pause()
	close(m.Pendingchan)

	return err
}

// ResumeSlot resumes the PTY session in the given slot.
func (m *PTYManager) ResumeSlot(slot int) error {
	m.mu.RLock()
	s, ok := m.sessions[slot]
	m.mu.RUnlock()
	if !ok || s == nil || m.bridgeActive == nil {
		return fmt.Errorf("no session in slot %d", slot)
	}

	return m.bridgeActive.Resume()
}

// CloseSlot closes and removes the session in slot (remote bridge closed).
func (m *PTYManager) CloseSlot(slot int) error {

	// cleanup
	if m.routerStop != nil {
		close(m.routerStop)
	}

	if m.Pendingchan != nil {
		close(m.Pendingchan)
	}

	util.Default.ClearScreen()
	time.Sleep(400 * time.Millisecond)
	util.Default.Resume()
	util.Default.PrintBlock("", true)
	util.Default.PrintBlock("🔌 Remote PTY session closed. Press Enter to return menu...", true)

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

	util.RestoreGlobal()
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

// OpenLocalSlot creates (but does not attach) a local PTY session in the given slot.
func (m *PTYManager) OpenLocalSlot(slot int, initialCmd string) error {
	// if slot < 3 || slot > 9 {
	// 	return fmt.Errorf("slot must be 3..9")
	// }
	// do not overwrite existing slot
	m.mu.RLock()
	_, exists := m.sessions[slot]
	m.mu.RUnlock()
	if exists {
		return fmt.Errorf("slot %d already exists", slot)
	}
	bridge, err := CreateLocalBridge(initialCmd)
	if err != nil {
		return fmt.Errorf("failed to create local bridge: %w", err)
	}
	s := &PTYSession{
		Slot:    slot,
		Cmd:     initialCmd,
		Bridge:  bridge,
		created: time.Now(),
	}
	m.mu.Lock()
	m.sessions[slot] = s
	m.mu.Unlock()
	return nil
}
