package util

import (
	"fmt"
	"net"
)

// GetFreeTCPPort asks the kernel for a free open port that is ready to use on
// the specified bind address. It does this by listening on address:0 which
// tells the kernel to allocate an ephemeral port. The listener is closed
// immediately after reading the assigned port. Note: this has a small race
// window between closing the listener and another process binding the port.
func GetFreeTCPPort(bindAddr string) (int, error) {
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:0", bindAddr))
	if err != nil {
		return 0, err
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)
	if addr.Port == 0 {
		return 0, fmt.Errorf("failed to acquire free port")
	}
	return addr.Port, nil
}
