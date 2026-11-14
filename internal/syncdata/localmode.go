package syncdata

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"make-sync/internal/config"
	"make-sync/internal/util"
)

// isLocalMode reports whether cfg selects the local transport
// (devsync.auth.mode: local).
func isLocalMode(cfg *config.Config) bool {
	return cfg != nil && cfg.Devsync.Auth.IsLocal()
}

// runLocalAgentIndexing runs the agent binary directly on this machine with the
// local target directory as working directory.
//
// It is the local-mode counterpart of RemoteRunAgentIndexing: mode local has no
// remote host, so the index of the target is produced by executing the already
// built agent binary locally instead of uploading it and running it over SSH.
// The agent stores indexing_files.db inside the target's own .sync_temp, which
// is exactly where DownloadIndexDB looks for it afterwards. Nothing is copied
// into the target: only the DB directory is created.
func runLocalAgentIndexing(cfg *config.Config, agentBinary string, bypassIgnore bool, prefixes []string) (string, error) {
	targetRoot := strings.TrimSpace(cfg.Devsync.Auth.RemoteRoot())
	if targetRoot == "" {
		return "", fmt.Errorf("devsync.auth.localTargetPath is empty")
	}
	absTarget, err := filepath.Abs(targetRoot)
	if err != nil {
		return "", fmt.Errorf("failed to resolve local target %s: %v", targetRoot, err)
	}
	if _, err := os.Stat(absTarget); err != nil {
		return "", fmt.Errorf("local target does not exist: %s", absTarget)
	}

	binaryPath, err := filepath.Abs(strings.TrimSpace(agentBinary))
	if err != nil {
		return "", fmt.Errorf("failed to resolve agent binary %s: %v", agentBinary, err)
	}
	if _, err := os.Stat(binaryPath); err != nil {
		return "", fmt.Errorf("agent binary not found: %s", binaryPath)
	}

	// The agent writes indexing_files.db to <cwd>/.sync_temp (its executable is
	// not inside a .sync_temp directory here), so make sure that directory
	// exists before running it.
	syncTempDir := filepath.Join(absTarget, ".sync_temp")
	if err := os.MkdirAll(syncTempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create %s: %v", syncTempDir, err)
	}

	// The agent reads <target>/.sync_temp/config.json for the authoritative
	// ignore list. In local mode it is written directly instead of uploaded over
	// SSH, so .sync_ignore files and ignore collections behave identically in
	// both modes.
	if cfgJSON, _, cerr := buildAgentConfigJSON(cfg); cerr == nil {
		if werr := os.WriteFile(filepath.Join(syncTempDir, "config.json"), cfgJSON, 0644); werr != nil {
			util.Default.Printf("⚠️  Failed to write agent config: %v\n", werr)
		}
	} else {
		util.Default.Printf("⚠️  Failed to render agent config: %v\n", cerr)
	}

	args := []string{"indexing"}
	if bypassIgnore {
		args = append(args, "--bypass-ignore")
	}
	if len(prefixes) > 0 {
		args = append(args, "--manual-transfer", strings.Join(prefixes, ","))
	}

	util.Default.Printf("🔍 Running local agent indexing in %s\n", absTarget)

	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = absTarget

	output, err := runCommandStreamingWithIdleTimeout(cmd, cfg.Devsync.IndexIdleTimeoutDuration(), "local indexing")
	if err != nil {
		return output, err
	}
	return output, nil
}

