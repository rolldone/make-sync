package sshclient

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"make-sync/internal/pty"
	"make-sync/internal/util"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// PTYSSHBridge represents a bridge between PTY and SSH session for interactive sessions
type PTYSSHBridge struct {
	localPTY   pty.PTY
	sshClient  *SSHClient
	sshSession *ssh.Session
	// ioCancel   chan bool
	ioOnce sync.Once

	initialCommand string
	postCommand    string

	StdinMatcher   func([]byte) bool
	StdinCallback  func([]byte)
	StdinObserver  func([]byte)
	outputDisabled bool
	outputMu       sync.Mutex

	localTTY *os.File

	// stdin control
	// stopStdinCh chan struct{}

	stdinPipe io.WriteCloser

	mu    sync.RWMutex // protect concurrent access to shared fields below
	stdin io.WriteCloser
	// exit listener called when the interactive session exits
	exitListener         func()
	inputListener        func([]byte)
	inputHitCodeListener func(string)
	exitMu               sync.Mutex
	// inputBuf is a small bounded channel used to queue stdin fragments
	// delivered to this bridge. The drainer goroutine writes queued data
	// into the bridge's stdin writer and notifies the inputListener.
	inputBuf chan []byte

	cancelFunc context.CancelFunc

	oldState   *term.State
	stdoutPipe io.Reader
	stderrPipe io.Reader
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
		// ioCancel:   make(chan bool),
		ioOnce: sync.Once{},
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
		localPTY:   ptWrapper,
		localTTY:   ptFile,
		sshClient:  sshClient,
		sshSession: sshSession,
		// ioCancel:       make(chan bool),
		ioOnce:         sync.Once{},
		initialCommand: initialCommand,
	}, nil
}

// NewPTYSSHBridgeWithCommandAndPost creates a bridge with both initial shell command and post command
func NewPTYSSHBridgeWithCommandAndPost(sshClient *SSHClient, initialCommand string, postCommand string) (*PTYSSHBridge, error) {
	bridge, err := NewPTYSSHBridgeWithCommand(sshClient, initialCommand)
	if err != nil {
		return nil, err
	}
	bridge.postCommand = postCommand
	return bridge, nil
}

