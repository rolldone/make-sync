package devsync

import (
	"fmt"
	"make-sync/internal/devsync/localclient"
	"make-sync/internal/devsync/sshclient"
)

// CreateSSHBridgeWithCommand constructs an SSH-backed Bridge using an existing
// SSH client and an initial command.
func CreateSSHBridgeWithCommand(sshClient *sshclient.SSHClient, initialCmd string) (Bridge, error) {
	if sshClient == nil {
		return nil, fmt.Errorf("nil ssh client")
	}
	b, err := sshclient.NewPTYSSHBridgeWithCommand(sshClient, initialCmd)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// CreateSSHBridge constructs an SSH-backed Bridge (no initial command).
func CreateSSHBridge(sshClient *sshclient.SSHClient) (Bridge, error) {
	if sshClient == nil {
		return nil, fmt.Errorf("nil ssh client")
	}
	b, err := sshclient.NewPTYSSHBridge(sshClient)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// CreateLocalBridge constructs a local PTY bridge. The caller can then
// call StartInteractiveShell or StartInteractiveShellWithCommand on the bridge.
func CreateLocalBridge(initialCmd string) (Bridge, error) {
	b, err := localclient.NewPTYLocalBridge(initialCmd)
	if err != nil {
		return nil, err
	}
	return b, nil
}
