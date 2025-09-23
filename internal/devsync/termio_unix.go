//go:build !windows
// +build !windows

package devsync

import (
	"os"
	"syscall"
	"time"
	"unsafe"
)

func flushStdin() {
	fd := int(os.Stdin.Fd())
	// best-effort flush of input queue
	_ = syscall.Tcflush(fd, syscall.TCIFLUSH)
}

func sendEnter() {
	fd := int(os.Stdin.Fd())
	c := byte('\n')
	// try to inject newline into the tty input buffer (TIOCSTI)
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TIOCSTI), uintptr(unsafe.Pointer(&c)))
	if errno != 0 {
		// fallback: try writing to /dev/tty
		if f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
			_, _ = f.Write([]byte{'\n'})
			f.Close()
		}
	}
	// give terminal a short moment to settle
	time.Sleep(30 * time.Millisecond)
}
