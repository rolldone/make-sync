//go:build windows
// +build windows

package devsync

import (
	"os"
	"syscall"
	"time"
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
