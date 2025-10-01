package localclient

import (
	"context"
	"io"
	"make-sync/internal/pty"
	"os"
	"sync"
	"time"

	"golang.org/x/term"
)

// PTYLocalBridge is a simplified PTY bridge for running local commands in a PTY.
type PTYLocalBridge struct {
	localPTY pty.PTY
	ioCancel chan bool
	ioOnce   sync.Once

	StdinMatcher   func([]byte) bool
	StdinCallback  func([]byte)
	StdinObserver  func([]byte)
	outputDisabled bool
	outputMu       sync.Mutex

	initialCommand string
	mu             sync.RWMutex
	stdin          io.WriteCloser // optional explicit writer (set to PTY file)
	// exit listener called when local PTY/process exits
	exitListener         func()
	inputListener        func([]byte)
	inputHitCodeListener func(string)
	exitMu               sync.Mutex
	lastHitTs            time.Time
	// inputBuf is a small bounded channel used on Unix builds to queue
	// stdin fragments from the manager. This allows the bridge to process
	// input in a single place and optionally notify listeners.
	inputBuf       chan []byte
	cancelFunc     context.CancelFunc
	oldState       *term.State
	switchOldState *term.State

	outPipe *os.File

	// output tap receives stdout/stderr bytes (err=false for stdout, true for stderr if implemented)
	outputTap func([]byte, bool)
}

func NewPTYLocalBridge(initialCommand string) (*PTYLocalBridge, error) {
	return &PTYLocalBridge{
		ioCancel:       make(chan bool),
		ioOnce:         sync.Once{},
		initialCommand: initialCommand,
		inputBuf:       nil,
	}, nil
}

// PushInput enqueues input bytes into the bridge's input buffer (if present).
// It returns false if the buffer is not configured or the buffer is full.
func (b *PTYLocalBridge) PushInput(data []byte) bool {
	if b.inputBuf == nil {
		return false
	}
	// make a copy to avoid reuse issues
	d := append([]byte(nil), data...)
	select {
	case b.inputBuf <- d:
		return true
	default:
		return false
	}
}

// SetInputListener registers a listen-only stdin callback. PTYManager will
// invoke this callback asynchronously when it wants to observe stdin bytes
// without writing them into the bridge's stdin writer.
func (b *PTYLocalBridge) SetInputListener(cb func([]byte)) {
	b.mu.Lock()
	b.StdinObserver = cb
	b.mu.Unlock()
}

// SetOnInputListener registers a listen-only stdin callback. The PTYManager
// or other components may register a callback to observe stdin bytes; the
// manager will remain responsible for invoking it.
func (b *PTYLocalBridge) SetOnInputListener(cb func([]byte)) {
	b.mu.Lock()
	b.inputListener = cb
	b.mu.Unlock()
}

func (b *PTYLocalBridge) SetOnInputHitCodeListener(cb func(string)) {
	b.mu.Lock()
	b.inputHitCodeListener = cb
	b.mu.Unlock()
}

// SetOutputTap registers a tap receiving stdout/stderr bytes (err=true for stderr).
// The tap is invoked regardless of outputDisabled so logging can continue while UI is paused.
func (b *PTYLocalBridge) SetOutputTap(fn func([]byte, bool)) {
	b.outputMu.Lock()
	b.outputTap = fn
	b.outputMu.Unlock()
}
