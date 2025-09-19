//go:build !windows
// +build !windows

package pty

import (
	"os"
	"os/exec"

	creackpty "github.com/creack/pty"
)

// unixPTY wraps *os.File returned by creack/pty
type unixPTY struct {
	f   *os.File
	cmd *exec.Cmd
}

func Start(cmd *exec.Cmd) (PTY, error) {
	f, err := creackpty.Start(cmd)
	if err != nil {
		return nil, err
	}
	return &unixPTY{f: f, cmd: cmd}, nil
}

// ...existing code...
func Open() (PTY, *os.File, error) {
	// creackpty.Open returns (master, slave, err)
	master, slave, err := creackpty.Open()
	if err != nil {
		return nil, nil, err
	}
	// we only keep the master side in our wrapper; close the slave to avoid fd leak
	if slave != nil {
		_ = slave.Close()
	}
	return &unixPTY{f: master, cmd: nil}, master, nil
}

// ...existing code...

func (p *unixPTY) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *unixPTY) Write(b []byte) (int, error) { return p.f.Write(b) }
func (p *unixPTY) Close() error {
	err := p.f.Close()
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	return err
}
func (p *unixPTY) Fd() uintptr    { return p.f.Fd() }
func (p *unixPTY) File() *os.File { return p.f }
func (p *unixPTY) SetSize(rows, cols int) error {
	return creackpty.Setsize(p.f, &creackpty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}
