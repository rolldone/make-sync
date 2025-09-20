package devsync

import (
	"io"
	"make-sync/internal/devsync/localclient"
	"make-sync/internal/devsync/sshclient"
)

// Bridge defines the minimal interface a PTY bridge must implement so
// PTYManager can control both SSH-backed and Local-backed PTY sessions
// interchangeably.
type Bridge interface {
	// StartInteractiveShell starts the interactive session. Implementations
	// may accept an initial command via constructor/factory; the manager
	// passes stdin callbacks via the SetStdinCallback family.
	StartInteractiveShell(cb func([]byte)) error

	Pause() error
	Resume() error
	Close() error

	SetStdinMatcher(func([]byte) bool)
	GetStdinMatcher() func([]byte) bool

	SetStdinCallback(func([]byte))
	GetStdinCallback() func([]byte)

	SetStdinObserver(func([]byte))
	GetStdinObserver() func([]byte)

	SetStdinWriter(io.WriteCloser)
	GetStdinWriter() io.WriteCloser
	// SetOnExitListener registers a callback that will be invoked when the
	// bridge's interactive session naturally exits (or the bridge detects EOF).
	// The callback should be invoked exactly once by the bridge implementation
	// and must be safe to call from any goroutine.
	SetOnExitListener(func())
}

// compile-time assertions that existing bridge implementations satisfy Bridge
var _ Bridge = (*sshclient.PTYSSHBridge)(nil)
var _ Bridge = (*localclient.PTYLocalBridge)(nil)
