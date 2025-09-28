//go:build !windows
// +build !windows

package localclient

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"

	"golang.org/x/term"

	"make-sync/internal/pty"
	"make-sync/internal/util"
)

// StartInteractiveShell implements the Bridge interface. The local bridge does
// not require a callback argument for startup; the callback is applied via
// SetStdinCallback prior to StartInteractiveShell being invoked. For backward
// compatibility with other call-sites we accept the cb param and set it.
func (b *PTYLocalBridge) StartInteractiveShell() error {
	// default to launching an interactive shell with no initial command
	return b.startLocalWithCommand(b.initialCommand)
}

// StartInteractiveShell starts the provided command in a PTY and bridges IO to the terminal.
// startLocalWithCommand starts the provided command in a PTY and bridges IO to the terminal.
// This is the existing implementation that accepts a shell command string.
func (b *PTYLocalBridge) startLocalWithCommand(command string) error {
	// Detect whether stdin/stdout are real terminals. Mirror SSH bridge behavior.

	// prepare command
	cmd := exec.Command("/bin/sh", "-lc", command)
	// start in pty
	pt, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("failed to start local pty: %w", err)
	}
	b.localPTY = pt

	// set PTY size to match current terminal if possible
	if w, h, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
		_ = b.localPTY.SetSize(h, w)
	}

	// get file for writing (manager will forward stdin into this writer)
	f := b.localPTY.File()
	// expose PTY writer so PTYManager can forward stdin
	b.SetStdinWriter(f)

	ctx, cancel := context.WithCancel(context.Background())
	b.cancelFunc = cancel

	b.ProcessPTYReadInput(ctx, cancel)
	// Start a simple keyboard forwarder: read from os.Stdin and write into
	// the PTY file. If an inputListener is registered, notify it asynchronously
	// with a copy of the bytes read.

	err = cmd.Wait()
	// invoke exit listener if set
	b.exitListener()
	log.Println("Local command exited:", command, "err:", err)
	return err
}

func (b *PTYLocalBridge) ProcessPTYReadInput(ctx context.Context, cancel context.CancelFunc) error {
	// Goroutine stdin reader
	go func(ctx context.Context) {
		buf := make([]byte, 256)

		for {
			select {
			case <-ctx.Done():
				fmt.Println("Unix stdin reader: context done, exiting")
				return
			default:
				n, rerr := os.Stdin.Read(buf)
				if n > 0 {
					b.mu.RLock()
					w := b.stdin
					il := b.inputListener
					ih := b.inputHitCodeListener
					b.mu.RUnlock()

					// forward to PTY stdin writer
					if w != nil {
						// fmt.Println("Writing to PTY:", string(buf[:n]))
						_, werr := w.Write(buf[:n])
						if werr != nil {
							return
						}
					}

					// call inputListener asynchronously
					if il != nil {
						data := make([]byte, n)
						copy(data, buf[:n])
						go func(cb func([]byte), d []byte) {
							defer func() { _ = recover() }()
							cb(d)
						}(il, data)
					}

					// detect ESC + digit (Alt+1..Alt+9, Alt+0)
					if ih != nil {
						for i := 0; i < n-1; i++ {
							if buf[i] == 0x1b { // ESC
								c := buf[i+1]
								if (c >= '1' && c <= '9') || c == '0' {
									digit := string([]byte{c})
									go func(cb func(string), d string) {
										defer func() { _ = recover() }()
										cb("alt+" + d)
									}(ih, digit)
									i++
								}
							}
						}
					}
				}

				if rerr != nil {
					return
				}
			}
		}
	}(ctx)

	// Goroutine stdout/output reader
	go func(ctx context.Context) {
		buf := make([]byte, 4096)
		for {
			select {
			case <-ctx.Done():
				util.Default.ClearLine()
				util.Default.Println("Unix stdout reader: context done, exiting")
				return
			default:
				n, err := b.localPTY.Read(buf)
				if n > 0 {
					b.outputMu.Lock()
					disabled := b.outputDisabled
					b.outputMu.Unlock()
					if !disabled {
						_, _ = os.Stdout.Write(buf[:n])
					}
				}
				if err != nil {
					// b.ioOnce.Do(func() { close(b.ioCancel) })
					util.Default.Println("Unix stdout reader: pty read error:", err)
					log.Println("Unix stdout reader: pty read error:", err)
					return
				}
			}
		}
	}(ctx)

	return nil
}

func (b *PTYLocalBridge) Pause() error {
	b.outputMu.Lock()
	b.outputDisabled = true
	b.outputMu.Unlock()
	b.localPTY.Pause()
	b.cancelFunc()
	return nil
}

func (b *PTYLocalBridge) Resume() error {
	// resume input/output and restore raw mode if needed
	b.outputMu.Lock()
	b.outputDisabled = false
	b.outputMu.Unlock()

	if b.localPTY != nil {
		// try to update size on resume
		if w, h, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
			_ = b.localPTY.SetSize(h, w)
		}
		// ensure PTY writer is exposed (manager will forward stdin into this)
		b.SetStdinWriter(b.localPTY.File())
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancelFunc = cancel

	b.ProcessPTYReadInput(ctx, cancel)
	return nil
}

func (b *PTYLocalBridge) Close() error {
	// b.ioOnce.Do(func() { close(b.ioCancel) })

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
