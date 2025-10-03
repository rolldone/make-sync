//go:build !windows && !darwin
// +build !windows,!darwin

package devsync

import (
	"log"
	"make-sync/internal/util"
	"os"
	"syscall"
	"time"
	"unsafe"

	// "golang.org/x/sys/unix" (not used on all platforms)
	"golang.org/x/term"
)

func flushStdin() {
	// Try to drain any pending bytes by opening /dev/tty in non-blocking mode
	// and reading whatever is immediately available. This avoids using
	// platform-specific ioctl constants like FIONREAD/TCFLSH which may not
	// exist on all Unix variants.
	if f, err := os.OpenFile("/dev/tty", os.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
		defer f.Close()
		buf := make([]byte, 4096)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				// discard
			}
			if err != nil {
				break
			}
			// if we read less than buffer, likely drained
			if n < len(buf) {
				break
			}
		}
		return
	}
	// fallback: if /dev/tty couldn't be opened non-blocking, give up silently
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
		time.Sleep(50 * time.Millisecond)
		fd := int(os.Stdin.Fd())
		c := byte('a')
		// try to inject 'a' into the tty input buffer (TIOCSTI)
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TIOCSTI), uintptr(unsafe.Pointer(&c)))
		if errno != 0 {
			// fallback: try writing to /dev/tty
			if f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
				_, _ = f.Write([]byte{'a'})
				f.Close()
			}
		}
		log.Println("Injected 'a' into TTY input buffer")
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

func FlushAllStdinNonBlocking() {
	fd := int(os.Stdin.Fd())
	util.Default.Println("Press any key to continue...")
	// Set stdin ke non-blocking
	_ = syscall.SetNonblock(fd, true)
	defer syscall.SetNonblock(fd, false)

	var buf [1]byte
	for {
		n, err := os.Stdin.Read(buf[:])
		if n == 0 || err != nil {
			break
		}
	}
}
