package sshforward

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"syscall"

	"make-sync/internal/util"
)

// ForwardSpec represents a single forward to create
type ForwardSpec struct {
	Name       string
	HostAlias  string // Host alias from direct_access.ssh_configs
	RemoteHost string // usually 127.0.0.1 on remote side
	RemotePort int
	LocalPort  int    // 0 means auto-allocate
	LocalHost  string // local bind address
	Protocol   string // tcp/udp
}

// ActiveForward represents a running ssh process for a set of forwards
type ActiveForward struct {
	Cmd      *exec.Cmd
	Host     string
	Forwards []ForwardSpec
	LogPath  string
}

// Runner manages multiple ActiveForwards
type Runner struct {
	mu      sync.Mutex
	actives []*ActiveForward
}

func NewRunner() *Runner { return &Runner{} }

// StartForwards starts ssh processes grouped by hostAlias. It returns started ActiveForward entries.
// ctx is used to cancel/stop processes.
func (r *Runner) StartForwards(ctx context.Context, specs []ForwardSpec) ([]*ActiveForward, error) {
	// group by HostAlias
	groups := map[string][]ForwardSpec{}
	for _, s := range specs {
		groups[s.HostAlias] = append(groups[s.HostAlias], s)
	}

	var started []*ActiveForward

	for hostAlias, fwdList := range groups {
		// build -L args
		var args []string
		args = append(args, "-v")
		for i := range fwdList {
			local := fwdList[i].LocalPort
			if local == 0 {
				// allocate ephemeral port
				p, err := util.GetFreeTCPPort(fwdList[i].LocalHost)
				if err != nil {
					// cleanup previously started
					r.stopMany(started)
					return nil, fmt.Errorf("failed allocate local port for %s:%d: %v", fwdList[i].HostAlias, fwdList[i].RemotePort, err)
				}
				local = p
			}
			// ensure local assigned back to spec for caller visibility
			fwdList[i].LocalPort = local

			// Build -L spec. If LocalHost provided, include it as bind address.
			if strings.TrimSpace(fwdList[i].LocalHost) != "" {
				args = append(args, "-L", fmt.Sprintf("%s:%d:%s:%d", fwdList[i].LocalHost, local, fwdList[i].RemoteHost, fwdList[i].RemotePort))
			} else {
				args = append(args, "-L", fmt.Sprintf("%d:%s:%d", local, fwdList[i].RemoteHost, fwdList[i].RemotePort))
			}
		}

		// essential flags: don't run remote command (-N), disable tty (-T), fail if forward fails
		args = append(args, "-N", "-T", "-o", "ExitOnForwardFailure=yes")

		// If a generated temporary SSH config exists at .sync_temp/.ssh/config, tell ssh to use it
		cfgPath := ".sync_temp/.ssh/config"
		if _, err := os.Stat(cfgPath); err == nil {
			args = append(args, "-F", cfgPath)
		}

		args = append(args, hostAlias)

		cmd := exec.CommandContext(ctx, "ssh", args...)

		// ensure .sync_temp logs dir exists
		logsDir := ".sync_temp"
		if err := os.MkdirAll(logsDir, 0755); err != nil {
			r.stopMany(started)
			return nil, fmt.Errorf("failed create logs dir: %v", err)
		}

		// create logfile per hostAlias
		logFileName := fmt.Sprintf("ssh-forward-%s-%d.log", sanitizeFileName(hostAlias), time.Now().Unix())
		logPath := filepath.Join(logsDir, logFileName)
		lf, err := os.Create(logPath)
		if err != nil {
			r.stopMany(started)
			return nil, fmt.Errorf("failed create log file: %v", err)
		}

		// capture stdout/stderr
		stderr, _ := cmd.StderrPipe()
		stdout, _ := cmd.StdoutPipe()

		if err := cmd.Start(); err != nil {
			lf.Close()
			r.stopMany(started)
			return nil, fmt.Errorf("failed start ssh for %s: %v", hostAlias, err)
		}

		// copy streams to logfile. By default do NOT stream to terminal.
		// Set env MAKE_SYNC_SSH_STREAM=1 (or true/yes) to stream to stdout/stderr as well.
		var stdoutWriter io.Writer = lf
		var stderrWriter io.Writer = lf
		if v := strings.ToLower(strings.TrimSpace(os.Getenv("MAKE_SYNC_SSH_STREAM"))); v == "1" || v == "true" || v == "yes" {
			stdoutWriter = io.MultiWriter(lf, os.Stdout)
			stderrWriter = io.MultiWriter(lf, os.Stderr)
		}

		go func() {
			defer lf.Sync()
			io.Copy(stdoutWriter, stdout)
		}()
		go func() {
			defer lf.Sync()
			io.Copy(stderrWriter, stderr)
		}()

		af := &ActiveForward{Cmd: cmd, Host: hostAlias, Forwards: fwdList, LogPath: logPath}
		r.mu.Lock()
		r.actives = append(r.actives, af)
		r.mu.Unlock()

		started = append(started, af)

		// verify ssh process is alive shortly after start. If it exited immediately
		// treat that as a failure and abort with log path for debugging.
		alive := false
		// Poll process existence using syscall.Kill(pid, 0)
		for i := 0; i < 15; i++ {
			if af.Cmd == nil || af.Cmd.Process == nil {
				break
			}
			pid := af.Cmd.Process.Pid
			err := syscall.Kill(pid, 0)
			if err == nil || err == syscall.EPERM {
				alive = true
				break
			}
			if err == syscall.ESRCH {
				// process does not exist -> exited
				// ensure logfile flushed
				_ = af.LogPath
				r.stopMany(started)
				return nil, fmt.Errorf("ssh process for %s exited immediately; see log: %s", hostAlias, af.LogPath)
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !alive {
			r.stopMany(started)
			return nil, fmt.Errorf("ssh process for %s did not become alive in time; see log: %s", hostAlias, af.LogPath)
		}
	}

	return started, nil
}

func (r *Runner) stopMany(list []*ActiveForward) {
	for _, a := range list {
		_ = a.Cmd.Process.Kill()
		a.Cmd.Wait()
	}
}

// sanitizeFileName makes a safe filename fragment from an arbitrary string
func sanitizeFileName(s string) string {
	// allow alnum, dot, dash, underscore; replace others with underscore
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		switch r {
		case '.', '-', '_':
			return r
		default:
			return '_'
		}
	}, s)
}

// StopAll stops all active forwards
func (r *Runner) StopAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.actives {
		_ = a.Cmd.Process.Kill()
		a.Cmd.Wait()
	}
	r.actives = nil
}
