package transport

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// LocalTransport transfers files directly on the local filesystem, rooted at an
// absolute target directory. Every relative path is resolved against that root
// and validated to stay inside it.
type LocalTransport struct {
	root string
}

// NewLocalTransport creates a LocalTransport for the given root directory.
// The root must be an absolute path; it is created if it does not exist.
func NewLocalTransport(root string) (*LocalTransport, error) {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return nil, fmt.Errorf("local transport root cannot be empty")
	}
	if !filepath.IsAbs(trimmed) {
		return nil, fmt.Errorf("local transport root must be an absolute path: %s", trimmed)
	}

	abs, err := filepath.Abs(filepath.Clean(trimmed))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve local transport root: %w", err)
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		return nil, fmt.Errorf("failed to create local transport root %s: %w", abs, err)
	}

	return &LocalTransport{root: abs}, nil
}

// Root returns the absolute root directory of the transport.
func (t *LocalTransport) Root() string {
	return t.root
}

// Close releases resources. LocalTransport holds no handles, so it is a no-op.
func (t *LocalTransport) Close() error {
	return nil
}

// resolve maps a relative, root-scoped path to an absolute local path and
// guarantees the result stays inside the transport root.
func (t *LocalTransport) resolve(rel string) (string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(rel), "\\", "/")
	if strings.HasPrefix(normalized, "/") {
		return "", fmt.Errorf("%w: %q must be a relative path", ErrPathTraversal, rel)
	}
	// Reject any parent segment outright. Cleaning alone would silently clamp
	// "../x" to "x", which hides bugs in callers.
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return "", fmt.Errorf("%w: %q", ErrPathTraversal, rel)
		}
	}

	cleaned := path.Clean("/" + normalized) // collapse . and empty segments
	relPath := strings.TrimPrefix(cleaned, "/")

	abs := filepath.Join(t.root, filepath.FromSlash(relPath))

	// Defence in depth: ensure the joined path is the root itself or a child.
	if abs != t.root {
		relCheck, err := filepath.Rel(t.root, abs)
		if err != nil {
			return "", fmt.Errorf("failed to resolve %q: %w", rel, err)
		}
		if relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("%w: %q", ErrPathTraversal, rel)
		}
	}

	return abs, nil
}

// UploadFile copies a local file into the transfer root, creating parents.
func (t *LocalTransport) UploadFile(localPath, relTarget string) error {
	dst, err := t.resolve(relTarget)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory for %s: %w", relTarget, err)
	}
	return copyFilePreserve(localPath, dst)
}

// DownloadFile copies a file from the transfer root to an absolute local path.
func (t *LocalTransport) DownloadFile(relSource, localTarget string) error {
	src, err := t.resolve(relSource)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(localTarget), 0755); err != nil {
		return fmt.Errorf("failed to create local target directory for %s: %w", localTarget, err)
	}
	return copyFilePreserve(src, localTarget)
}

// DeleteRemote removes relPath (file or directory) recursively.
func (t *LocalTransport) DeleteRemote(relPath string) error {
	target, err := t.resolve(relPath)
	if err != nil {
		return err
	}
	if target == t.root {
		return fmt.Errorf("%w: refusing to delete transport root", ErrPathTraversal)
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("failed to delete %s: %w", relPath, err)
	}
	return nil
}

// MkdirAll creates relPath and missing parents under the transfer root.
func (t *LocalTransport) MkdirAll(relPath string) error {
	target, err := t.resolve(relPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		return fmt.Errorf("failed to create %s: %w", relPath, err)
	}
	return nil
}

// Stat returns metadata for relPath.
func (t *LocalTransport) Stat(relPath string) (FileInfo, error) {
	target, err := t.resolve(relPath)
	if err != nil {
		return FileInfo{}, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return FileInfo{}, err
	}
	return localFileInfo(relPath, info), nil
}

// ListDir lists the entries directly under relPath.
func (t *LocalTransport) ListDir(relPath string) ([]FileInfo, error) {
	target, err := t.resolve(relPath)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return nil, err
	}
	out := make([]FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		child := path.Join(strings.TrimSpace(relPath), entry.Name())
		out = append(out, localFileInfo(child, info))
	}
	return out, nil
}

// localFileInfo converts an os.FileInfo into a transport FileInfo using a
// forward-slash relative path.
func localFileInfo(rel string, info os.FileInfo) FileInfo {
	normalized := strings.ReplaceAll(rel, "\\", "/")
	return FileInfo{
		Path:    normalized,
		Size:    info.Size(),
		ModTime: info.ModTime(),
		IsDir:   info.IsDir(),
		Mode:    info.Mode(),
	}
}

// copyFilePreserve copies src to dst, preserving permission bits and modtime.
func copyFilePreserve(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file %s: %w", src, err)
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat source file %s: %w", src, err)
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("failed to create destination file %s: %w", dst, err)
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("failed to copy %s to %s: %w", src, dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("failed to finalize %s: %w", dst, err)
	}

	// Preserve permission bits and modification time for delta detection.
	if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
		return fmt.Errorf("failed to set mode on %s: %w", dst, err)
	}
	if err := os.Chtimes(dst, info.ModTime(), info.ModTime()); err != nil {
		return fmt.Errorf("failed to set modtime on %s: %w", dst, err)
	}

	return nil
}
