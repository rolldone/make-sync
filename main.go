package main

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"make-sync/cmd"
	"make-sync/internal/config"
	"make-sync/internal/events"
	"make-sync/internal/util"

	"strings"

	gspt "github.com/erikdubbelboer/gspt"

	"golang.org/x/term"
)

// truncateToBytes truncates s to at most max bytes without splitting UTF-8 runes.
func truncateToBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	var b []byte
	for _, r := range s {
		rb := []byte(string(r))
		if len(b)+len(rb) > max {
			break
		}
		b = append(b, rb...)
	}
	if len(b) == 0 {
		return s[:max]
	}
	return string(b)
}

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

	// Determine process title preference order:
	// 1) project_name in make-sync.yaml via internal/config
	// 2) PROC_TITLE env var
	// 3) default "make-sync"
	var procTitle string
	if cfg, err := config.LoadAndValidateConfig(); err == nil && cfg.ProjectName != "" {
		procTitle = cfg.ProjectName
	} else if t := os.Getenv("PROC_TITLE"); t != "" {
		procTitle = t
	} else {
		procTitle = "make-sync"
	}
	// Replace whitespace with single '-' (collapse multiple spaces/tabs) to make
	// the process name friendlier and avoid spaces in ps output.
	procTitle = strings.Join(strings.Fields(procTitle), "-")
	// PR_SET_NAME (Linux comm) limited to 16 bytes including NUL, so keep ~15 bytes
	procTitle = truncateToBytes(procTitle, 15)
	gspt.SetProcTitle(procTitle)

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
