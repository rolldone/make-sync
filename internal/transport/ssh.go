package transport

import (
	"fmt"
	"os"
	"path"
	"strings"

	"make-sync/internal/sshclient"

	"github.com/pkg/sftp"
)

// SSHTransport adapts an sshclient.SSHClient to the FileTransfer interface.
// Upload/download keep using the existing SSHClient helper methods so the
// legacy behaviour of the ssh mode is untouched; directory, stat and delete
// operations go through the SFTP subsystem.
type SSHTransport struct {
	client *sshclient.SSHClient
	root   string
}

// NewSSHTransport wraps an already connected SSH client with the given remote
// root directory (typically cfg.Devsync.Auth.RemotePath).
func NewSSHTransport(client *sshclient.SSHClient, remoteRoot string) *SSHTransport {
	return &SSHTransport{client: client, root: strings.TrimSpace(remoteRoot)}
}

// Client exposes the underlying SSH client for code paths that still need
// remote command execution (agent boot, chmod, ...).
func (t *SSHTransport) Client() *sshclient.SSHClient {
	return t.client
}

// Root returns the remote root directory used to resolve relative paths.
func (t *SSHTransport) Root() string {
	return t.root
}

// Close terminates the underlying SSH connection.
func (t *SSHTransport) Close() error {
	if t.client == nil {
		return nil
	}
	return t.client.Close()
}

// isWindowsRoot reports whether the configured root is a Windows path.
func (t *SSHTransport) isWindowsRoot() bool {
	if strings.HasPrefix(t.root, `\\`) {
		return true
	}
	return len(t.root) >= 3 && t.root[1] == ':' && (t.root[2] == '\\' || t.root[2] == '/')
}

// resolveRemote joins the remote root with a relative path, mirroring the
// separator style of the configured root.
func (t *SSHTransport) resolveRemote(rel string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(rel), "\\", "/")
	normalized = strings.TrimPrefix(normalized, "/")

	joined := path.Join(strings.ReplaceAll(t.root, "\\", "/"), normalized)
	if t.isWindowsRoot() {
		return strings.ReplaceAll(joined, "/", "\\")
	}
	return joined
}

// UploadFile copies a local file to relTarget under the remote root.
func (t *SSHTransport) UploadFile(localPath, relTarget string) error {
	if t.client == nil {
		return fmt.Errorf("ssh transport is not connected")
	}
	return t.client.UploadFile(localPath, t.resolveRemote(relTarget))
}

// DownloadFile copies relSource under the remote root to localTarget.
func (t *SSHTransport) DownloadFile(relSource, localTarget string) error {
	if t.client == nil {
		return fmt.Errorf("ssh transport is not connected")
	}
	return t.client.DownloadFile(localTarget, t.resolveRemote(relSource))
}

// DeleteRemote removes relPath on the remote host recursively.
func (t *SSHTransport) DeleteRemote(relPath string) error {
	if t.client == nil {
		return fmt.Errorf("ssh transport is not connected")
	}
	target := t.resolveRemote(relPath)
	if target == t.root {
		return fmt.Errorf("%w: refusing to delete transport root", ErrPathTraversal)
	}
	cmd := fmt.Sprintf("rm -rf %s", shellQuote(target))
	return t.client.RunCommand(cmd)
}

// MkdirAll creates relPath and missing parents on the remote host.
func (t *SSHTransport) MkdirAll(relPath string) error {
	if t.client == nil {
		return fmt.Errorf("ssh transport is not connected")
	}
	target := t.resolveRemote(relPath)
	var cmd string
	if t.isWindowsRoot() {
		cmd = fmt.Sprintf(`cmd.exe /C if not exist "%s" mkdir "%s"`, target, target)
	} else {
		cmd = fmt.Sprintf("mkdir -p %s", shellQuote(target))
	}
	return t.client.RunCommand(cmd)
}

// Stat returns metadata for relPath on the remote host.
func (t *SSHTransport) Stat(relPath string) (FileInfo, error) {
	sftpClient, err := t.openSFTP()
	if err != nil {
		return FileInfo{}, err
	}
	defer sftpClient.Close()

	info, err := sftpClient.Stat(t.resolveRemote(relPath))
	if err != nil {
		return FileInfo{}, err
	}
	return sftpFileInfo(relPath, info), nil
}

// ListDir lists the entries directly under relPath on the remote host.
func (t *SSHTransport) ListDir(relPath string) ([]FileInfo, error) {
	sftpClient, err := t.openSFTP()
	if err != nil {
		return nil, err
	}
	defer sftpClient.Close()

	entries, err := sftpClient.ReadDir(t.resolveRemote(relPath))
	if err != nil {
		return nil, err
	}
	out := make([]FileInfo, 0, len(entries))
	for _, entry := range entries {
		child := path.Join(strings.ReplaceAll(strings.TrimSpace(relPath), "\\", "/"), entry.Name())
		out = append(out, sftpFileInfo(child, entry))
	}
	return out, nil
}

func (t *SSHTransport) openSFTP() (*sftp.Client, error) {
	if t.client == nil {
		return nil, fmt.Errorf("ssh transport is not connected")
	}
	return t.client.NewSFTPClient()
}

// shellQuote wraps a string in single quotes for safe shell inclusion.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sftpFileInfo converts an os.FileInfo (from SFTP) into a transport FileInfo
// using a forward-slash relative path.
func sftpFileInfo(rel string, info os.FileInfo) FileInfo {
	normalized := strings.ReplaceAll(rel, "\\", "/")
	return FileInfo{
		Path:    normalized,
		Size:    info.Size(),
		ModTime: info.ModTime(),
		IsDir:   info.IsDir(),
		Mode:    info.Mode(),
	}
}
