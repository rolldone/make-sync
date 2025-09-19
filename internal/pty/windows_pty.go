//go:build windows
// +build windows

package pty

import (
	"errors"
	"os"
	"os/exec"
)

// windowsPTY is a minimal stub. Replace with a real ConPTY/winpty implementation.
type windowsPTY struct {
	f *os.File
}

func Start(cmd *exec.Cmd) (PTY, error) {
	// TODO: implement using ConPTY (recommended) or winpty wrapper.
	return nil, errors.New("pty: Start not implemented on windows; implement using ConPTY/winpty")
}

func Open() (PTY, *os.File, error) {
	// TODO: implement opening a pty pair on Windows (ConPTY).
	return nil, nil, errors.New("pty: Open not implemented on windows; implement using ConPTY/winpty")
}

func (p *windowsPTY) Read(b []byte) (int, error)  { return 0, errors.New("not implemented") }
func (p *windowsPTY) Write(b []byte) (int, error) { return 0, errors.New("not implemented") }
func (p *windowsPTY) Close() error                { return nil }
func (p *windowsPTY) Fd() uintptr                 { return uintptr(0) }
func (p *windowsPTY) File() *os.File              { return p.f }
func (p *windowsPTY) SetSize(rows, cols int) error {
	return errors.New("pty: SetSize not implemented on windows")
}
