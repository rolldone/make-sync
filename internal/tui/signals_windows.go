//go:build windows
// +build windows

package tui

import "os"

// winchSignals empty on Windows (no SIGWINCH)
var winchSignals = []os.Signal{}
