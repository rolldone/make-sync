//go:build windows
// +build windows

package localclient

import (
	"fmt"
	"io"
	"make-sync/internal/pty"
	"make-sync/internal/util"
	"os"
	"os/exec"
	"time"
)

// StartInteractiveShell starts an interactive shell using ConPTY. The callback
// parameter is provided for API compatibility; the bridge uses SetStdinCallback
// for stdin handling.
func (b *PTYLocalBridge) StartInteractiveShell(cb func([]byte)) error {
	if cb != nil {
		b.SetStdinCallback(cb)
	}
	return b.StartInteractiveShellWithCommand(b.initialCommand)
}

// StartInteractiveShellWithCommand starts the provided command in a ConPTY and
// wires IO to the current process' stdout/stdin.
func (b *PTYLocalBridge) StartInteractiveShellWithCommand(command string) error {
	if command == "" {
		// default to system shell
		shell := os.Getenv("COMSPEC")
		if shell == "" {
			shell = "cmd.exe"
		}
		command = shell
	}

	// Build exec.Cmd for the command. Use cmd.exe /C on Windows to execute shell commands.
	cmd := exec.Command("cmd.exe", "/C", command)

	p, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("failed to start local pty on Windows: %w", err)
	}

	// store PTY and expose writer so manager can forward stdin
	b.localPTY = p
	// Use the PTY interface methods directly
	b.SetStdinWriter(p.InPipe())

	// ioCancel and ioOnce are created by NewPTYLocalBridge; follow the same
	// channel/Once cancellation pattern used by other bridge implementations.

	// read output and write to stdout
	go func() {
		out := p.OutPipe()
		buf := make([]byte, 4096)
		for {
			n, err := out.Read(buf)
			if n > 0 {
				b.outputMu.Lock()
				disabled := b.outputDisabled
				b.outputMu.Unlock()
				if !disabled {
					_, _ = os.Stdout.Write(buf[:n])
				}
			}
			if err != nil {
				// signal goroutine cancellation like other implementations
				b.ioOnce.Do(func() { close(b.ioCancel) })
				// invoke exit listener
				b.exitMu.Lock()
				if b.exitListener != nil {
					go func(cb func()) {
						defer func() { _ = recover() }()
						cb()
					}(b.exitListener)
					b.exitListener = nil
				}
				b.exitMu.Unlock()
				return
			}
		}
	}()

	// Write initial command if provided.
	if b.initialCommand != "" {
		go func() {
			time.Sleep(50 * time.Millisecond)
			// use stored stdin writer if available
			b.mu.RLock()
			w := b.stdin
			b.mu.RUnlock()
			if w != nil {
				_, _ = io.WriteString(w, b.initialCommand)
				_, _ = io.WriteString(w, "\r")
			}
		}()
	}

	return nil
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
	b.outputMu.Lock()
	b.outputDisabled = false
	b.outputMu.Unlock()
	if util.IsEffectiveRaw() {
		// if TUI owns terminal we shouldn't change raw mode; otherwise enable raw
	} else if os.Getenv("TERM") != "" || true {
		// attempt to enable raw mode if possible
		restore, err := util.EnableRaw(int(os.Stdin.Fd()))
		if err == nil {
			b.termRestore = restore
		}
	}
	if b.localPTY != nil {
		// re-expose stdin writer
		b.SetStdinWriter(b.localPTY.InPipe())
	}
	return nil
}

// SetStdinWriter assigns the writer used to send data to the PTY's stdin.
func (b *PTYLocalBridge) SetStdinWriter(w io.WriteCloser) {
	b.mu.Lock()
	b.stdin = w
	b.mu.Unlock()
}

func (b *PTYLocalBridge) GetStdinWriter() io.WriteCloser {
	b.mu.RLock()
	w := b.stdin
	b.mu.RUnlock()
	return w
}

// stdin hook getters/setters
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

// SetOnExitListener registers a callback to be executed when the local
// interactive session exits.
func (b *PTYLocalBridge) SetOnExitListener(cb func()) {
	b.exitMu.Lock()
	b.exitListener = cb
	b.exitMu.Unlock()
}

// Close shuts down IO goroutines, closes the PTY, and invokes the exit listener.
func (b *PTYLocalBridge) Close() error {
	// cancel IO goroutines if configured
	b.ioOnce.Do(func() { close(b.ioCancel) })
	if b.termRestore != nil {
		_ = b.termRestore()
		b.termRestore = nil
	}
	if b.localPTY != nil {
		_ = b.localPTY.Close()
		b.localPTY = nil
	}

	if b.stdin != nil {
		_ = b.stdin.Close()
		b.stdin = nil
	}

	// invoke registered exit listener (at most once)
	b.exitMu.Lock()
	if b.exitListener != nil {
		go func(cb func()) {
			defer func() { _ = recover() }()
			cb()
		}(b.exitListener)
		b.exitListener = nil
	}
	b.exitMu.Unlock()
	return nil
}
