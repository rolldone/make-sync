package cmd

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"

	"make-sync/internal/config"
	"make-sync/internal/syncdata"

	"github.com/spf13/cobra"
)

// shellEscapePOSIX escapes a string for safe inclusion inside single quotes
// for a POSIX shell (e.g., bash -lc '...').
func shellEscapePOSIX(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// quoteWindows wraps a string in double quotes for cmd.exe and escapes embedded quotes by doubling them.
// In cmd.exe, the way to include a literal double quote inside a quoted string is to double it: "".
func quoteWindows(s string) string {
	return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
}

var execCmd = &cobra.Command{
	Use:   "exec",
	Short: "Execute a command on the remote host",
	Long:  "Execute arbitrary shell commands on the remote host (non-PTY). Streams output and returns remote exit status (non-zero on failure).",
	// Treat everything after 'exec' as the remote command; do not parse local flags
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "Usage: make-sync exec <command...>")
			os.Exit(1)
			return
		}

		// Load config
		if !config.ConfigExists() {
			fmt.Fprintln(os.Stderr, "Config file not found. Run 'make-sync init' first.")
			os.Exit(1)
			return
		}
		cfg, err := config.LoadAndRenderConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to load config: %v\n", err)
			os.Exit(1)
			return
		}

		// Determine remote working directory
		remotePath := strings.TrimSpace(cfg.Devsync.Auth.RemotePath)
		if remotePath == "" {
			remotePath = strings.TrimSpace(cfg.RemotePath)
		}

		// Build raw command to execute
		rawCmd := strings.Join(args, " ")

		// Build both POSIX and Windows variants so we can run regardless of remote OS
		buildPosix := func() string {
			if remotePath != "" {
				combined := fmt.Sprintf("cd %s && %s", shellEscapePOSIX(remotePath), rawCmd)
				return fmt.Sprintf("bash -lc %s", shellEscapePOSIX(combined))
			}
			return fmt.Sprintf("bash -lc %s", shellEscapePOSIX(rawCmd))
		}
		buildWindows := func() string {
			if remotePath != "" {
				return fmt.Sprintf("cmd.exe /C cd /d %s && %s", quoteWindows(remotePath), rawCmd)
			}
			return fmt.Sprintf("cmd.exe /C %s", rawCmd)
		}

		// Prefer order based on config, but fall back to the other if the shell is unavailable.
		// If user wants to ignore os_target, we still use it only as a hint for the first try.
		osTarget := strings.ToLower(strings.TrimSpace(cfg.Devsync.OSTarget))
		tryOrder := []string{buildPosix(), buildWindows()}
		if strings.Contains(osTarget, "win") {
			tryOrder = []string{buildWindows(), buildPosix()}
		}

		// Connect SSH
		sshCli, err := syncdata.ConnectSSH(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ SSH connect failed: %v\n", err)
			os.Exit(1)
			return
		}
		defer sshCli.Close()

		// Heuristic matcher for "shell not found" so we can transparently try the other OS style
		shellMissingRe := regexp.MustCompile(`(?i)(bash|sh|cmd\.exe|cmd)(:|\sis\snot\srecognized|: not found)`) // covers common messages

		var lastErr bytes.Buffer
		for i, candidate := range tryOrder {
			// Stream output
			outCh, errCh, err := sshCli.RunCommandWithStream(candidate, false)
			if err != nil {
				// start failed, try next if available
				fmt.Fprintf(&lastErr, "start failed: %v\n", err)
				if i < len(tryOrder)-1 {
					continue
				}
				fmt.Fprintf(os.Stderr, "❌ Failed to start remote command: %v\n", err)
				os.Exit(1)
				return
			}

			hadFailure := false
			// fan-in loop
			doneOut := false
			doneErr := false
			var stderrBuf strings.Builder
			for !(doneOut && doneErr) {
				select {
				case s, ok := <-outCh:
					if !ok {
						doneOut = true
						outCh = nil
						continue
					}
					// write stdout directly
					fmt.Fprint(os.Stdout, s)
				case e, ok := <-errCh:
					if !ok {
						doneErr = true
						errCh = nil
						continue
					}
					// Distinguish stderr vs lifecycle errors by prefix
					msg := e.Error()
					if strings.HasPrefix(msg, "stderr: ") {
						txt := strings.TrimPrefix(msg, "stderr: ")
						stderrBuf.WriteString(txt)
						fmt.Fprint(os.Stderr, txt)
					} else {
						// lifecycle/start/wait errors → mark failure
						hadFailure = true
						stderrBuf.WriteString(msg)
						fmt.Fprintln(os.Stderr, msg)
					}
				}
			}

			if !hadFailure {
				// success
				return
			}

			// If shell seems missing, try next candidate; otherwise stop here
			if i < len(tryOrder)-1 && shellMissingRe.MatchString(stderrBuf.String()) {
				continue
			}

			// terminal failure
			os.Exit(1)
			return
		}
	},
}
