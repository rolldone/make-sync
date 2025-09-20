//go:build !windows
// +build !windows

package localclient

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"golang.org/x/term"

	"make-sync/internal/pty"
	"make-sync/internal/util"
)

// PTYLocalBridge is a simplified PTY bridge for running local commands in a PTY.
type PTYLocalBridge struct {
	localPTY    pty.PTY
	termRestore func() error
	ioCancel    chan bool
	ioOnce      sync.Once

	StdinMatcher   func([]byte) bool
	StdinCallback  func([]byte)
	StdinObserver  func([]byte)
	outputDisabled bool
	outputMu       sync.Mutex

	initialCommand string
	mu             sync.RWMutex
	stdin          io.WriteCloser // optional explicit writer (set to PTY file)
	// exit listener called when local PTY/process exits
	exitListener func()
	exitMu       sync.Mutex
}

func NewPTYLocalBridge(initialCommand string) (*PTYLocalBridge, error) {
	return &PTYLocalBridge{
		ioCancel:       make(chan bool),
		ioOnce:         sync.Once{},
		initialCommand: initialCommand,
	}, nil
}

// StartInteractiveShell starts the provided command in a PTY and bridges IO to the terminal.
// startLocalWithCommand starts the provided command in a PTY and bridges IO to the terminal.
// This is the existing implementation that accepts a shell command string.
func (b *PTYLocalBridge) startLocalWithCommand(command string) error {
	// Detect whether stdin/stdout are real terminals. Mirror SSH bridge behavior.
	isTTY := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))

	// prepare command
	cmd := exec.Command("/bin/sh", "-lc", command)
	// start in pty
	pt, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("failed to start local pty: %w", err)
	}
	b.localPTY = pt

	if isTTY {
		restore, err := util.EnableRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("failed to enable raw mode: %w", err)
		}
		b.termRestore = restore
	}

	// set PTY size to match current terminal if possible
	if w, h, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
		_ = b.localPTY.SetSize(h, w)
	}

	// get file for writing (manager will forward stdin into this writer)
	f := b.localPTY.File()
	// expose PTY writer so PTYManager can forward stdin
	b.SetStdinWriter(f)

	// handle output
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				b.outputMu.Lock()
				disabled := b.outputDisabled
				b.outputMu.Unlock()
				if !disabled {
					os.Stdout.Write(buf[:n])
				}
			}
			if err != nil {
				b.ioOnce.Do(func() { close(b.ioCancel) })
				return
			}
		}
	}()

	err = cmd.Wait()
	b.ioOnce.Do(func() { close(b.ioCancel) })
	// invoke exit listener if set
	b.exitMu.Lock()
	if b.exitListener != nil {
		go func(cb func()) {
			defer func() { _ = recover() }()
			cb()
		}(b.exitListener)
		b.exitListener = nil
	}
	b.exitMu.Unlock()
	return err
}

// StartInteractiveShell implements the Bridge interface. The local bridge does
// not require a callback argument for startup; the callback is applied via
// SetStdinCallback prior to StartInteractiveShell being invoked. For backward
// compatibility with other call-sites we accept the cb param and set it.
func (b *PTYLocalBridge) StartInteractiveShell(cb func([]byte)) error {
	if cb != nil {
		b.SetStdinCallback(cb)
	}
	// default to launching an interactive shell with no initial command
	return b.startLocalWithCommand(b.initialCommand)
}

func (b *PTYLocalBridge) Pause() error {
	b.outputMu.Lock()
	b.outputDisabled = true
	b.outputMu.Unlock()
	if b.termRestore != nil {
		_ = b.termRestore()
		b.termRestore = nil
	}
	return nil
}

func (b *PTYLocalBridge) Resume() error {
	// resume input/output and restore raw mode if needed
	b.outputMu.Lock()
	b.outputDisabled = false
	b.outputMu.Unlock()
	if term.IsTerminal(int(os.Stdin.Fd())) {
		restore, err := util.EnableRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("failed to set raw mode: %v", err)
		}
		b.termRestore = restore
	}
	if b.localPTY != nil {
		// try to update size on resume
		if w, h, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
			_ = b.localPTY.SetSize(h, w)
		}
		// ensure PTY writer is exposed (manager will forward stdin into this)
		b.SetStdinWriter(b.localPTY.File())
	}
	return nil
}

func (b *PTYLocalBridge) Close() error {
	b.ioOnce.Do(func() { close(b.ioCancel) })
	if b.termRestore != nil {
		_ = b.termRestore()
		b.termRestore = nil
	}
	if b.localPTY != nil {
		_ = b.localPTY.Close()
		b.localPTY = nil
	}
	return nil
}

// SetOnExitListener registers a callback to be executed when the local
// interactive session exits (process ends). The callback will be executed at
// most once.
func (b *PTYLocalBridge) SetOnExitListener(cb func()) {
	b.exitMu.Lock()
	b.exitListener = cb
	b.exitMu.Unlock()
}

// Thread-safe setters/getters
func (b *PTYLocalBridge) SetStdinMatcher(m func([]byte) bool) {
	b.mu.Lock()
	b.StdinMatcher = m
	b.mu.Unlock()
}
func (b *PTYLocalBridge) GetStdinMatcher() func([]byte) bool {
	b.mu.RLock()
	m := b.StdinMatcher
	b.mu.RUnlock()
	return m
}
func (b *PTYLocalBridge) SetStdinCallback(cb func([]byte)) {
	b.mu.Lock()
	b.StdinCallback = cb
	b.mu.Unlock()
}
func (b *PTYLocalBridge) GetStdinCallback() func([]byte) {
	b.mu.RLock()
	cb := b.StdinCallback
	b.mu.RUnlock()
	return cb
}
func (b *PTYLocalBridge) SetStdinObserver(o func([]byte)) {
	b.mu.Lock()
	b.StdinObserver = o
	b.mu.Unlock()
}
func (b *PTYLocalBridge) GetStdinObserver() func([]byte) {
	b.mu.RLock()
	o := b.StdinObserver
	b.mu.RUnlock()
	return o
}

func (b *PTYLocalBridge) SetStdinWriter(w io.WriteCloser) { b.mu.Lock(); b.stdin = w; b.mu.Unlock() }
func (b *PTYLocalBridge) GetStdinWriter() io.WriteCloser {
	b.mu.RLock()
	w := b.stdin
	b.mu.RUnlock()
	return w
}

// utilGetSize is a tiny helper to get terminal size using golang.org/x/term.
// (size helper removed; use util or term.GetSize directly where needed)
