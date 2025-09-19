package sshclient

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/aymanbagabas/go-pty"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// PTYSSHBridge represents a bridge between PTY and SSH session for interactive sessions
type PTYSSHBridge struct {
	pty          pty.Pty
	sshClient    *SSHClient
	sshSession   *ssh.Session
	termOldState *term.State
	ioCancel     chan bool
	ioOnce       sync.Once

	initialCommand string

	StdinMatcher   func([]byte) bool
	StdinCallback  func([]byte)
	StdinObserver  func([]byte)
	inputDisabled  bool
	inputMu        sync.Mutex
	outputDisabled bool
	outputMu       sync.Mutex

	localTTY *os.File

	// stdin control
	stopStdinCh chan struct{}
	stdinWg     sync.WaitGroup

	stdinPipe io.WriteCloser
}

// NewPTYSSHBridge creates a new PTY-SSH bridge for interactive sessions
func NewPTYSSHBridge(sshClient *SSHClient) (*PTYSSHBridge, error) {
	ptyMaster, err := pty.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create PTY: %v", err)
	}
	sshSession, err := sshClient.CreatePTYSession()
	if err != nil {
		ptyMaster.Close()
		return nil, fmt.Errorf("failed to create SSH session: %v", err)
	}
	tty, _ := os.OpenFile("/dev/tty", os.O_WRONLY, 0)

	return &PTYSSHBridge{
		pty:        ptyMaster,
		sshClient:  sshClient,
		sshSession: sshSession,
		ioCancel:   make(chan bool),
		ioOnce:     sync.Once{},
		localTTY:   tty,
	}, nil
}

// NewPTYSSHBridgeWithCommand creates a new PTY-SSH bridge with initial command
func NewPTYSSHBridgeWithCommand(sshClient *SSHClient, initialCommand string) (*PTYSSHBridge, error) {
	ptyMaster, err := pty.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create PTY: %v", err)
	}
	sshSession, err := sshClient.CreatePTYSession()
	if err != nil {
		ptyMaster.Close()
		return nil, fmt.Errorf("failed to create SSH session: %v", err)
	}
	tty, _ := os.OpenFile("/dev/tty", os.O_WRONLY, 0)

	return &PTYSSHBridge{
		pty:            ptyMaster,
		sshClient:      sshClient,
		sshSession:     sshSession,
		ioCancel:       make(chan bool),
		ioOnce:         sync.Once{},
		initialCommand: initialCommand,
		localTTY:       tty,
	}, nil
}

// stdinLoop handles forwarding stdin to remote
func (bridge *PTYSSHBridge) stdinLoop(stdinPipe io.WriteCloser) {
	defer bridge.stdinWg.Done()
	buf := make([]byte, 4096)
	for {
		select {
		case <-bridge.stopStdinCh:
			return
		default:
		}

		n, err := os.Stdin.Read(buf)
		if n > 0 {
			data := buf[:n]

			// Jangan echo ke local TTY di sini — biarkan remote PTY yang echo.
			// Hanya jalankan observer/matcher dan kirim ke remote stdin.
			if bridge.StdinObserver != nil {
				// copy data to avoid races
				d := make([]byte, len(data))
				copy(d, data)
				go bridge.StdinObserver(d)
			}
			if bridge.StdinCallback != nil && bridge.StdinMatcher != nil && bridge.StdinMatcher(data) {
				go bridge.StdinCallback(data)
			}
			if stdinPipe != nil {
				_, _ = stdinPipe.Write(data)
			}
		}
		if err != nil {
			return
		}
	}
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
		bridge.pty.Close()
		bridge.sshSession.Close()
		return fmt.Errorf("failed to request PTY: %v", err)
	}
	if isTTY {
		oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("failed to set raw mode: %v", err)
		}
		bridge.termOldState = oldState
	}
	stdinPipe, err := bridge.sshSession.StdinPipe()
	if err != nil {
		return err
	}
	bridge.stdinPipe = stdinPipe
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

	// start stdin goroutine
	bridge.stopStdinCh = make(chan struct{})
	bridge.stdinWg.Add(1)
	go bridge.stdinLoop(stdinPipe)

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
	return err
}

// Pause stops stdin/output
func (bridge *PTYSSHBridge) Pause() error {
	if bridge.stopStdinCh != nil {
		close(bridge.stopStdinCh)
		bridge.stdinWg.Wait()
		bridge.stopStdinCh = nil
	}

	bridge.outputMu.Lock()
	bridge.outputDisabled = true
	bridge.outputMu.Unlock()

	if bridge.termOldState != nil {
		term.Restore(int(os.Stdin.Fd()), bridge.termOldState)
		bridge.termOldState = nil
	}
	return nil
}

// Resume restarts stdin/output
func (bridge *PTYSSHBridge) Resume() error {
	bridge.outputMu.Lock()
	bridge.outputDisabled = false
	bridge.outputMu.Unlock()

	if term.IsTerminal(int(os.Stdin.Fd())) {
		oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("failed to set raw mode: %v", err)
		}
		bridge.termOldState = oldState
	}

	if bridge.stdinPipe != nil {
		bridge.stopStdinCh = make(chan struct{})
		bridge.stdinWg.Add(1)
		go bridge.stdinLoop(bridge.stdinPipe)
	}
	return nil
}

// Close closes bridge
func (bridge *PTYSSHBridge) Close() error {
	bridge.ioOnce.Do(func() {
		close(bridge.ioCancel)
	})
	if bridge.termOldState != nil {
		term.Restore(int(os.Stdin.Fd()), bridge.termOldState)
		bridge.termOldState = nil
	}
	if bridge.stopStdinCh != nil {
		close(bridge.stopStdinCh)
		bridge.stdinWg.Wait()
	}
	if bridge.localTTY != nil {
		bridge.localTTY.Close()
	}
	if bridge.sshSession != nil {
		bridge.sshSession.Close()
	}
	if bridge.pty != nil {
		bridge.pty.Close()
	}
	return nil
}
