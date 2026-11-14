// Package transport abstracts the file transfer backend used by make-sync.
//
// Historically every sync operation talked to a remote machine over SSH. This
// package introduces a small interface (FileTransfer) with two implementations:
//
//   - SSHTransport: adapter over internal/sshclient, preserving the legacy
//     SSH behaviour.
//   - LocalTransport: direct filesystem access between two local paths, used
//     when devsync.auth.mode is "local".
//
// Paths handed to the interface are always relative to the transfer root
// (remote root for SSH, localTargetPath for local). The root itself is never
// exposed through the interface, which keeps callers agnostic about the
// underlying transport and lets LocalTransport enforce path safety.
package transport

import (
	"errors"
	"fmt"
	"io/fs"
	"time"

	"make-sync/internal/config"
	"make-sync/internal/sshclient"
)

// ErrPathTraversal is returned when a relative path would escape the transfer
// root after cleaning (e.g. "../../etc/passwd").
var ErrPathTraversal = errors.New("path escapes transfer root")

// ErrNotSupported is returned by optional operations a transport does not
// implement.
var ErrNotSupported = errors.New("operation not supported by transport")

// FileInfo describes a single entry under the transfer root.
type FileInfo struct {
	// Path is the entry path relative to the transfer root, always using
	// forward slashes regardless of host OS.
	Path    string
	Size    int64
	ModTime time.Time
	IsDir   bool
	Mode    fs.FileMode
}

// FileTransfer is the transport contract consumed by sync/deploy code.
//
// Local paths (localPath / localTarget) are absolute paths on the machine
// running make-sync. Relative paths (relTarget / relSource / relPath) are
// resolved against the transport root by the implementation.
type FileTransfer interface {
	// UploadFile copies a local file to relTarget under the transfer root,
	// creating parent directories as needed.
	UploadFile(localPath, relTarget string) error
	// DownloadFile copies the file at relSource under the transfer root to the
	// absolute localTarget path, creating parent directories as needed.
	DownloadFile(relSource, localTarget string) error
	// DeleteRemote removes the file or directory at relPath (recursively).
	DeleteRemote(relPath string) error
	// MkdirAll creates relPath and any missing parents.
	MkdirAll(relPath string) error
	// Stat returns metadata for relPath.
	Stat(relPath string) (FileInfo, error)
	// ListDir lists entries directly under relPath.
	ListDir(relPath string) ([]FileInfo, error)
}

// NewTransport builds the transport selected by cfg.Devsync.Auth.mode.
//
// Mode "local" returns a LocalTransport rooted at localTargetPath. Mode "ssh"
// (default) dials the configured host and returns an SSHTransport. The caller
// is responsible for closing the transport when it implements io.Closer.
func NewTransport(cfg *config.Config) (FileTransfer, error) {
	auth := cfg.Devsync.Auth
	switch auth.EffectiveMode() {
	case config.AuthModeLocal:
		return NewLocalTransport(auth.LocalTargetPath)
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
		return NewSSHTransport(client, auth.RemotePath), nil
	default:
		return nil, fmt.Errorf("unsupported transport mode %q", auth.Mode)
	}
}
