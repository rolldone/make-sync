package main

import (
	"os"
	"os/signal"
	"syscall"

	"make-sync/cmd"
)

func main() {
	// Setup signal handler for graceful shutdown (Ctrl+C)
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)

	// Run the CLI in a goroutine so we can listen for signals concurrently
	go func() {
		cmd.Execute()
	}()

	// Wait for a termination signal
	<-sigs
}
