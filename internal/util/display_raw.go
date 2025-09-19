package util

import (
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/term"
)

var rawMu sync.Mutex
var rawStates = map[int]*term.State{}

var globalMu sync.Mutex
var globalRestore func() error

// TUIActive should be set to true by the TUI when it owns the terminal.
// When true, helpers that would enable raw mode for the global stdin become no-ops
// so the TUI remains the single owner of terminal raw mode.
var TUIActive bool

// IsRaw reports whether the given fd has been put into raw mode by EnableRaw.
func IsRaw(fd int) bool {
	rawMu.Lock()
	defer rawMu.Unlock()
	_, ok := rawStates[fd]
	return ok
}

// IsAnyRaw reports whether any fd is currently in raw mode.
func IsAnyRaw() bool {
	rawMu.Lock()
	defer rawMu.Unlock()
	return len(rawStates) > 0
}

// IsGlobalRaw reports whether a global restore function has been set (i.e. EnableRawGlobal was used).
func IsGlobalRaw() bool {
	globalMu.Lock()
	defer globalMu.Unlock()
	return globalRestore != nil
}

// IsEffectiveRaw reports whether the effective runtime should consider raw mode enabled
// for the process' stdin. It takes TUI ownership into account: when the TUI owns the
// terminal (`TUIActive == true`), callers should treat the terminal as being under
// TUI control rather than enabling raw mode themselves.
func IsEffectiveRaw() bool {
	if TUIActive {
		return true
	}
	// check common stdin fd
	return IsRaw(int(os.Stdin.Fd())) || IsGlobalRaw()
}

// SetTUIActive sets the TUI ownership flag. Callers should set this to true when
// the TUI takes ownership of the terminal (so other helpers avoid enabling raw mode).
func SetTUIActive(v bool) {
	TUIActive = v
}

// EnableRaw enables raw mode on fd and returns a restore function.
// Restore is safe to call multiple times.
func EnableRaw(fd int) (func() error, error) {
	rawMu.Lock()
	defer rawMu.Unlock()

	if !term.IsTerminal(fd) {
		return func() error { return nil }, nil
	}
	if _, ok := rawStates[fd]; ok {
		// already raw; return noop restore
		return func() error { return nil }, nil
	}

	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	rawStates[fd] = state

	once := sync.Once{}
	restore := func() error {
		var rerr error
		once.Do(func() {
			rawMu.Lock()
			defer rawMu.Unlock()
			if st, ok := rawStates[fd]; ok {
				rerr = term.Restore(fd, st)
				delete(rawStates, fd)
			}
		})
		return rerr
	}
	return restore, nil
}

// EnableRawGlobal enables raw on fd and stores the restore function as the global restore.
// Use RestoreGlobal() to restore later.
func EnableRawGlobal(fd int) (func() error, error) {
	restore, err := EnableRaw(fd)
	if err != nil {
		return nil, err
	}
	f, _ := os.OpenFile("/tmp/make-sync-raw.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	defer f.Close()
	f.WriteString(time.Now().Format(time.RFC3339Nano) + " EnableRawGlobal called fd=" + fmt.Sprint(fd) + "\n")
	SetGlobalRestore(restore)
	return restore, nil
}

// Convenience helper that enables raw on os.Stdin and stores global restore.
func EnableRawGlobalAuto() (func() error, error) {
	// If the TUI owns the terminal, don't change the global raw state.
	if TUIActive {
		return func() error { return nil }, nil
	}
	fd := int(os.Stdin.Fd())
	return EnableRawGlobal(fd)
}

// SetGlobalRestore sets the global restore function (overwrites previous).
func SetGlobalRestore(restore func() error) {
	globalMu.Lock()
	defer globalMu.Unlock()
	f, _ := os.OpenFile("/tmp/make-sync-raw.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	defer f.Close()
	f.WriteString(time.Now().Format(time.RFC3339Nano) + " RestoreGlobal called\n")
	globalRestore = restore
}

// RestoreGlobal calls the stored global restore (if any) and clears it.
func RestoreGlobal() error {
	globalMu.Lock()
	r := globalRestore
	globalRestore = nil
	globalMu.Unlock()
	if r == nil {
		return nil
	}
	return r()
}

// WithRaw is a convenience wrapper: enable raw, run fn, then restore.
func WithRaw(fd int, fn func()) error {
	restore, err := EnableRaw(fd)
	if err != nil {
		return err
	}
	defer restore()
	fn()
	return nil
}
