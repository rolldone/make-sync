package localclient

import (
	"io"
	"make-sync/internal/pty"
	"sync"
)

// PTYLocalBridge is a simplified PTY bridge for running local commands in a PTY.
type PTYLocalBridge struct {
	localPTY    pty.PTY
	termRestore func() error
	ioCancel    chan bool
	ioOnce      sync.Once

	StdinMatcher   func([]byte) bool
	StdinCallback  func([]byte)
	StdinObserver  func([]byte)
	outputDisabled bool
	outputMu       sync.Mutex

	initialCommand string
	mu             sync.RWMutex
	stdin          io.WriteCloser // optional explicit writer (set to PTY file)
	// exit listener called when local PTY/process exits
	exitListener func()
	exitMu       sync.Mutex
}

func NewPTYLocalBridge(initialCommand string) (*PTYLocalBridge, error) {
	return &PTYLocalBridge{
		ioCancel:       make(chan bool),
		ioOnce:         sync.Once{},
		initialCommand: initialCommand,
	}, nil
}
