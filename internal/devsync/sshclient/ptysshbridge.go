package sshclient

import (
	"fmt"
	"io"
	"os"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"

	"make-sync/internal/pty"
	"make-sync/internal/util"
)

// PTYSSHBridge represents a bridge between PTY and SSH session for interactive sessions
type PTYSSHBridge struct {
	localPTY    pty.PTY
	sshClient   *SSHClient
	sshSession  *ssh.Session
	termRestore func() error
	ioCancel    chan bool
	ioOnce      sync.Once

	initialCommand string

	StdinMatcher   func([]byte) bool
	StdinCallback  func([]byte)
	StdinObserver  func([]byte)
	outputDisabled bool
	outputMu       sync.Mutex

	localTTY *os.File

	// stdin control
	stopStdinCh chan struct{}

	stdinPipe io.WriteCloser

	mu    sync.RWMutex // protect concurrent access to shared fields below
	stdin io.WriteCloser
	// exit listener called when the interactive session exits
	exitListener func()
	exitMu       sync.Mutex
}

// NewPTYSSHBridge creates a new PTY-SSH bridge for interactive sessions
func NewPTYSSHBridge(sshClient *SSHClient) (*PTYSSHBridge, error) {
	ptWrapper, ptFile, err := pty.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to create PTY: %v", err)
	}
	sshSession, err := sshClient.CreatePTYSession()
	if err != nil {
		if ptWrapper != nil {
			ptWrapper.Close()
		}
		return nil, fmt.Errorf("failed to create SSH session: %v", err)
	}

	return &PTYSSHBridge{
		localPTY:   ptWrapper,
		localTTY:   ptFile,
		sshClient:  sshClient,
		sshSession: sshSession,
		ioCancel:   make(chan bool),
		ioOnce:     sync.Once{},
	}, nil
}

func NewPTYSSHBridgeWithCommand(sshClient *SSHClient, initialCommand string) (*PTYSSHBridge, error) {
	// keep behavior consistent with NewPTYSSHBridge: open a PTY master
	ptWrapper, ptFile, err := pty.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to create PTY: %v", err)
	}
	sshSession, err := sshClient.CreatePTYSession()
	if err != nil {
		if ptWrapper != nil {
			_ = ptWrapper.Close()
		}
		if ptFile != nil {
			_ = ptFile.Close()
		}
		return nil, fmt.Errorf("failed to create SSH session: %v", err)
	}

	return &PTYSSHBridge{
		localPTY:       ptWrapper,
		localTTY:       ptFile,
		sshClient:      sshClient,
		sshSession:     sshSession,
		ioCancel:       make(chan bool),
		ioOnce:         sync.Once{},
		initialCommand: initialCommand,
	}, nil
}

// StartInteractiveShell starts an interactive shell session
func (bridge *PTYSSHBridge) StartInteractiveShell(cb func([]byte)) error {
	cols, rows := 80, 24
	isTTY := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	if isTTY {
		w, h, err := term.GetSize(int(os.Stdin.Fd()))
		if err == nil && w > 0 && h > 0 {
			cols, rows = w, h
		}
	}
	if err := bridge.sshSession.RequestPty("xterm-256color", rows, cols, ssh.TerminalModes{}); err != nil {
		if bridge.localPTY != nil {
			_ = bridge.localPTY.Close()
		}
		bridge.sshSession.Close()
		return fmt.Errorf("failed to request PTY: %v", err)
	}
	if isTTY {
		restore, err := util.EnableRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("failed to set raw mode: %v", err)
		}
		bridge.termRestore = restore
	}
	stdinPipe, err := bridge.sshSession.StdinPipe()
	if err != nil {
		return err
	}
	bridge.stdinPipe = stdinPipe
	// expose stdin writer so PTYManager can forward stdin bytes into the session
	bridge.SetStdinWriter(stdinPipe)
	stdoutPipe, err := bridge.sshSession.StdoutPipe()
	if err != nil {
		return err
	}
	stderrPipe, err := bridge.sshSession.StderrPipe()
	if err != nil {
		return err
	}
	bridge.sshSession.Setenv("TERM", "xterm-256color")

	if bridge.initialCommand != "" {
		if err := bridge.sshSession.Start(bridge.initialCommand); err != nil {
			return err
		}
	} else {
		if err := bridge.sshSession.Shell(); err != nil {
			return err
		}
	}

	// Note: the bridge no longer starts a stdin-reading goroutine. The PTYManager
	// is responsible for reading os.Stdin and forwarding bytes into the bridge's
	// stdin writer. This avoids multiple readers on os.Stdin and centralizes
	// shortcut handling.

	// handle output
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdoutPipe.Read(buf)
			if n > 0 {
				bridge.outputMu.Lock()
				disabled := bridge.outputDisabled
				bridge.outputMu.Unlock()
				if !disabled {
					os.Stdout.Write(buf[:n])
				}
			}
			if err != nil {
				bridge.ioOnce.Do(func() { close(bridge.ioCancel) })
				return
			}
		}
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stderrPipe.Read(buf)
			if n > 0 {
				bridge.outputMu.Lock()
				disabled := bridge.outputDisabled
				bridge.outputMu.Unlock()
				if !disabled {
					os.Stderr.Write(buf[:n])
				}
			}
			if err != nil {
				bridge.ioOnce.Do(func() { close(bridge.ioCancel) })
				return
			}
		}
	}()

	err = bridge.sshSession.Wait()
	bridge.ioOnce.Do(func() { close(bridge.ioCancel) })
	// Notify registered exit listener (if any). Protect invocation with mutex
	bridge.exitMu.Lock()
	if bridge.exitListener != nil {
		go func(cb func()) {
			defer func() { _ = recover() }()
			cb()
		}(bridge.exitListener)
		bridge.exitListener = nil
	}
	bridge.exitMu.Unlock()
	return err
}

