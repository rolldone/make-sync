package devsync

import (
	"fmt"
	"log"
	"make-sync/internal/devsync/localclient"
	"make-sync/internal/sshclient"
	"make-sync/internal/util"
	"os"
	"strconv"
	"strings"
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
	w                *Watcher
	sessions         map[int]*PTYSession
	Pendingchan      chan string  // channel to send pause/unpause/exit commands
	mu               sync.RWMutex // protect sessions map
	pendingMu        sync.Mutex   // protect Pendingchan creation/close
	bridgeActive     Bridge       // currently active bridge (if any)
	routerStop       chan struct{}
	wgGroup          sync.WaitGroup
	bridgeActiveSlot int // slot number of currently active bridge
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
	m.bridgeActiveSlot = slot // simpan slot aktif

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

	// Setup input listeners ONCE - no need to re-setup in loop
	m.bridgeActive.SetOnInputListener(func(b []byte) {
		// fmt.Println("DEBUG: input listener received data:", string(b))
	})
	m.bridgeActive.SetOnInputHitCodeListener(func(b string) {
		// Only process alt-style messages like "alt+N" or "alt+NN".
		if strings.Contains(b, "alt") {
			// extract digits from the string
			newSlot := ""
			for _, r := range b {
				if r >= '0' && r <= '9' {
					newSlot += string(r)
				}
			}
			if newSlot != "" {
				// fmt.Println("DEBUG: input hit digits:", digits)
				gg, err := strconv.Atoi(newSlot)
				if err != nil {
					fmt.Println("DEBUG: invalid slot number:", newSlot)
					return
				}
				callback(gg)
				m.PauseSlot(slot)
			}
		}
	})

	// register exit listener so that when the bridge exits we stop router and
	// close the pending channel to unblock Focus.
	m.bridgeActive.SetOnExitListener(func() {
		// close pending and router in a goroutine to avoid blocking bridge
		log.Println("SetOnExitListener: exit listener called")
		util.Default.Print("PTYManager: Bridge exit listener called, closing slot", slot)
		util.Default.ClearLine()
		m.CloseSlot(slot)
		log.Println("SetOnExitListener: slot", slot, "closed by exit listener")
	})

	// Simple router goroutine - just waits for stop signal
	m.wgGroup.Add(1)
	go func() {
		defer func() {
			m.wgGroup.Done()
			util.Default.ClearLine()
			util.Default.Println("DEBUG: router goroutine exiting")
			util.Default.ClearLine()
		}()
		// Block until stop signal - no more tight loop!
		<-m.routerStop
	}()

	// message loop (blocks until Pendingchan closed)
	m.wgGroup.Add(1)
	go func() {
		defer func() {
			m.wgGroup.Done()
			log.Println("DEBUG: message loop goroutine exiting")
		}()
		for msg := range m.Pendingchan {
			switch msg {
			case "pause":
				util.Default.PrintBlock("⏸️  Press any key to return to menu...", true)
				util.Default.ClearLine()

			case "unpause":
				if err := m.ResumeSlot(slot); err != nil {
					util.Default.Printf("❌ Failed to resume PTY session: %v\n", err)
					// os.Exit(1)
				}
			case "start":
				go func() {
					if err := m.bridgeActive.StartInteractiveShell(); err != nil {
						util.Default.Printf("❌ Failed to start interactive shell: %v\n", err)
						os.Exit(1)
					}
				}()
			}
		}
	}()
	m.wgGroup.Wait()
	// No per-bridge stdin matchers/callbacks to restore; keyboard shortcuts
	// are handled by the Watcher/PTYManager input router.
	log.Println("DEBUG: Focus exiting normally")
	return nil
}

// SetOutputTapForSlot sets an output tap on the bridge for a given slot, if supported.
// The tap receives stdout/stderr bytes (err=true for stderr) regardless of UI output state.
func (m *PTYManager) SetOutputTapForSlot(slot int, tap func([]byte, bool)) error {
	m.mu.RLock()
	s, ok := m.sessions[slot]
	m.mu.RUnlock()
	if !ok || s == nil || s.Bridge == nil {
		return fmt.Errorf("no session in slot %d", slot)
	}
	// SSH bridge
	if sb, ok := s.Bridge.(*sshclient.PTYSSHBridge); ok {
		sb.SetOutputTap(tap)
		return nil
	}
	// Local bridge
	if lb, ok := s.Bridge.(*localclient.PTYLocalBridge); ok {
		lb.SetOutputTap(tap)
		return nil
	}
	return fmt.Errorf("bridge for slot %d does not support output tap", slot)
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
	s.Bridge.SetOnInputListener(nil)
	s.Bridge.SetOnInputHitCodeListener(nil)
	err := s.Bridge.Pause()

	close(m.Pendingchan)
	close(m.routerStop)
	return err
}

// ResumeSlot resumes the PTY session in the given slot.
func (m *PTYManager) ResumeSlot(slot int) error {
	util.Default.PrintBlock(fmt.Sprintf("✅ You are in slot %d. Press any key to resume\n", slot), true)
	util.Default.ClearLine()
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

	log.Println("PTYManager: CloseSlot called for slot", slot)

	// util.Default.ClearScreen()
	// time.Sleep(400 * time.Millisecond)
	util.Default.Resume()
	util.Default.PrintBlock("", true)
	util.Default.PrintBlock("🔌 Remote PTY session closed. Press Enter to return menu...", true)
	log.Println("PTYManager: Remote PTY session closed. Press Enter to return menu...")

	m.mu.Lock()
	s, ok := m.sessions[slot]
	log.Println("PTYManager: Retrieved session from map for slot", slot, "ok=", ok)
	if ok {
		delete(m.sessions, slot)
		log.Println("PTYManager: Deleted session from map for slot", slot)
	}
	m.mu.Unlock()
	log.Println("PTYManager: Session removed from map for slot", slot)
	if !ok || s == nil {
		log.Println("PTYManager: No session found for slot", slot)
		return nil
	}
	log.Println("PTYManager: Found session for slot", slot)
	err := s.Bridge.Close()
	if err != nil {
		log.Println("PTYManager: Error closing bridge for slot", slot, ":", err)
	}
	log.Println("PTYManager: Bridge closed for slot", slot)

	// cleanup
	close(m.routerStop)
	log.Println("PTYManager: routerStop channel closed")
	close(m.Pendingchan)
	log.Println("PTYManager: Pendingchan channel closed")

	log.Println("PTYManager: Waiting for goroutines to finish")

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
	util.ResetRaw(m.w.oldState)
	oldState, err := util.NewRaw()
	if err != nil {
		return fmt.Errorf("failed to enable raw mode: %w", err)
	}
	m.w.oldState = oldState
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
