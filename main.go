package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"make-sync/cmd"
	"make-sync/internal/util"

	"golang.org/x/term"
)

func main() {
	// Capture original terminal state (if stdin is a TTY) so we can restore on forced exit.
	var origState *term.State
	if fi, _ := os.Stdin.Stat(); (fi.Mode() & os.ModeCharDevice) != 0 {
		if st, err := term.GetState(int(os.Stdin.Fd())); err == nil {
			origState = st
		}
	}

	forceExit := func(code int) {
		if origState != nil {
			_ = term.Restore(int(os.Stdin.Fd()), origState)
		}
		os.Exit(code)
	}

	// Context used to issue graceful cancellation to command tree.
	ctx, cancel := context.WithCancel(context.Background())

	// Setup signal handler for graceful + forced shutdown. Buffer 2 to catch quick double Ctrl+C.
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Run the CLI in a goroutine so we can listen for signals concurrently
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = cmd.ExecuteContext(ctx)
		close(done)
	}()

	var first int32 // 0 = not received, 1 = received first Ctrl+C

waitLoop:
	for {
		select {
		case sig := <-sigs:
			if sig == os.Interrupt || sig == syscall.SIGTERM {
				if atomic.CompareAndSwapInt32(&first, 0, 1) {
					log.Println("interrupt received (Ctrl+C). Attempting graceful shutdown... (press Ctrl+C again to force)")
					cancel() // signal all routines via context
					// wait for goroutine to finish, but force exit after timeout
					select {
					case <-done:
						log.Println("goroutine exited cleanly")
						break waitLoop
					case sig2 := <-sigs:
						// second signal while waiting => force
						log.Printf("second signal (%v) received -> force exit\n", sig2)
						forceExit(130) // 130 = terminated by Ctrl+C convention
					case <-time.After(5 * time.Second):
						log.Println("timeout waiting for goroutine, forcing exit")
						forceExit(1)
					}
				} else {
					log.Println("second Ctrl+C -> immediate force exit")
					forceExit(130)
				}
			}
		case <-done:
			// finished normally before any signal
			log.Println("goroutine finished; exiting.")
			util.Default.ClearLine()
			break waitLoop
		}
	}

	// ensure wg cleaned up (optional)
	wg.Wait()

	// Restore terminal before normal exit if it was changed (best-effort)
	if origState != nil {
		_ = term.Restore(int(os.Stdin.Fd()), origState)
	}
}
