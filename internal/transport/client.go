package transport

import (
	"fmt"
	"strings"

	"make-sync/internal/sshclient"
)

// UploadPair is a single local->root file mapping used by bulk operations.
type UploadPair struct {
	Local  string
	Remote string
}

// Client is the execution + file transfer facade consumed by sync, deploy,
// manual transfer and the watcher.
//
// It mirrors the subset of sshclient.SSHClient the codebase actually uses, so a
// call site can switch between SSH and local mode without changing its logic.
// Paths handed to the file methods are absolute paths in the destination
// namespace (remote paths for SSH, local ABS paths under the target root for
// local mode).
type Client interface {
	// Connect establishes the underlying connection (no-op for local mode).
	Connect() error
	// Close releases the underlying resources.
	Close() error
	// SyncFile uploads a local file to destPath.
	SyncFile(localPath, destPath string) error
	// UploadFile uploads a local file to destPath.
	UploadFile(localPath, destPath string) error
	// UploadFileSCP uploads a local file using the alternate (scp) backend.
	UploadFileSCP(localPath, destPath string) error
	// DownloadFile downloads srcPath to localPath.
	DownloadFile(localPath, srcPath string) error
	// UploadFilesSFTP uploads pairs concurrently and returns successful local paths.
	UploadFilesSFTP(pairs []UploadPair, concurrency int) ([]string, error)
	// DownloadFilesSFTP downloads pairs concurrently and returns successful local paths.
	DownloadFilesSFTP(pairs []UploadPair, concurrency int) ([]string, error)
	// RunCommand executes cmd, discarding output.
	RunCommand(cmd string) error
	// RunCommandWithOutput executes cmd and returns its combined output.
	RunCommandWithOutput(cmd string) (string, error)
	// RunCommandWithStream executes cmd and streams stdout/stderr.
	RunCommandWithStream(cmd string, usePty bool) (<-chan string, <-chan error, error)
	// HasSession reports whether a persistent session is available. Local mode
	// always reports true.
	HasSession() bool
	// StartPersistentSession prepares a persistent session (no-op when unused).
	StartPersistentSession() error
	// StopAgentSession releases the agent session (no-op when unused).
	StopAgentSession() error
	// IsPersistent reports whether the client uses a persistent connection.
	IsPersistent() bool
	// AbortAllSCPTransfers cancels in-flight SCP transfers (no-op when unused).
	AbortAllSCPTransfers()
}

// NewSSHClient adapts a connected sshclient.SSHClient into a Client. It is a
// thin pass-through: no SSH behaviour is altered.
func NewSSHClient(client *sshclient.SSHClient) Client {
	return &sshClient{c: client}
}

// sshClient adapts sshclient.SSHClient to the Client interface.
type sshClient struct {
	c *sshclient.SSHClient
}

func (a *sshClient) Connect() error { return a.c.Connect() }
func (a *sshClient) Close() error   { return a.c.Close() }

func (a *sshClient) SyncFile(localPath, destPath string) error {
	return a.c.SyncFile(localPath, destPath)
}

func (a *sshClient) UploadFile(localPath, destPath string) error {
	return a.c.UploadFile(localPath, destPath)
}

func (a *sshClient) UploadFileSCP(localPath, destPath string) error {
	return a.c.UploadFileSCP(localPath, destPath)
}

func (a *sshClient) DownloadFile(localPath, srcPath string) error {
	return a.c.DownloadFile(localPath, srcPath)
}

func (a *sshClient) UploadFilesSFTP(pairs []UploadPair, concurrency int) ([]string, error) {
	return a.c.UploadFilesSFTP(toSSHPairs(pairs), concurrency)
}

func (a *sshClient) DownloadFilesSFTP(pairs []UploadPair, concurrency int) ([]string, error) {
	return a.c.DownloadFilesSFTP(toSSHPairs(pairs), concurrency)
}

func (a *sshClient) RunCommand(cmd string) error { return a.c.RunCommand(cmd) }

func (a *sshClient) RunCommandWithOutput(cmd string) (string, error) {
	return a.c.RunCommandWithOutput(cmd)
}

func (a *sshClient) RunCommandWithStream(cmd string, usePty bool) (<-chan string, <-chan error, error) {
	return a.c.RunCommandWithStream(cmd, usePty)
}

func (a *sshClient) HasSession() bool              { return a.c.GetSession() != nil }
func (a *sshClient) StartPersistentSession() error { return a.c.StartPersistentSession() }
func (a *sshClient) StopAgentSession() error       { return a.c.StopAgentSession() }
func (a *sshClient) IsPersistent() bool            { return a.c.IsPersistent() }
func (a *sshClient) AbortAllSCPTransfers()         { a.c.AbortAllSCPTransfers() }

// SSHClient exposes the wrapped client, for call sites that still need
// SSH-only capabilities (PTY bridge).
func (a *sshClient) SSHClient() *sshclient.SSHClient {
	return a.c
}

// toSSHPairs converts transport pairs into sshclient pairs.
func toSSHPairs(pairs []UploadPair) []sshclient.UploadPair {
	out := make([]sshclient.UploadPair, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, sshclient.UploadPair{Local: p.Local, Remote: p.Remote})
	}
	return out
}

// AsSSHClient unwraps a Client back into an sshclient.SSHClient when possible.
func AsSSHClient(c Client) (*sshclient.SSHClient, error) {
	if a, ok := c.(*sshClient); ok {
		return a.c, nil
	}
	return nil, fmt.Errorf("client is not backed by SSH")
}

// ModeLabel returns a short label for logging.
func ModeLabel(auth interface{ EffectiveMode() string }) string {
	mode := strings.TrimSpace(auth.EffectiveMode())
	if mode == "" {
		return "ssh"
	}
	return mode
}
