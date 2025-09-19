//go:build !windows
// +build !windows

package tui

import (
	"os"
	"syscall"
)

// winchSignals holds platform-specific SIGWINCH equivalent (non-windows)
var winchSignals = []os.Signal{syscall.SIGWINCH}