// StartInteractiveShell starts an interactive shell session
func (bridge *PTYSSHBridge) StartInteractiveShell() error {

	// Best-effort: set stdin into raw mode for interactive sessions and keep
	// the restore function so Pause/Resume/Close can restore it.
	oldstate, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to snapshot terminal state: %v", err)
	}
	bridge.oldState = oldstate

	cols, rows := 80, 24

	if err := bridge.sshSession.RequestPty("xterm-256color", rows, cols, ssh.TerminalModes{}); err != nil {
		if bridge.localPTY != nil {
			_ = bridge.localPTY.Close()
		}
		bridge.sshSession.Close()
		return fmt.Errorf("failed to request PTY: %v", err)
	}

	stdoutPipe, err := bridge.sshSession.StdoutPipe()
	if err != nil {
		fmt.Print("failed to get stdout pipe: ", err)
		os.Exit(1)
		return err
	}
	bridge.stdoutPipe = stdoutPipe
	stderrPipe, err := bridge.sshSession.StderrPipe()
	if err != nil {
		fmt.Print("failed to get stdout pipe: ", err)
		os.Exit(1)
		return err
	}
	bridge.stderrPipe = stderrPipe

	ctx, cancel := context.WithCancel(context.Background())
	bridge.cancelFunc = cancel
	log.Println("StartInteractiveShell : Started interactive shell session")
	bridge.ProcessPTYReadInput(ctx, cancel)

	// Start a small resize watcher that polls the terminal size and applies
	// WindowChange on the remote session when it changes. This is a simple
	// fallback for environments that don't send SIGWINCH or when the TUI
	// framework doesn't propagate resize events.
	go func(ctx context.Context) {
		prevW, prevH := rows, cols
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
				if bridge.sshSession == nil {
					continue
				}
				if w, h, err := getTerminalSizeFallback(); err == nil {
					if w != prevW || h != prevH {
						_ = bridge.sshSession.WindowChange(h, w)
						log.Printf("DEBUG: Resize watcher WindowChange applied height=%d width=%d\n", h, w)
						prevW, prevH = w, h
					}
				}
			}
		}
	}(ctx)

	stdinPipe, err := bridge.sshSession.StdinPipe()
	if err != nil {
		return err
	}
	bridge.stdinPipe = stdinPipe
	// expose stdin writer so PTYManager can forward stdin bytes into the session
	bridge.SetStdinWriter(stdinPipe)

	bridge.sshSession.Setenv("TERM", "xterm-256color")

	log.Println("DEBUG: Starting interactive shell with initialCommand:", bridge.initialCommand)

	// Smart detection: if initialCommand contains both shell + user command,
	// split them to avoid double execution. Look for patterns like:
	// "cmd.exe /K cd /d \"path\" & ping google" -> shell="cmd.exe /K cd /d \"path\"", cmd="ping google"
	ic := strings.TrimSpace(bridge.initialCommand)
	var shellCmd, userCmd string

	if strings.Contains(ic, "cmd.exe /K") && strings.Contains(ic, " & ") {
		// Windows pattern: "cmd.exe /K cd /d \"path\" & command"
		parts := strings.Split(ic, " & ")
		if len(parts) >= 2 {
			shellCmd = strings.TrimSpace(parts[0])                      // "cmd.exe /K cd /d \"path\""
			userCmd = strings.TrimSpace(strings.Join(parts[1:], " & ")) // "command"
			log.Printf("DEBUG: Detected Windows shell+cmd: shell=%q cmd=%q\n", shellCmd, userCmd)
		} else {
			shellCmd = ic
		}
	} else if strings.Contains(ic, "bash -c") && strings.Contains(ic, " ; exec bash") {
		// Unix pattern: "mkdir -p /path && cd /path && bash -c 'command' ; exec bash"
		if idx := strings.Index(ic, "bash -c "); idx != -1 {
			beforeBash := ic[:idx] + "bash -l" // "mkdir -p /path && cd /path && bash -l"
			shellCmd = beforeBash
			// Extract command from bash -c '...'
			remaining := ic[idx+8:] // after "bash -c "
			if idx2 := strings.Index(remaining, " ; exec bash"); idx2 != -1 {
				cmdPart := remaining[:idx2]
				cmdPart = strings.Trim(cmdPart, "'\"") // remove quotes
				userCmd = strings.TrimSpace(cmdPart)
				log.Printf("DEBUG: Detected Unix shell+cmd: shell=%q cmd=%q\n", shellCmd, userCmd)
			}
		}
		if shellCmd == "" {
			shellCmd = ic
		}
	} else {
		shellCmd = ic
	}

	// If we have both shell and user command, use two-step approach
	if userCmd != "" {
		// Two-step: start shell, then send user command
		log.Printf("DEBUG: Two-step execution: starting shell=%q\n", shellCmd)
		if err := bridge.sshSession.Start(shellCmd); err != nil {
			return err
		}
		if bridge.stdinPipe != nil {
			go func(cmd string, w io.WriteCloser) {
				// longer delay for shell to fully initialize
				time.Sleep(300 * time.Millisecond)
				// util.Default.ClearLine()
				// util.Default.Printf("DEBUG: Sending user command=%q\n", cmd)
				// util.Default.ClearLine()
				_, _ = w.Write([]byte(cmd + "\r\n"))
			}(userCmd, bridge.stdinPipe)
		}
	} else {
		// Single command: determine if it's a shell starter or regular command
		startsShell := false
		if ic != "" {
			low := strings.ToLower(ic)
			if strings.HasPrefix(low, "cmd.exe") || strings.Contains(low, "/k") || strings.Contains(low, "-noexit") || strings.Contains(low, "-c") {
				startsShell = true
			}
		}

		if ic != "" && startsShell {
			// Start shell directly
			util.Default.Printf("DEBUG: Starting shell directly: %q\n", ic)
			if err := bridge.sshSession.Start(bridge.initialCommand); err != nil {
				return err
			}
		} else {
			// Legacy mode: start shell and write command to stdin
			util.Default.Printf("DEBUG: Legacy mode: Shell() + stdin write: %q\n", ic)
			if err := bridge.sshSession.Shell(); err != nil {
				return err
			}
			if bridge.initialCommand != "" && bridge.stdinPipe != nil {
				go func(cmd string, w io.WriteCloser) {
					time.Sleep(150 * time.Millisecond)
					_, _ = w.Write([]byte(cmd + "\r\n"))
				}(bridge.initialCommand, bridge.stdinPipe)
			}
		}
	}

	// Note: the bridge no longer starts a stdin-reading goroutine. The PTYManager
	// is responsible for reading os.Stdin and forwarding bytes into the bridge's
	// stdin writer. This avoids multiple readers on os.Stdin and centralizes
	// shortcut handling.
	util.Default.ClearLine()
	util.Default.Print("2982394 : Started interactive shell session")
	util.Default.ClearLine()
	err = bridge.sshSession.Wait()
	if err != nil {
		if err == io.EOF {
			// normal exit
		} else {
			util.Default.ClearLine()
			util.Default.Print("ssh session wait exited with err:", err)
			util.Default.ClearLine()
		}
	}

	// Notify registered exit listener (if any). Protect invocation with mutex
	util.Default.Print("PTYSSHBridge: invoking exit listener")
	util.Default.ClearLine()
	bridge.exitListener()
	log.Println("PTYSSHBridge : SetOnExitListener exit listener done")
	return nil
}

