package transport

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// LocalClient implements Client by operating directly on the local filesystem
// and a local shell. It is used when devsync.auth.mode is "local": paths are
// already absolute paths on this machine (the config layer maps RemotePath onto
// localTargetPath), so no path translation is required here.
type LocalClient struct {
	root string
	// shell is the interpreter used for RunCommand variants.
	shell      string
	shellArgs  []string
	closeDummy bool
}

// NewLocalClient creates a LocalClient rooted at root. The root is created when
// missing. Commands run through the host shell (sh on POSIX, cmd on Windows).
func NewLocalClient(root string) (*LocalClient, error) {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return nil, fmt.Errorf("local client root cannot be empty")
	}
	abs, err := filepath.Abs(filepath.Clean(trimmed))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve local client root: %w", err)
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		return nil, fmt.Errorf("failed to create local client root %s: %w", abs, err)
	}

	lc := &LocalClient{root: abs}
	if isWindows() {
		lc.shell = "cmd"
		lc.shellArgs = []string{"/C"}
	} else {
		lc.shell = "sh"
		lc.shellArgs = []string{"-c"}
	}
	return lc, nil
}

// isWindows reports whether the current process runs on Windows.
func isWindows() bool {
	return runtime.GOOS == "windows"
}

// Root returns the absolute root directory.
func (c *LocalClient) Root() string { return c.root }

func (c *LocalClient) Connect() error { return nil }
func (c *LocalClient) Close() error   { return nil }

// SyncFile copies localPath to destPath (parent directories are created).
func (c *LocalClient) SyncFile(localPath, destPath string) error {
	return copyPreserving(localPath, destPath)
}

// UploadFile copies localPath to destPath.
func (c *LocalClient) UploadFile(localPath, destPath string) error {
	return copyPreserving(localPath, destPath)
}

// UploadFileSCP behaves like UploadFile: there is no separate backend locally.
func (c *LocalClient) UploadFileSCP(localPath, destPath string) error {
	return copyPreserving(localPath, destPath)
}

// DownloadFile copies srcPath to localPath (parent directories are created).
func (c *LocalClient) DownloadFile(localPath, srcPath string) error {
	return copyPreserving(srcPath, localPath)
}

// UploadFilesSFTP copies pairs concurrently and returns the local sources that
// were copied successfully (mirrors the SSHClient contract).
func (c *LocalClient) UploadFilesSFTP(pairs []UploadPair, concurrency int) ([]string, error) {
	return c.copyPairs(pairs, concurrency, func(p UploadPair) (string, string, error) {
		return p.Local, p.Remote, copyPreserving(p.Local, p.Remote)
	})
}

// DownloadFilesSFTP copies pairs concurrently and returns the local
// destinations that were written successfully (mirrors the SSHClient contract).
func (c *LocalClient) DownloadFilesSFTP(pairs []UploadPair, concurrency int) ([]string, error) {
	return c.copyPairs(pairs, concurrency, func(p UploadPair) (string, string, error) {
		return p.Local, p.Remote, copyPreserving(p.Remote, p.Local)
	})
}

// copyPairs runs copy for every pair with bounded concurrency.
func (c *LocalClient) copyPairs(pairs []UploadPair, concurrency int, run func(UploadPair) (string, string, error)) ([]string, error) {
	if concurrency <= 0 {
		concurrency = 5
	}

	var (
		mu        sync.Mutex
		succeeded []string
		errs      []string
		wg        sync.WaitGroup
	)
	sem := make(chan struct{}, concurrency)

	for _, p := range pairs {
		wg.Add(1)
		sem <- struct{}{}
		go func(p UploadPair) {
			defer wg.Done()
			defer func() { <-sem }()

			okPath, target, err := run(p)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", target, err))
				return
			}
			succeeded = append(succeeded, okPath)
		}(p)
	}
	wg.Wait()

	if len(errs) > 0 {
		return succeeded, fmt.Errorf("%d of %d operations failed: %s", len(errs), len(pairs), strings.Join(errs, "; "))
	}
	return succeeded, nil
}

// RunCommand executes cmd through the host shell, discarding output.
func (c *LocalClient) RunCommand(cmd string) error {
	return c.buildCmd(cmd).Run()
}

// RunCommandWithOutput executes cmd and returns its combined output.
func (c *LocalClient) RunCommandWithOutput(cmd string) (string, error) {
	out, err := c.buildCmd(cmd).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("command failed: %v", err)
	}
	return string(out), nil
}

// RunCommandWithStream executes cmd and streams stdout and stderr.
func (c *LocalClient) RunCommandWithStream(cmd string, usePty bool) (<-chan string, <-chan error, error) {
	command := c.buildCmd(cmd)

	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get stdout pipe: %v", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get stderr pipe: %v", err)
	}
	if err := command.Start(); err != nil {
		return nil, nil, fmt.Errorf("failed to start command: %v", err)
	}

	outCh := make(chan string, 128)
	errCh := make(chan error, 8)

	var wg sync.WaitGroup
	wg.Add(3)

	go streamReader(stdout, &wg, func(b []byte) {
		select {
		case outCh <- string(b):
		default:
		}
	})

	go streamReader(stderr, &wg, func(b []byte) {
		select {
		case errCh <- fmt.Errorf("stderr: %s", string(b)):
		default:
		}
	})

	go func() {
		defer wg.Done()
		if err := command.Wait(); err != nil {
			select {
			case errCh <- fmt.Errorf("command finished with error: %v", err):
			default:
			}
		}
	}()

	go func() {
		wg.Wait()
		close(outCh)
		close(errCh)
	}()

	return outCh, errCh, nil
}

// streamReader copies r into onChunk until EOF.
func streamReader(r io.Reader, wg *sync.WaitGroup, onChunk func([]byte)) {
	defer wg.Done()
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			onChunk(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// HasSession always reports true for local mode.
func (c *LocalClient) HasSession() bool { return true }

// StartPersistentSession is a no-op for local mode.
func (c *LocalClient) StartPersistentSession() error { return nil }

// StopAgentSession is a no-op for local mode.
func (c *LocalClient) StopAgentSession() error { return nil }

// IsPersistent reports false: local mode has no connection to keep alive.
func (c *LocalClient) IsPersistent() bool { return false }

// AbortAllSCPTransfers is a no-op for local mode.
func (c *LocalClient) AbortAllSCPTransfers() {}

// buildCmd wraps cmd into an exec.Cmd running the host shell in the root dir.
func (c *LocalClient) buildCmd(cmd string) *exec.Cmd {
	args := append(append([]string{}, c.shellArgs...), cmd)
	ec := exec.Command(c.shell, args...)
	ec.Dir = c.root
	return ec
}

// copyPreserving copies src to dst, creating parents and preserving mode and
// modification time so delta detection stays accurate.
func copyPreserving(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source %s: %w", src, err)
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat source %s: %w", src, err)
	}
	if info.IsDir() {
		return fmt.Errorf("source is a directory: %s", src)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("failed to create parent of %s: %w", dst, err)
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("failed to create destination %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("failed to copy %s to %s: %w", src, dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("failed to finalize %s: %w", dst, err)
	}

	if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
		return fmt.Errorf("failed to set mode on %s: %w", dst, err)
	}
	if err := os.Chtimes(dst, info.ModTime(), info.ModTime()); err != nil {
		return fmt.Errorf("failed to set modtime on %s: %w", dst, err)
	}
	return nil
}
