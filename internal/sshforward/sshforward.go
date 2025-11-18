package sshforward

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

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
		args = append(args, hostAlias)

		cmd := exec.CommandContext(ctx, "ssh", args...)

		// capture stdout/stderr to avoid blocking
		stderr, _ := cmd.StderrPipe()
		stdout, _ := cmd.StdoutPipe()

		if err := cmd.Start(); err != nil {
			r.stopMany(started)
			return nil, fmt.Errorf("failed start ssh for %s: %v", hostAlias, err)
		}

		// simple goroutine to drain pipes
		go io.Copy(io.Discard, stderr)
		go io.Copy(io.Discard, stdout)

		af := &ActiveForward{Cmd: cmd, Host: hostAlias, Forwards: fwdList}
		r.mu.Lock()
		r.actives = append(r.actives, af)
		r.mu.Unlock()

		started = append(started, af)

		// give a moment for ssh to establish; if process exits quickly treat as error
		go func(a *ActiveForward) {
			time.Sleep(500 * time.Millisecond)
			if a.Cmd.ProcessState != nil && a.Cmd.ProcessState.Exited() {
				// no-op here; caller should check cmd.Wait via returned ActiveForward if desired
			}
		}(af)
	}

	return started, nil
}

func (r *Runner) stopMany(list []*ActiveForward) {
	for _, a := range list {
		_ = a.Cmd.Process.Kill()
		a.Cmd.Wait()
	}
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
