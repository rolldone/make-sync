//go:build windows
// +build windows

package pty

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	cbconpty "github.com/charmbracelet/x/conpty"
)

// winConPTY wraps charmbracelet/x/conpty.ConPty to satisfy our PTY interface.
type winConPTY struct {
	c *cbconpty.ConPty
}

func Start(cmd *exec.Cmd) (PTY, error) {
	// Create a new ConPTY with default size and spawn the command attached to it.
	c, err := cbconpty.New(cbconpty.DefaultWidth, cbconpty.DefaultHeight, 0)
	if err != nil {
		return nil, fmt.Errorf("pty: failed to create ConPTY: %w", err)
	}

	// Spawn the process attached to the ConPTY
	// charmbracelet/x/conpty exposes Spawn which accepts program name and args.
	// Build args from cmd
	args := []string{cmd.Path}
	if cmd.Args != nil {
		args = cmd.Args
	}

	// Map basic fields from exec.Cmd to syscall.ProcAttr so environment and dir are preserved.
	attr := &syscall.ProcAttr{}
	if cmd.Env != nil {
		attr.Env = cmd.Env
	}
	if cmd.Dir != "" {
		attr.Dir = cmd.Dir
	}

	// If Stdin/Stdout/Stderr are files, propagate them so the spawned process
	// inherits the expected handles. syscall.ProcAttr.Files expects a slice of
	// three uintptr values on Windows.
	var files []uintptr
	if cmd.Stdin != nil {
		if f, ok := cmd.Stdin.(*os.File); ok {
			files = append(files, f.Fd())
		}
	}
	if cmd.Stdout != nil {
		if f, ok := cmd.Stdout.(*os.File); ok {
			files = append(files, f.Fd())
		}
	}
	if cmd.Stderr != nil {
		if f, ok := cmd.Stderr.(*os.File); ok {
			files = append(files, f.Fd())
		}
	}
	if len(files) > 0 {
		// Ensure slice has exactly 3 entries
		for len(files) < 3 {
			files = append(files, 0)
		}
		attr.Files = files
	}

	// Forward SysProcAttr if provided
	if cmd.SysProcAttr != nil {
		attr.Sys = cmd.SysProcAttr
	}

	pid, _, err := c.Spawn(cmd.Path, args, attr)
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("pty: failed to spawn process in ConPTY: %w", err)
	}
	_ = pid // pid is unused here

	return &winConPTY{c: c}, nil
}

func Open() (PTY, *os.File, error) {
	c, err := cbconpty.New(cbconpty.DefaultWidth, cbconpty.DefaultHeight, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("pty: failed to create ConPTY: %w", err)
	}
	// Return the wrapper and the output pipe as *os.File for reading
	return &winConPTY{c: c}, c.OutPipe(), nil
}

func (w *winConPTY) Read(b []byte) (int, error)   { return w.c.Read(b) }
func (w *winConPTY) Write(b []byte) (int, error)  { return w.c.Write(b) }
func (w *winConPTY) Close() error                 { return w.c.Close() }
func (w *winConPTY) Fd() uintptr                  { return w.c.Fd() }
func (w *winConPTY) File() *os.File               { return w.c.OutPipe() }
func (w *winConPTY) SetSize(rows, cols int) error { return w.c.Resize(cols, rows) }

// InPipe exposes the ConPty input pipe (write to this to send stdin to the child).
func (w *winConPTY) InPipe() *os.File {
	return w.c.InPipe()
}

// OutPipe exposes the ConPty output pipe (read from this to get child's stdout/stderr).
func (w *winConPTY) OutPipe() *os.File {
	return w.c.OutPipe()
}
