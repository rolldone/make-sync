package devsync

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"make-sync/internal/config"
)

// RunDevSync runs the devsync file watcher
func RunDevSync(cfg *config.Config) error {
	fmt.Println("🚀 Starting DevSync File Watcher")
	fmt.Println("=================================")

	// Execute on_ready script if configured
	if cfg.Devsync.Script.Local.OnReady != "" {
		fmt.Printf("🔧 Executing on_ready script: %s\n", cfg.Devsync.Script.Local.OnReady)
		// TODO: Implement script execution
	}

	// Create and start watcher
	watcher := NewWatcher(cfg)

	// Setup graceful shutdown
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	// Start watcher in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- watcher.Start()
	}()

	// Wait for shutdown signal or error
	select {
	case sig := <-signalChan:
		fmt.Printf("\n⚠️  Received signal: %v\n", sig)
		watcher.Stop()
	case err := <-errChan:
		if err != nil {
			return fmt.Errorf("watcher error: %v", err)
		}
	}

	// Execute on_stop script if configured
	if cfg.Devsync.Script.Local.OnStop != "" {
		fmt.Printf("🔧 Executing on_stop script: %s\n", cfg.Devsync.Script.Local.OnStop)
		// TODO: Implement script execution
	}

	fmt.Println("✅ DevSync stopped gracefully")
	return nil
}