// Pause stops stdin/output
func (bridge *PTYSSHBridge) Pause() error {
	if bridge.stopStdinCh != nil {
		close(bridge.stopStdinCh)
		bridge.stopStdinCh = nil
	}

	bridge.outputMu.Lock()
	bridge.outputDisabled = true
	bridge.outputMu.Unlock()

	if bridge.termRestore != nil {
		_ = bridge.termRestore()
		bridge.termRestore = nil
	}
	return nil
}

// Resume restarts stdin/output
func (bridge *PTYSSHBridge) Resume() error {
	bridge.outputMu.Lock()
	bridge.outputDisabled = false
	bridge.outputMu.Unlock()

	if term.IsTerminal(int(os.Stdin.Fd())) {
		restore, err := util.EnableRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("failed to set raw mode: %v", err)
		}
		bridge.termRestore = restore
	}

	if bridge.stdinPipe != nil {
		// bridge.stopStdinCh = make(chan struct{})
		// go bridge.stdinLoop()
	}
	return nil
}

// Close closes bridge
func (bridge *PTYSSHBridge) Close() error {
	bridge.ioOnce.Do(func() {
		close(bridge.ioCancel)
	})
	if bridge.termRestore != nil {
		_ = bridge.termRestore()
		bridge.termRestore = nil
	}
	if bridge.stopStdinCh != nil {
		close(bridge.stopStdinCh)
	}
	if bridge.localTTY != nil {
		bridge.localTTY.Close()
	}
	if bridge.sshSession != nil {
		bridge.sshSession.Close()
	}
	if bridge.localPTY != nil {
		_ = bridge.localPTY.Close()
		bridge.localPTY = nil
	}
	return nil
}

// Thread-safe setters/getters for stdin hooks and writer

func (b *PTYSSHBridge) SetStdinMatcher(m func([]byte) bool) {
	b.mu.Lock()
	b.StdinMatcher = m
	b.mu.Unlock()
}

func (b *PTYSSHBridge) GetStdinMatcher() func([]byte) bool {
	b.mu.RLock()
	m := b.StdinMatcher
	b.mu.RUnlock()
	return m
}

func (b *PTYSSHBridge) SetStdinCallback(cb func([]byte)) {
	b.mu.Lock()
	b.StdinCallback = cb
	b.mu.Unlock()
}

func (b *PTYSSHBridge) GetStdinCallback() func([]byte) {
	b.mu.RLock()
	cb := b.StdinCallback
	b.mu.RUnlock()
	return cb
}

func (b *PTYSSHBridge) SetStdinObserver(o func([]byte)) {
	b.mu.Lock()
	b.StdinObserver = o
	b.mu.Unlock()
}

func (b *PTYSSHBridge) GetStdinObserver() func([]byte) {
	b.mu.RLock()
	o := b.StdinObserver
	b.mu.RUnlock()
	return o
}

// Optionally expose safe access to the active stdin writer if needed
func (b *PTYSSHBridge) setStdinWriter(w io.WriteCloser) {
	b.mu.Lock()
	b.stdin = w
	b.mu.Unlock()
}

func (b *PTYSSHBridge) getStdinWriter() io.WriteCloser {
	b.mu.RLock()
	w := b.stdin
	b.mu.RUnlock()
	return w
}

// Exported wrappers to satisfy Bridge interface in other packages.
func (b *PTYSSHBridge) SetStdinWriter(w io.WriteCloser) { b.setStdinWriter(w) }
func (b *PTYSSHBridge) GetStdinWriter() io.WriteCloser  { return b.getStdinWriter() }

// SetOnExitListener registers a callback to be executed when the bridge detects
// the interactive session has ended. The callback will be executed at most once.
func (b *PTYSSHBridge) SetOnExitListener(cb func()) {
	b.exitMu.Lock()
	b.exitListener = cb
	b.exitMu.Unlock()
}
