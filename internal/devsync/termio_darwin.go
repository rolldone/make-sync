//go:build darwin
// +build darwin

package devsync

import (
	"os"
	"time"

	"golang.org/x/term"
)

// On darwin some ioctl constants used on Linux/BSD are not available. Provide
// a conservative, portable implementation: flushStdin is a best-effort no-op
// (we cannot access the same TCFLSH behavior portably), and other helpers
// mimic the behavior expected by the callers.

func flushStdin() {
	// No reliable portable TCFLSH on darwin via golang.org/x/sys/unix.
	// Best-effort fallback: attempt to read any immediately available bytes
	// from /dev/tty (non-blocking would be ideal but is complex), but to
	// keep this simple and safe we do nothing here.
	return
}

func sendEnter() {
	// fallback: try writing to /dev/tty
	if f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		_, _ = f.Write([]byte{'\n'})
		f.Close()
	}
	// give terminal a short moment to settle
	time.Sleep(30 * time.Millisecond)
}

func sendKeyA() {
	// No-op on darwin; left intentionally empty.
}

func sendCtrlArrowDown() {
	// Fallback: write a common escape to /dev/tty
	seq := []byte{0x1b, '[', '5', 'B'}
	if f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		_, _ = f.Write(seq)
		_ = f.Close()
		time.Sleep(25 * time.Millisecond)
		return
	}
	// nothing worked — best-effort pause
	time.Sleep(25 * time.Millisecond)
}

func waitAnyKey() {
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err == nil {
		defer term.Restore(fd, old)
	}
	buf := make([]byte, 1)
	_, _ = os.Stdin.Read(buf)
}
