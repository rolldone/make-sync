//go:build !windows
// +build !windows

package devsync

import (
	"os"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func flushStdin() {
	fd := int(os.Stdin.Fd())
	// best-effort flush of input queue
	_ = unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH)
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

// sendKeyA injects a lowercase 'a' into the TTY input buffer (best-effort)
func sendKeyA() {
	go func() {
		// time.Sleep(50 * time.Millisecond)
		// fd := int(os.Stdin.Fd())
		// c := byte('a')
		// // try to inject 'a' into the tty input buffer (TIOCSTI)
		// _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TIOCSTI), uintptr(unsafe.Pointer(&c)))
		// if errno != 0 {
		// 	// fallback: try writing to /dev/tty
		// 	if f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		// 		_, _ = f.Write([]byte{'a'})
		// 		f.Close()
		// 	}
		// }
		// fmt.Println("Injected 'a' into TTY input buffer")
	}()
}

// sendCtrlArrowDown injects a Ctrl+ArrowDown key sequence (best-effort).
// Tries multiple common sequences and falls back to /dev/tty if TIOCSTI is blocked.
func sendCtrlArrowDown() {
	fd := int(os.Stdin.Fd())

	seqs := [][]byte{
		// common xterm variants
		[]byte{0x1b, '[', '1', ';', '5', 'B'},
		[]byte{0x1b, '[', '5', 'B'},
	}

	for _, seq := range seqs {
		// try injecting bytes into tty input buffer (TIOCSTI) per-byte
		ok := true
		for _, b := range seq {
			c := b
			_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TIOCSTI), uintptr(unsafe.Pointer(&c)))
			if errno != 0 {
				ok = false
				break
			}
		}
		if ok {
			time.Sleep(25 * time.Millisecond)
			return
		}

		// fallback: try writing the escape sequence to /dev/tty
		if f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
			_, _ = f.Write(seq)
			_ = f.Close()
			time.Sleep(25 * time.Millisecond)
			return
		}
	}

	// nothing worked — best-effort pause
	time.Sleep(25 * time.Millisecond)
}

// waitAnyKey waits for a single keypress (best-effort).
func waitAnyKey() {
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err == nil {
		defer term.Restore(fd, old)
	}
	buf := make([]byte, 1)
	_, _ = os.Stdin.Read(buf)
}
