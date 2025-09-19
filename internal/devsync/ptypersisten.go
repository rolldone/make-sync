package devsync

import (
	"fmt"
	"make-sync/internal/devsync/sshclient"
	"os"
)

// runRemoteCommand executes the given command on the existing persistent SSH session
// using a PTY bridge. It will reuse w.sshClient (no new connection) and set the
// working directory to cfg.Devsync.Auth.RemotePath. Printing is suspended while the
// interactive session is active to avoid interleaved output.
func (w *Watcher) runRemoteCommand(cmd string) {
	if w == nil {
		return
	}

	// Suspend background printing while interactive session is active
	if w.printer != nil {
		w.printer.Suspend()
		defer w.printer.Resume()
	}

	// Note: keyboard handler is expected to have restored terminal state
	// before calling this method (showCommandMenuDisplay restores it). We
	// avoid sending keyboardStop here because the caller (keyboard handler)
	// may be the goroutine that invoked the menu; sending could deadlock.

	if w.sshClient == nil {
		if w.printer != nil {
			w.printer.PrintBlock("no ssh client available to run remote command\n", true)
		} else {
			fmt.Fprintln(os.Stderr, "no ssh client available to run remote command")
		}
		return
	}

	remotePath := w.config.Devsync.Auth.RemotePath
	if remotePath == "" {
		remotePath = "/tmp"
	}

	// Build an initial command that ensures the directory exists and cd into it
	initialCmd := fmt.Sprintf("mkdir -p %s || true && cd %s && bash -c %s ; exec bash", shellEscape(remotePath), shellEscape(remotePath), shellEscape(cmd))

	// Create a PTY bridge with the initial command using the existing ssh client
	bridge, err := sshclient.NewPTYSSHBridgeWithCommand(w.sshClient, initialCmd)
	if err != nil {
		if w.printer != nil {
			w.printer.PrintBlock(fmt.Sprintf("failed to create pty bridge: %v\n", err), true)
		} else {
			fmt.Fprintf(os.Stderr, "failed to create pty bridge: %v\n", err)
		}
		return
	}
	defer func() {
		bridge.Close()
	}()

	// Start interactive shell which runs the initial command and attaches stdio
	if err := bridge.StartInteractiveShell(nil); err != nil {
		if w.printer != nil {
			w.printer.PrintBlock(fmt.Sprintf("remote command failed: %v\n", err), true)
		} else {
			fmt.Fprintf(os.Stderr, "remote command failed: %v\n", err)
		}
	}
}
