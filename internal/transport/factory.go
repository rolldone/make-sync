package transport

import (
	"fmt"

	"make-sync/internal/config"
	"make-sync/internal/sshclient"
)

// NewClient builds a Client according to cfg.Devsync.Auth.mode.
//
//   - mode "local": returns a LocalClient rooted at localTargetPath. No SSH is
//     opened.
//   - mode "ssh" (default): dials the configured host and returns an adapter
//     over sshclient.SSHClient, preserving all existing behaviour.
func NewClient(cfg *config.Config) (Client, error) {
	auth := cfg.Devsync.Auth
	switch auth.EffectiveMode() {
	case config.AuthModeLocal:
		return NewLocalClient(auth.RemoteRoot())
	case config.AuthModeSSH:
		port := auth.Port
		if port == "" {
			port = "22"
		}
		client, err := sshclient.NewPersistentSSHClient(auth.Username, auth.PrivateKey, auth.Password, auth.Host, port)
		if err != nil {
			return nil, fmt.Errorf("failed to create ssh client: %w", err)
		}
		if err := client.Connect(); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("failed to connect ssh client: %w", err)
		}
		return NewSSHClient(client), nil
	default:
		return nil, fmt.Errorf("unsupported transport mode %q", auth.Mode)
	}
}
