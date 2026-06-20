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
	"strings"
	"time"

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

// startLocalWithCommand starts the provided command in a PTY and bridges IO to the terminal.
// This is the existing implementation that accepts a shell command string.
func (b *PTYLocalBridge) startLocalWithCommand(command string) error {
	// Detect whether stdin/stdout are real terminals. Mirror SSH bridge behavior.
	util.ResetRaw(b.oldState)
	oldState, err := util.NewRaw()
	if err != nil {
		return fmt.Errorf("failed to enable raw mode: %w", err)
	}
	b.oldState = oldState

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

	// Create separate contexts: output stays alive until session end; input can be paused
	outCtx, outCancel := context.WithCancel(context.Background())
	inCtx, inCancel := context.WithCancel(context.Background())
	b.outputCancel = outCancel
	b.inputCancel = inCancel

	b.ProcessPTYReadOutput(outCtx)
	b.ProcessPTYReadInput(inCtx)

	b.localPTY.Write([]byte("\n")) // ensure prompt starts on new line

	err = cmd.Wait()
	// invoke exit listener if set
	b.exitListener()
	log.Println("Local command exited:", command, "err:", err)
	return err
}

func (b *PTYLocalBridge) ProcessPTYReadInput(ctx context.Context) error {
	// Goroutine stdin reader with vim-style command mode (ESC : exit)
	go func(ctx context.Context) {
		buf := make([]byte, 256)
		throttledMyFunc := util.ThrottledFunction(300 * time.Millisecond)

		cmdState := 0 // 0=normal, 1=esc_seen, 2=command_mode
		var cmdBuf []byte

		for {
			select {
			case <-ctx.Done():
				log.Println("ProcessPTYReadInput :: Unix stdin reader: context done, exiting")
				return
			default:
				n, rerr := os.Stdin.Read(buf)
				if n > 0 {
					b.mu.RLock()
					w := b.stdin
					il := b.inputListener
					ih := b.inputHitCodeListener
					b.mu.RUnlock()

					if il != nil {
						data := make([]byte, n)
						copy(data, buf[:n])
						go func(cb func([]byte), d []byte) {
							defer func() { _ = recover() }()
							cb(d)
						}(il, data)
					}

					for i := 0; i < n; i++ {
						bt := buf[i]

						switch cmdState {
						case 0: // normal mode
							if bt == 0x1b { // ESC
								cmdState = 1
								continue
							}
							if bt == 0x10 && ih != nil { // Ctrl+0 fallback
								log.Println("PTYLocalBridge: Ctrl+0 detected, force close")
								go func(cb func(string)) {
									defer func() { _ = recover() }()
									cb("alt+0")
								}(ih)
								continue
							}
							if w != nil {
								w.Write([]byte{bt})
							}
							cmdState = 0

						case 1: // just saw ESC
							if bt == ':' {
								cmdState = 2
								cmdBuf = nil
								log.Println("PTYLocalBridge: ESC+: detected, entering command mode")
								continue
							}
							if (bt >= '1' && bt <= '9') || bt == '0' {
								digit := string([]byte{bt})
								log.Println("PTYLocalBridge: ESC+digit shortcut: alt+" + digit)
								if ih != nil {
									if bt == '0' {
										go func(cb func(string), d string) {
											defer func() { _ = recover() }()
											cb("alt+" + d)
										}(ih, digit)
									} else {
										go func(cb func(string), d string) {
											throttledMyFunc(func() {
												defer func() { _ = recover() }()
												cb("alt+" + d)
											})
										}(ih, digit)
									}
								}
								cmdState = 0
								continue
							}
							// Not a shortcut: forward ESC + this byte
							if w != nil {
								w.Write([]byte{0x1b, bt})
							}
							cmdState = 0

						case 2: // command mode
							if bt == '\r' || bt == '\n' {
								cmd := strings.TrimSpace(string(cmdBuf))
								log.Printf("PTYLocalBridge: command mode: got '%s'", cmd)
								if cmd == "exit" || cmd == "q" || cmd == "quit" {
									log.Println("PTYLocalBridge: exit command, force close")
									if ih != nil {
										go func(cb func(string)) {
											defer func() { _ = recover() }()
											cb("alt+0")
										}(ih)
									}
								} else {
									// Forward whole sequence to PTY
									if w != nil {
										w.Write([]byte{0x1b, ':'})
										w.Write(cmdBuf)
										w.Write([]byte{bt})
									}
								}
								cmdBuf = nil
								cmdState = 0
								continue
							}
							if bt == 0x7f || bt == 0x08 {
								if len(cmdBuf) > 0 {
									cmdBuf = cmdBuf[:len(cmdBuf)-1]
								}
								continue
							}
							if bt >= 0x20 && bt <= 0x7e {
								cmdBuf = append(cmdBuf, bt)
							}
							continue
						}
					}
				}

				if rerr != nil {
					return
				}
			}
		}
	}(ctx)

	return nil
}

func (b *PTYLocalBridge) ProcessPTYReadOutput(ctx context.Context) error {
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
					tap := b.outputTap
					b.outputMu.Unlock()
					if !disabled {
						_, _ = os.Stdout.Write(buf[:n])
					}
					// Always cache output as history buffer
					b.cacheOutput(buf[:n])
					if tap != nil {
						// Local PTY only provides a single output stream; mark as stdout (isErr=false)
						// Invoke regardless of outputDisabled to keep background logging.
						data := make([]byte, n)
						copy(data, buf[:n])
						go func(cb func([]byte, bool), d []byte) {
							defer func() { _ = recover() }()
							cb(d, false)
						}(tap, data)
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

	b.localPTY.Write([]byte("\x1b")) // ensure prompt ends cleanly
	b.localPTY.Write([]byte("\x08")) // send backspace to clear any partial input

	util.ResetRaw(b.oldState)

	b.outputMu.Lock()
	b.outputDisabled = true
	b.outputMu.Unlock()
	b.localPTY.Pause()
	if b.inputCancel != nil {
		b.inputCancel()
	}

	return nil
}

func (b *PTYLocalBridge) Resume() error {

	util.ResetRaw(b.oldState)
	oldstate, err := util.NewRaw()
	if err != nil {
		return fmt.Errorf("failed to enable raw mode: %w", err)
	}
	b.oldState = oldstate

	// load cached output first
	b.cacheMu.Lock()
	earlyTotal := len(b.outputCache)
	if earlyTotal > 0 {
		fmt.Print(string(b.outputCache))
	}
	b.cacheMu.Unlock()

	// resume input/output and restore raw mode if needed
	b.outputMu.Lock()
	b.outputDisabled = false
	b.outputMu.Unlock()

	// restart input reader
	inCtx, inCancel := context.WithCancel(context.Background())
	b.inputCancel = inCancel
	b.ProcessPTYReadInput(inCtx)

	if b.localPTY != nil {
		// try to update size on resume
		if w, h, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
			_ = b.localPTY.SetSize(h, w)
		}
	}

	b.localPTY.Write([]byte("\x1b")) // ensure prompt ends cleanly
	b.localPTY.Write([]byte("\x08")) // send backspace to clear any partial input
	// b.outputCache = nil

	return nil
}

func (b *PTYLocalBridge) Close() error {
	// b.ioOnce.Do(func() { close(b.ioCancel) })

	b.localPTY.Write([]byte("\x1b\x08")) // ensure prompt ends cleanly and clear any partial input

	b.cacheMu.Lock()
	b.outputCache = nil
	b.cacheMu.Unlock()

	if b.localPTY != nil {
		_ = b.localPTY.Close()
		b.localPTY = nil
	}
	util.ResetRaw(b.oldState)

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