// runCommandStreamingWithIdleTimeout starts cmd, streams its combined output
// into the returned string, and aborts when no output arrives within idleTimeout
// (mirrors the SSH streaming path so long index runs are never killed as long as
// the agent keeps emitting its heartbeat).
func runCommandStreamingWithIdleTimeout(cmd *exec.Cmd, idleTimeout time.Duration, label string) (string, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to get stdout pipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("failed to get stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start %s: %v", label, err)
	}

	outCh := make(chan string, 64)
	errCh := make(chan error, 8)

	go func() {
		defer close(outCh)
		buf := make([]byte, 4096)
		for {
			n, rerr := stdout.Read(buf)
			if n > 0 {
				outCh <- string(buf[:n])
			}
			if rerr != nil {
				return
			}
		}
	}()

	go func() {
		defer close(errCh)
		buf := make([]byte, 4096)
		for {
			n, rerr := stderr.Read(buf)
			if n > 0 {
				errCh <- fmt.Errorf("%s", strings.TrimSpace(string(buf[:n])))
			}
			if rerr != nil {
				return
			}
		}
	}()

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	if idleTimeout <= 0 {
		idleTimeout = config.DefaultIndexIdleTimeout
	}

	var output strings.Builder
	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()

	reset := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(idleTimeout)
	}

	outClosed, errClosed := false, false
	for {
		select {
		case chunk, ok := <-outCh:
			if !ok {
				outClosed = true
			} else {
				output.WriteString(chunk)
				reset()
			}
		case serr, ok := <-errCh:
			if !ok {
				errClosed = true
			} else if serr != nil {
				// stderr is diagnostic: surface it but keep going
				util.Default.Printf("⚠️  %s: %v\n", label, serr)
				reset()
			}
		case werr := <-waitErr:
			// drain whatever is still buffered
			drainStream(outCh, &output)
			if werr != nil {
				return output.String(), fmt.Errorf("%s failed: %v", label, werr)
			}
			return output.String(), nil
		case <-timer.C:
			_ = cmd.Process.Kill()
			return output.String(), fmt.Errorf("%s timed out after %s without output", label, idleTimeout)
		}

		if outClosed && errClosed {
			// both streams done: fall through to wait for the exit status
			if werr := <-waitErr; werr != nil {
				return output.String(), fmt.Errorf("%s failed: %v", label, werr)
			}
			return output.String(), nil
		}
	}
}

// drainStream appends any remaining buffered chunks to output.
func drainStream(ch <-chan string, output *strings.Builder) {
	for {
		select {
		case chunk, ok := <-ch:
			if !ok {
				return
			}
			output.WriteString(chunk)
		default:
			return
		}
	}
}

// localTargetIndexDB resolves the index DB of the local target.
//
// The agent already wrote it inside <target>/.sync_temp, so no download is
// performed. When destFolder is not the target root the DB is copied into
// destFolder/.sync_temp/indexing_files_target.db so a target-side DB can never be
// confused with the source-side one (the "-target" suffix is the target marker).
func localTargetIndexDB(cfg *config.Config, destFolder string) (string, error) {
	targetRoot := strings.TrimSpace(cfg.Devsync.Auth.RemoteRoot())
	if targetRoot == "" {
		return "", fmt.Errorf("devsync.auth.localTargetPath is empty")
	}
	src := filepath.Join(targetRoot, ".sync_temp", "indexing_files.db")
	if _, err := os.Stat(src); err != nil {
		return "", fmt.Errorf("target index DB not found at %s: %v", src, err)
	}

	dest := strings.TrimSpace(destFolder)
	if dest == "" {
		dest = "."
	}
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return "", fmt.Errorf("failed to resolve destination %s: %v", dest, err)
	}
	absTarget, err := filepath.Abs(targetRoot)
	if err == nil && absTarget == absDest {
		// Destination is the target itself: the DB is already where it belongs.
		return src, nil
	}

	outDir := filepath.Join(absDest, ".sync_temp")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create %s: %v", outDir, err)
	}
	out := filepath.Join(outDir, "indexing_files_target.db")
	if err := copyFileLocal(src, out); err != nil {
		return "", fmt.Errorf("failed to store target index DB: %v", err)
	}
	util.Default.Printf("🗂️  Target index DB stored locally at %s\n", out)
	return out, nil
}

// copyFileLocal copies src to dst, creating parents and preserving the mode.
func copyFileLocal(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