func (bridge *PTYSSHBridge) ProcessPTYReadInput(ctx context.Context, cancel context.CancelFunc) error {
	stdoutPipe := bridge.stdoutPipe
	stderrPipe := bridge.stderrPipe
	// stdin reader
	go func(ctx context.Context) {
		buf := make([]byte, 256)
		util.Default.ClearLine()
		for {
			select {
			case <-ctx.Done():
				// fmt.Println("PTYSSHBridge stdin reader: context done, exiting")
				log.Println("PTYSSHBridge stdin reader: context done, exiting")
				return
			default:
				if bridge.outputDisabled {
					return
				}
				n, rerr := os.Stdin.Read(buf)
				if n > 0 {

					// call inputListener asynchronously
					bridge.mu.RLock()
					il := bridge.inputListener
					ih := bridge.inputHitCodeListener
					bridge.mu.RUnlock()

					if il != nil {
						data := make([]byte, n)
						copy(data, buf[:n])
						il(data)
					}
					isHit := false
					// Scan for ESC + digit (Alt+1..Alt+9 and Alt+0)
					if ih != nil {
						for i := 0; i < n-1; i++ {
							if buf[i] == 0x1b { // ESC
								c := buf[i+1]
								if (c >= '1' && c <= '9') || c == '0' {
									digit := string([]byte{c})
									ih("alt+" + digit)
									isHit = true
									i++
								}
							}
						}
					}
					if isHit {
						continue
					}
					w := bridge.GetStdinWriter()
					if w != nil {
						if _, werr := w.Write(buf[:n]); werr != nil {
							return
						}
					}
				}

				if rerr != nil {
					return
				}
			}
		}
	}(ctx)

	// stdout reader
	go func(ctx context.Context) {
		buf := make([]byte, 4096)
		util.Default.ClearLine()
		// Set pending output delay to allow terminal to initialize
		time.Sleep(2 * time.Second)
		for {
			select {
			case <-ctx.Done():
				// fmt.Println("PTYSSHBridge stdout reader: context done, exiting")
				log.Println("PTYSSHBridge stdout reader: context done, exiting")
				return
			default:
				if bridge.outputDisabled {
					return
				}
				n, err := stdoutPipe.Read(buf)
				if n > 0 {
					bridge.outputMu.Lock()
					disabled := bridge.outputDisabled
					bridge.outputMu.Unlock()
					if !disabled {
						_, _ = os.Stdout.Write(buf[:n])
					}
				}
				if err != nil {
					util.Default.ClearLine()
					util.Default.Print("PTYSSHBridge stdout reader error:", err)
					util.Default.ClearLine()
					// bridge.ioOnce.Do(func() { close(bridge.ioCancel) })
					return
				}
			}
		}
	}(ctx)

	// stderr reader
	go func(ctx context.Context) {
		buf := make([]byte, 4096)
		// Set pending output delay to allow terminal to initialize
		time.Sleep(2 * time.Second)
		for {
			select {
			case <-ctx.Done():
				// fmt.Println("PTYSSHBridge stderr reader: context done, exiting")
				log.Println("PTYSSHBridge stderr reader: context done, exiting")
				return
			default:
				if bridge.outputDisabled {
					return
				}
				n, err := stderrPipe.Read(buf)
				if n > 0 {
					bridge.outputMu.Lock()
					disabled := bridge.outputDisabled
					bridge.outputMu.Unlock()
					if !disabled {
						_, _ = os.Stderr.Write(buf[:n])
					}
				}
				if err != nil {
					util.Default.ClearLine()
					util.Default.Print("PTYSSHBridge stderr reader error:", err)
					// bridge.ioOnce.Do(func() { close(bridge.ioCancel) })
					return
				}
			}
		}
	}(ctx)

	return nil
}

