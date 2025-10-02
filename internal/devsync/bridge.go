package devsync

import (
	"io"
	"make-sync/internal/devsync/localclient"
	"make-sync/internal/sshclient"
)

// Bridge defines the minimal interface a PTY bridge must implement so
// PTYManager can control both SSH-backed and Local-backed PTY sessions
// interchangeably.
type Bridge interface {
	// StartInteractiveShell starts the interactive session. Implementations
	// may accept an initial command via constructor/factory; the manager
	// passes stdin callbacks via the SetStdinCallback family.
	StartInteractiveShell() error

	// Pause stops output to terminal and caches subsequent output for Resume.
	Pause() error
	// Resume loads cached output to terminal and resumes normal output.
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
	// SetInputListener registers a listen-only callback. PTYManager (the
	// central stdin reader) may register a function here to receive stdin
	// bytes without the bridge itself reading os.Stdin. The callback will be
	// invoked asynchronously by the manager; it must be safe for concurrent
	// use. This API is intentionally write-only (no getter) to keep
	// ownership of stdin in the manager.
	// SetOnInputListener registers a listen-only callback. PTYManager (the
	// central stdin reader) or other components may register a function here
	// to receive stdin bytes. The manager will decide how/when to invoke
	// the registered callback. This API is intentionally write-focused; a
	// companion "ready" accessor is provided by bridge implementations as
	// needed.
	SetOnInputListener(func([]byte))
	SetOnInputHitCodeListener(func(string))
}

// compile-time assertions that existing bridge implementations satisfy Bridge
var _ Bridge = (*sshclient.PTYSSHBridge)(nil)
var _ Bridge = (*localclient.PTYLocalBridge)(nil)
