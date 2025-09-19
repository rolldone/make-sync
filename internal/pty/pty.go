package pty

import (
	"os"
)

// PTY is a small, cross-platform abstraction over a pseudo-terminal.
type PTY interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Close() error
	Fd() uintptr
	File() *os.File
	SetSize(rows, cols int) error
}
