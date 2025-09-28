//go:build windows
// +build windows

package devsync

import (
	"os"
	"syscall"
	"time"

	"golang.org/x/term"
)

// flushStdin flushes the Windows console input buffer (best-effort)
func flushStdin() {
	handle := syscall.Handle(os.Stdin.Fd())
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("FlushConsoleInputBuffer")
	_, _, _ = proc.Call(uintptr(handle))
}

// sendEnter simulates an Enter keypress using Win32 keybd_event (best-effort)
func sendEnter() {
	user32 := syscall.NewLazyDLL("user32.dll")
	keybd := user32.NewProc("keybd_event")
	const KEYEVENTF_KEYUP = 0x0002
	const VK_RETURN = 0x0D

	// press Enter
	_, _, _ = keybd.Call(uintptr(VK_RETURN), uintptr(0), uintptr(0), uintptr(0))
	// small pause
	time.Sleep(15 * time.Millisecond)
	// release Enter
	_, _, _ = keybd.Call(uintptr(VK_RETURN), uintptr(0), uintptr(KEYEVENTF_KEYUP), uintptr(0))
}

// sendKeyA simulates pressing the 'a' key (lowercase) via Win32 keybd_event (best-effort)
func sendKeyA() {
	user32 := syscall.NewLazyDLL("user32.dll")
	keybd := user32.NewProc("keybd_event")
	const KEYEVENTF_KEYUP = 0x0002
	// Virtual-Key code for 'A' is 0x41
	const VK_A = 0x41

	// press 'A'
	_, _, _ = keybd.Call(uintptr(VK_A), uintptr(0), uintptr(0), uintptr(0))
	time.Sleep(10 * time.Millisecond)
	// release 'A'
	_, _, _ = keybd.Call(uintptr(VK_A), uintptr(0), uintptr(KEYEVENTF_KEYUP), uintptr(0))
}

// sendCtrlArrowDown simulates Ctrl+ArrowDown (best-effort).
func sendCtrlArrowDown() {
	user32 := syscall.NewLazyDLL("user32.dll")
	keybd := user32.NewProc("keybd_event")
	const (
		KEYEVENTF_KEYUP = 0x0002
		VK_CONTROL      = 0x11
		VK_DOWN         = 0x28
	)

	// press Ctrl
	_, _, _ = keybd.Call(uintptr(VK_CONTROL), 0, 0, 0)
	time.Sleep(5 * time.Millisecond)
	// press Down
	_, _, _ = keybd.Call(uintptr(VK_DOWN), 0, 0, 0)
	time.Sleep(15 * time.Millisecond)
	// release Down
	_, _, _ = keybd.Call(uintptr(VK_DOWN), 0, KEYEVENTF_KEYUP, 0)
	time.Sleep(5 * time.Millisecond)
	// release Ctrl
	_, _, _ = keybd.Call(uintptr(VK_CONTROL), 0, KEYEVENTF_KEYUP, 0)
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
