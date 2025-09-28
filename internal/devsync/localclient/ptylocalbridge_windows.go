//go:build windows
// +build windows

package localclient

import (
	"context"
	"fmt"
	"io"
	"log"
	"make-sync/internal/pty"
	"make-sync/internal/util"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/term"
	// strings removed (unused)
)

// StartInteractiveShell starts an interactive shell using ConPTY. The callback
// parameter is provided for API compatibility; the bridge uses SetStdinCallback
// for stdin handling.
func (b *PTYLocalBridge) StartInteractiveShell() error {
	return b.StartInteractiveShellWithCommand(b.initialCommand)
}

// StartInteractiveShellWithCommand starts the provided command in a ConPTY and
// wires IO to the current process' stdout/stdin.
func (b *PTYLocalBridge) StartInteractiveShellWithCommand(command string) error {

	oldstate, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to snapshot terminal state: %v", err)
	}
	b.oldState = oldstate

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
	// try to set PTY size to match current terminal if possible
	cols, rows := 80, 24
	if w, h, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
		cols = w
		rows = h
		util.Default.Printf("DEBUG: local PTY start size width=%d height=%d\n", w, h)
		_ = b.localPTY.SetSize(h, w)
		// Re-apply size a couple of times after start to avoid race with ConPTY
		go func(rows, cols int) {
			for i := 0; i < 3; i++ {
				time.Sleep(50 * time.Millisecond)
				_ = b.localPTY.SetSize(rows, cols)
			}
		}(h, w)
	} else {
		// term.GetSize failed; try Win32 console query as a fallback
		if fw, fh, ferr := getTerminalSizeWindows(); ferr == nil {
			cols = fw
			rows = fh
			util.Default.Printf("DEBUG: fallback Windows PTY start size width=%d height=%d\n", fw, fh)
			_ = b.localPTY.SetSize(rows, cols)
			go func(rows, cols int) {
				for i := 0; i < 3; i++ {
					time.Sleep(50 * time.Millisecond)
					_ = b.localPTY.SetSize(rows, cols)
				}
			}(fh, fw)
		} else {
			util.Default.Printf("DEBUG: local PTY start size detection failed: %v (fallback err: %v)\n", err, ferr)
			// still apply a default so the ConPTY has a non-zero buffer
			_ = b.localPTY.SetSize(24, 80)
		}
	}
	// re-expose stdin writer
	if ptyIn := p.InPipe(); ptyIn != nil {
		b.SetStdinWriter(ptyIn)
	}

	ctx, cancel := context.WithCancel(context.Background())
	b.cancelFunc = cancel

	b.outPipe = p.OutPipe()

	b.ProcessPTYReadInput(ctx, cancel)

	// start a small resize watcher: poll terminal size and update PTY when it changes
	go func(ctx context.Context) {
		prevW, prevH := cols, rows
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
				if b.localPTY == nil {
					continue
				}
				if w, h, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
					if w != prevW || h != prevH {
						_ = b.localPTY.SetSize(h, w)
						util.Default.Printf("DEBUG: local PTY resize applied height=%d width=%d\n", h, w)
						prevW, prevH = w, h
					}
				}
			}
		}
	}(ctx)
	log.Println("Started local interactive shell in PTY, waiting for process to exit...")
	if err := p.Wait(); err != nil {
		fmt.Println("process exited:", err)
	}
	log.Println("Process exited, invoking exit listener...")
	b.exitListener()
	log.Println("Exit listener complete, closing bridge...")
	return nil
}