// Pause stops stdin/output
func (bridge *PTYSSHBridge) Pause() error {

	bridge.outputMu.Lock()
	bridge.outputDisabled = true
	bridge.outputMu.Unlock()
	bridge.cancelFunc()

	term.Restore(int(os.Stdin.Fd()), bridge.oldState)

	fmt.Print("\033c")

	return nil
}

// Resume restarts stdin/output
func (bridge *PTYSSHBridge) Resume() error {

	bridge.outputMu.Lock()
	bridge.outputDisabled = false
	bridge.outputMu.Unlock()

	oldstate, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to snapshot terminal state: %v", err)
	}
	bridge.oldState = oldstate

	ctx, cancel := context.WithCancel(context.Background())
	bridge.cancelFunc = cancel

	bridge.ProcessPTYReadInput(ctx, cancel)

	return nil
}

// Close closes bridge
func (bridge *PTYSSHBridge) Close() error {
	bridge.outputDisabled = true
	log.Println("PTYSSHBridge : Close called, output disabled")

	log.Println("PTYSSHBridge : SetOnExitListener exit listener called")
	if bridge.localTTY != nil {
		bridge.localTTY.Close()
		log.Println("PTYSSHBridge : localTTY closed")
	}

	if bridge.sshSession != nil {
		bridge.sshSession.Close()
		log.Println("PTYSSHBridge : sshSession closed")
	}
	if bridge.localPTY != nil {
		_ = bridge.localPTY.Close()
		log.Println("PTYSSHBridge : localPTY closed")
		bridge.localPTY = nil
	}

	bridge.cancelFunc()
	log.Println("PTYSSHBridge : context cancelled")

	// We dont need to clear these, PTYManager will discard the bridge reference
	// bridge.SetOnExitListener(nil)
	log.Println("PTYSSHBridge : Exit listener cleared")
	// bridge.SetOnInputHitCodeListener(nil)
	log.Println("PTYSSHBridge : InputHitCodeListener cleared")
	/// bridge.SetOnInputListener(nil)
	log.Println("PTYSSHBridge : InputListener cleared")
	log.Println("PTYSSHBridge : Bridge closed")

	return nil
}

// SetOnExitListener registers a listener function invoked when the bridge exits
func (bridge *PTYSSHBridge) SetOnExitListener(fn func()) {
	bridge.exitMu.Lock()
	defer bridge.exitMu.Unlock()
	bridge.exitListener = fn
}

// SetOnInputListener registers a listener for input bytes
func (bridge *PTYSSHBridge) SetOnInputListener(fn func([]byte)) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.inputListener = fn
}

// SetOnInputHitCodeListener registers a listener for special hit codes
func (bridge *PTYSSHBridge) SetOnInputHitCodeListener(fn func(string)) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.inputHitCodeListener = fn
}

// SetStdinWriter sets the stdin writer for the bridge
func (bridge *PTYSSHBridge) SetStdinWriter(w io.WriteCloser) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.stdin = w
}

// GetStdinWriter returns the stdin writer
func (bridge *PTYSSHBridge) GetStdinWriter() io.WriteCloser {
	bridge.mu.RLock()
	defer bridge.mu.RUnlock()
	return bridge.stdin
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
