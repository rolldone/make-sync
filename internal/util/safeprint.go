package util

import (
	"fmt"
	"strings"
	"sync"
)

type SafePrinter struct {
	mu        sync.Mutex
	suspended bool
}

// Default is the shared SafePrinter used across the application to
// ensure all packages serialize their output to the terminal and avoid
// interleaving between goroutines.
var Default = &SafePrinter{}

func (s *SafePrinter) Print(a ...interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.suspended {
		return
	}
	fmt.Print(a...)
}

func (s *SafePrinter) Printf(format string, a ...interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.suspended {
		return
	}
	fmt.Printf(format, a...)
}

func (s *SafePrinter) Println(a ...interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.suspended {
		return
	}
	fmt.Println(a...)
}

// Add clear screen
func (s *SafePrinter) ClearScreen() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.suspended {
		return
	}
	fmt.Print("\x1b[2J\x1b[H")
}

// PrintBlock prints a potentially multi-line block atomically. If clearLine is true
// it will first clear the current line (useful to overwrite a status line) and then
// print the block exactly as provided.
func (s *SafePrinter) PrintBlock(block string, clearLine bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.suspended {
		return
	}
	if clearLine {
		fmt.Print("\r\x1b[K")
	}
	fmt.Print(block)
	// Ensure the block ends with a newline
	if !strings.HasSuffix(block, "\n") {
		fmt.Print("\n")
	}
}

// ClearLine clears the current line and returns the cursor to the beginning.
func (s *SafePrinter) ClearLine() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.suspended {
		return
	}
	fmt.Print("\r\x1b[K")
}

// Suspend silences all subsequent prints until Resume is called.
// Useful to temporarily hide status messages while interactive prompts
// take over the terminal.
func (s *SafePrinter) Suspend() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.suspended = true
}

// Resume re-enables printing after Suspend.
func (s *SafePrinter) Resume() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.suspended = false
}

func (s *SafePrinter) IsSuspended() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.suspended
}
