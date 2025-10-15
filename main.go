package main

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"make-sync/cmd"
	"make-sync/internal/events"
	"make-sync/internal/util"

	"golang.org/x/term"
)

func main() {

	// Ensure .sync_temp/logs directory exists for logging
	if err := os.MkdirAll(".sync_temp/logs", 0755); err != nil {
		log.Fatalf("failed to create .sync_temp/logs directory: %v", err)
	}

	// Buka file untuk menulis log (append, create kalau belum ada)
	f, err := os.OpenFile(".sync_temp/logs/watcher.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("gagal buka file log: %v", err)
	}
	defer f.Close()

	// Arahkan standard logger ke file
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds) // contoh: timestamp

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

	var wg sync.WaitGroup
	done := make(chan struct{})
	shutdown := make(chan struct{})

	// Listen for shutdown events from components via EventBus
	events.GlobalBus.Subscribe(events.EventShutdownRequested, func(reason string) {
		log.Printf("shutdown requested from component: %s\n", reason)
		cancel() // signal all routines via context
		close(shutdown)
	})

	// Listen for cleanup events from components via EventBus
	events.GlobalBus.Subscribe(events.EventCleanupRequested, func() {
		log.Printf("cleanup requested from component\n")
		// Perform direct agent cleanup here without watcher dependency
		// This will be handled by main.go level cleanup logic
	})

	// Run the CLI in a goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = cmd.ExecuteContext(ctx)
		close(done)
	}()

waitLoop:
	for {
		select {
		case <-shutdown:
			// Component requested shutdown via EventBus
			select {
			case <-done:
				log.Println("goroutine exited cleanly after component shutdown")
				break waitLoop
			case <-time.After(5 * time.Second):
				log.Println("timeout waiting for goroutine after component shutdown, forcing exit")
				forceExit(1)
			}
		case <-done:
			// finished normally before any shutdown request
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