func (b *PTYLocalBridge) ProcessPTYReadInput(ctx context.Context, cancel context.CancelFunc) error {
	go func(ctx context.Context) {
		defer func() {
			log.Println("PTY output reader: exiting, cancelling context")
		}()
		out := b.outPipe
		fmt.Println("PTY output reader: started")
		buf := make([]byte, 4096)

		readFn := func() (int, error) {
			if out != nil {
				return out.Read(buf)
			}
			return b.localPTY.Read(buf)
		}

		for {
			select {
			case <-ctx.Done():
				// fmt.Println("PTY output reader: context done, exiting")
				return
			default:
				n, err := readFn()
				if err != nil {
					fmt.Println("PTY output reader: read error:", err)
				}
				if n > 0 {
					b.outputMu.Lock()
					disabled := b.outputDisabled
					b.outputMu.Unlock()
					if !disabled {
						_, _ = os.Stdout.Write(buf[:n])
					}
				}
				if err != nil {
					log.Println("PTY output reader: read error, cancelling context:", err)
					return
				}
			}
		}
	}(ctx)
	go func(ctx context.Context) {
		defer func() {
			log.Println("PTY stdin reader: exiting, cancelling context")
		}()
		in := os.Stdin
		buf := make([]byte, 1024)
		fnCallbackHit := func(s string) {
			if b.inputHitCodeListener != nil {
				b.inputHitCodeListener(s)
			}
		}
		throttledMyFunc := util.ThrottledFunction(300 * time.Millisecond)
		var carry []byte // carry-over bytes from previous read

		for {
			select {
			case <-ctx.Done():
				// fmt.Println("PTY stdin reader: context done, exiting")
				return
			default:
				n, _ := in.Read(buf)
				if n > 0 {
					// prepend carry-over bytes dari read sebelumnya
					data := append(carry, buf[:n]...)
					carry = carry[:0]
					// fmt.Println("PTY stdin reader: read :: ", data)

					if b.outputDisabled {
						continue
					}

					isMatch := false
					// parse Alt sequences
					results := util.ParseAltUniversal(data)
					for _, r := range results {
						if !r.IsAltOnly {
							throttledMyFunc(func() {
								fnCallbackHit(fmt.Sprintf("alt+%s", r.Key))
							})
							isMatch = true
							break
						}
					}
					if isMatch {
						continue
					}

					isMatch = false
					// Parse Alt versi linux
					// detect ESC + digit (Alt+1..Alt+9, Alt+0)
					for i := 0; i < n-1; i++ {
						if buf[i] == 0x1b { // ESC
							c := buf[i+1]
							if (c >= '1' && c <= '9') || c == '0' {
								digit := string([]byte{c})
								throttledMyFunc(func() {
									fnCallbackHit(fmt.Sprintf("alt+%s", digit))
								})
								isMatch = true
								i++
							}
							break
						}
					}

					if isMatch {
						continue
					}
					// forward ke PTY stdin writer
					if w := b.GetStdinWriter(); w != nil {
						_, _ = w.Write(data)
						// fmt.Println("ProcessPTYReadInput: wrote to PTY:", string(data))
						if b.inputListener != nil {
							b.inputListener(data)
						}
					}
				}
			}
		}
	}(ctx)

	return nil

}

// Close shuts down IO goroutines, closes the PTY, and invokes the exit listener.
func (b *PTYLocalBridge) Close() error {

	log.Println("PTYLocalBridge: closing")
	b.outputDisabled = true
	log.Println("PTYLocalBridge: output disabled")

	b.cancelFunc()
	log.Println("PTYLocalBridge: context cancelled")
	b.SetOnExitListener(nil)
	log.Println("PTYLocalBridge: exit listener cleared")
	b.SetOnInputHitCodeListener(nil)
	log.Println("PTYLocalBridge: input hit code listener cleared")
	b.SetOnInputListener(nil)
	log.Println("PTYLocalBridge: input listener cleared")

	if b.localPTY != nil {
		_ = b.localPTY.Close()
		log.Println("PTYLocalBridge: local PTY closed")
		b.localPTY = nil
	}

	log.Println("PTYLocalBridge: closed")
	// go func() {
	// 	time.Sleep(10 * time.Second)
	// 	os.Exit(1)
	// }()

	err := b.resetConsoleMode()
	if err != nil {
		log.Println("resetConsoleMode error:", err)
	}
	return nil
}

func (b *PTYLocalBridge) Pause() error {
	// Matikan output & cancel reader goroutine

	b.outputMu.Lock()
	b.outputDisabled = true
	b.outputMu.Unlock()
	b.localPTY.Pause()
	b.cancelFunc()

	fmt.Println("1.PTYLocalBridge: reader cancelled")

	// flush dan reset mode Windows console
	if err := b.resetConsoleMode(); err != nil {
		fmt.Println("resetConsoleMode error:", err)
	}

	fmt.Println("2.PTYLocalBridge: console restored + flushed")

	return nil
}

var (
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procFlushConsoleInputBuffer = kernel32.NewProc("FlushConsoleInputBuffer")
)

// resetConsoleMode mengembalikan mode console ke default dasar (non-raw)
func (b *PTYLocalBridge) resetConsoleMode() error {

	// currentState, err := term.GetState(int(os.Stdin.Fd()))
	// if err != nil {
	// 	return fmt.Errorf("failed to get terminal state: %v", err)
	// }

	// // term.Restore(int(os.Stdin.Fd()), b.oldState)

	// b.oldState = currentState

	// step 2: reset console mode
	handle := syscall.Handle(os.Stdin.Fd())
	const (
	// ENABLE_PROCESSED_INPUT = 0x0001
	// ENABLE_LINE_INPUT      = 0x0002
	// ENABLE_ECHO_INPUT      = 0x0004
	)
	// procSetConsoleMode.Call(uintptr(handle))

	// step 3: flush buffer biar gak ada "sampah" escape sequence
	procFlushConsoleInputBuffer.Call(uintptr(handle))

	// step 4: ANSI reset biar cursor & screen bersih
	fmt.Print("\033c")

	return nil
}

func (b *PTYLocalBridge) Resume() error {
	// util.Default.ClearScreen()

	err := term.Restore(int(os.Stdin.Fd()), b.oldState)
	if err != nil {
		return fmt.Errorf("failed to snapshot terminal state: %v", err)
	}

	b.outputMu.Lock()
	b.outputDisabled = false
	b.outputMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	b.cancelFunc = cancel

	b.ProcessPTYReadInput(ctx, cancel)

	fmt.Println("PTYLocalBridge: reader resumed")
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
	log.Println("SetOnExitListener: exit listener set")
	b.exitMu.Unlock()
}
