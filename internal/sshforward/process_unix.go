//go:build !windows

package sshforward

import "syscall"

// processAlive reports whether the process with the given PID is still running.
// It uses signal 0 which does not actually send a signal, only checks existence.
func processAlive(pid int) (alive bool, err error) {
	err = syscall.Kill(pid, 0)
	switch err {
	case nil, syscall.EPERM:
		return true, nil
	case syscall.ESRCH:
		return false, nil
	default:
		return false, err
	}
}
