//go:build windows

package sshforward

import "golang.org/x/sys/windows"

// processAlive reports whether the process with the given PID is still running.
// On Windows we open the process with PROCESS_QUERY_LIMITED_INFORMATION; if the
// PID does not exist, OpenProcess fails with ERROR_INVALID_PARAMETER.
func processAlive(pid int) (alive bool, err error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if err == windows.ERROR_INVALID_PARAMETER {
			return false, nil // process does not exist
		}
		return false, err
	}
	windows.CloseHandle(h)
	return true, nil
}
