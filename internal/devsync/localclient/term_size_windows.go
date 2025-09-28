//go:build windows
// +build windows

package localclient

import (
	"golang.org/x/sys/windows"
)

// getTerminalSizeWindows returns (cols, rows, err) by querying the Win32
// console buffer. This is a fallback for environments where term.GetSize
// cannot determine the size (e.g., certain redirected/stdout setups).
func getTerminalSizeWindows() (cols, rows int, err error) {
	h, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	if err != nil {
		return 0, 0, err
	}
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(h, &info); err != nil {
		return 0, 0, err
	}
	cols = int(info.Window.Right - info.Window.Left + 1)
	rows = int(info.Window.Bottom - info.Window.Top + 1)
	return cols, rows, nil
}
