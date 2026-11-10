package cmd

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"

	"make-sync/internal/config"
	"make-sync/internal/sshclient"
	"make-sync/internal/syncdata"
	"make-sync/internal/util"

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

type shellKind int

const (
	shellPOSIX shellKind = iota
	shellWindows
)

var (
	posixShellMissingRE     = regexp.MustCompile(`(?i)^(?:bash|sh|/[^:[:space:]]*/(?:bash|sh)):\s*(?:command not found|not found)$`)
	posixWrapperMissingRE   = regexp.MustCompile(`(?i)^(?:bash|sh|/[^:[:space:]]*/(?:bash|sh)):\s*(?:[0-9]+:\s*)?(?:line [0-9]+:\s*)?(?:bash|/[^:[:space:]]*/bash):\s*(?:command not found|not found)$`)
	posixWindowsMissingRE   = regexp.MustCompile(`(?i)^(?:'?(?:bash|sh)(?:\.exe)?'?) is not recognized as an internal or external command(?:,.*)?$`)
	windowsShellMissingRE   = regexp.MustCompile(`(?i)^(?:'cmd(?:\.exe)?'|"cmd(?:\.exe)?"|cmd(?:\.exe)?) is not recognized as an internal or external command(?:,.*)?$`)
	windowsWrapperMissingRE = regexp.MustCompile(`(?i)^(?:bash|sh|/[^:[:space:]]*/(?:bash|sh)):\s*(?:[0-9]+:\s*)?(?:line [0-9]+:\s*)?(?:cmd(?:\.exe)?|/[^:[:space:]]*/cmd(?:\.exe)?):\s*(?:command not found|not found)$`)
)

// isShellUnavailable reports whether stderr explicitly says that the wrapper
// shell used for this candidate is unavailable. It intentionally rejects
// ordinary command-not-found diagnostics such as "bash: docker: command not found".
func isShellUnavailable(stderr string, attempted shellKind) bool {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch attempted {
		case shellPOSIX:
			if posixShellMissingRE.MatchString(line) || posixWrapperMissingRE.MatchString(line) || posixWindowsMissingRE.MatchString(line) {
				return true
			}
		case shellWindows:
			if windowsShellMissingRE.MatchString(line) || windowsWrapperMissingRE.MatchString(line) {
				return true
			}
		}
	}
	return false
}

var execCmd = &cobra.Command{
	Use:   "exec",
	Short: "Execute a command on the remote host",
	Long:  "Execute arbitrary shell commands on the remote host (non-PTY). Streams output and returns remote exit status (non-zero on failure).",
	// Treat everything after 'exec' as the remote command; do not parse local flags
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "Usage: make-sync exec [-it] <command...>")
			os.Exit(1)
			return
		}

		// Detect interactive mode. Supports BOTH:
		//  1. Explicit leading flag: make-sync exec -it <command...>  (or --it / -i)
		//  2. Auto-detect: the command clearly wants a shell/TTY (e.g. ends with
		//     `bash`/`sh`/`zsh` or contains `-it`/`-ti`/`-t`), which is exactly the
		//     "type directly into the server" behavior the user wants. For example
		//     `docker compose exec -it app bash` should open an interactive PTY
		//     automatically, while `bash -c 'echo hi'` stays a one-shot command.
		//
		// Because DisableFlagParsing is true, cobra hands us everything verbatim,
		// so we strip leading interactive flags manually and keep the rest as the
		// command to run.
		restArgs, interactive := splitInteractiveFlags(args)

		if !interactive && wantsInteractiveShell(restArgs) {
			interactive = true
		}

		// Load config
		if !config.ConfigExists() {
			fmt.Fprintln(os.Stderr, "Config file not found. Run 'make-sync init' first.")
			os.Exit(1)
			return
		}
		cfg, err := config.LoadAndRenderConfigQuiet()
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to load config: %v\n", err)
			os.Exit(1)
			return
		}

		// Determine remote working directory
		remotePath := strings.TrimSpace(cfg.Devsync.Auth.RemotePath)

		// --- Teruskan mentah (forward raw) ---
		// Teks setelah 'exec' diperlakukan persis seperti diketik langsung ke
		// terminal server: operator shell (`|`, `>`, `>>`, `<`, `&&`, `||`, `;`),
		// variabel, backtick, dan kutipan diinterpretasikan oleh shell remote.
		//
		// Sumber teks mentah: argumen proses asli (os.Args di POSIX,
		// wincmdline.WindowsRawArgs() di Windows) yang dibaca tepat setelah token
		// `exec`, sehingga struktur yang diketik user tidak di-escape ulang oleh
		// make-sync. Bila raw tidak tersedia, kita jatuh ke
		// strings.Join(restArgs, " ") sebagai teks command.
		//
		// Catatan: kami TIDAK lagi memakai base64 + `bash -s`. Uji nyata
		// menunjukkan `bash -s` (script via stdin) gagal untuk `'bash -c echo hi'`
		// dan `'ls && echo done'` karena seluruh perintah dibungkus petik tunggal
		// menjadi satu kata (command not found). Sebaliknya `bash -lc '<raw>'`
		// memenuhi persis perilaku "seperti akses server langsung".
		rawCmd := rawCommandText(restArgs)

		// Build both POSIX and Windows variants so we can run regardless of remote OS
		buildPosix := func() string {
			return buildPosixExecCommand(rawCmd, remotePath)
		}
		buildWindows := func() string {
			return buildWindowsExecCommand(rawCmd, remotePath)
		}

		// Prefer order based on config, but fall back to the other if the shell is unavailable.
		// If user wants to ignore os_target, we still use it only as a hint for the first try.
		osTarget := strings.ToLower(strings.TrimSpace(cfg.Devsync.OSTarget))
		tryOrder := []string{buildPosix(), buildWindows()}
		shellKinds := []shellKind{shellPOSIX, shellWindows}
		if strings.Contains(osTarget, "win") {
			tryOrder = []string{buildWindows(), buildPosix()}
			shellKinds = []shellKind{shellWindows, shellPOSIX}
		}

		// Connect SSH
		sshCli, err := syncdata.ConnectSSH(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ SSH connect failed: %v\n", err)
			os.Exit(1)
			return
		}

		// --- Interactive mode (exec -it) ---
		// Gives the user a real PTY bridge: local stdin is forwarded to the remote
		// shell so they can `docker compose exec -it app bash` and interact like a
		// direct terminal. Falls back through Pause/Resume/Close automatically.
		if interactive {
			oldState, err := util.NewRaw()
			if err != nil {
				fmt.Fprintf(os.Stderr, "❌ Failed to enter raw terminal mode: %v\n", err)
				sshCli.Close()
				os.Exit(1)
				return
			}

			// For interactive PTY we forward the same raw command text so the
			// remote shell interprets quoting and operators naturally.
			userCmd := strings.TrimSpace(rawCmd)
			// Build the command that lands in remotePath (and runs the user's command if any).
			var initCmd string
			switch {
			case remotePath != "" && userCmd != "":
				initCmd = "cd " + shellEscapePOSIX(remotePath) + " && " + userCmd
			case remotePath != "" && userCmd == "":
				initCmd = "cd " + shellEscapePOSIX(remotePath) + " && exec bash -l"
			case userCmd != "":
				initCmd = userCmd
			default:
				initCmd = "exec bash -l"
			}

			fmt.Fprintf(os.Stdout, "🔗 Interactive shell (make-sync exec). Type 'exit' to quit.\n")
			bridge, err := sshclient.NewPTYSSHBridgeWithCommand(sshCli, initCmd)
			if err != nil {
				fmt.Fprintf(os.Stderr, "❌ Failed to create PTY bridge: %v\n", err)
				util.ResetRaw(oldState)
				sshCli.Close()
				os.Exit(1)
				return
			}

			// Mark the shell as done/closed on exit so we can restore the terminal
			// cleanly after the user exits the remote shell.
			bridge.SetOnExitListener(func() {
				// nothing extra needed here; cleanup happens after StartInteractiveShell returns
			})

			if err := bridge.StartInteractiveShell(); err != nil {
				fmt.Fprintf(os.Stderr, "❌ Interactive shell ended with error: %v\n", err)
			}
			bridge.Close()
			sshCli.Close()
			util.ResetRaw(oldState)
			return
		}

		defer sshCli.Close()

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

			// Only retry when stderr identifies the wrapper shell attempted for this
			// candidate as unavailable; command-level failures must stop here.
			if i < len(tryOrder)-1 && isShellUnavailable(stderrBuf.String(), shellKinds[i]) {
				continue
			}

			// terminal failure
			os.Exit(1)
			return
		}
	},
}

// wantsInteractiveShell reports whether the given command clearly asks for an
// interactive shell / TTY. It returns true when:
//   - the command contains an explicit interactive flag like `-it`, `-ti`, or `-t`
//     (e.g. `docker compose exec -it app bash`), OR
//   - the command ends with a bare interactive shell binary (`bash`, `sh`, `zsh`)
//     with no `-c`/`-lc` (so `bash -c 'echo hi'` is correctly treated as one-shot).
//
// This gives the "type directly into the server" experience: when the user writes
// something that needs a TTY, we automatically open a PTY bridge.
func wantsInteractiveShell(args []string) bool {
	cmdText := strings.Join(args, " ")

	// Bail out for obvious one-shot forms first.
	// `bash -c '...'`, `bash -lc '...'`, `-l -c`, `sh -c`, etc.
	for _, w := range strings.Fields(cmdText) {
		switch w {
		case "-c", "-lc", "-l -c":
			return false
		}
	}

	// Explicit interactive flag present somewhere in the command.
	if strings.Contains(cmdText, " -it ") ||
		strings.Contains(cmdText, " -ti ") ||
		strings.HasSuffix(cmdText, " -it") ||
		strings.HasSuffix(cmdText, " -ti") ||
		strings.Contains(cmdText, " -t ") ||
		strings.HasSuffix(cmdText, " -t") {
		return true
	}

	// Ends with a bare interactive shell binary (no -c). e.g. "... app bash"
	last := ""
	if len(args) > 0 {
		last = strings.TrimSpace(args[len(args)-1])
	}
	switch last {
	case "bash", "sh", "zsh", "bash -l", "sh -l", "zsh -l", "bash --login", "sh --login":
		// The single-token cases are covered by the switch; the " -l" variants
		// arrive as a single token when quoted ("bash -l") so include them above.
		return true
	}

	return false
}

// execSubcommandName is the token that separates the local make-sync arguments
// from the raw remote command inside the process argument vector.
const execSubcommandName = "exec"

// splitInteractiveFlags removes leading interactive-mode flags (e.g. `-it`)
// from args and reports whether any were present. Because DisableFlagParsing is
// enabled, cobra hands us the raw arguments; we strip these flags manually so
// they are not forwarded to the remote shell.
func splitInteractiveFlags(args []string) (rest []string, interactive bool) {
	for len(args) > 0 {
		switch args[0] {
		case "-it", "--it", "-i", "--interactive":
			interactive = true
			args = args[1:]
		default:
			return args, interactive
		}
	}
	return nil, interactive
}

// rawCommandText returns the raw command text the user typed after the `exec`
// token, preserving shell operators (`|`, `>`, `>>`, `<`, `&&`, `||`, `;`),
// variables, backticks, and quoting so the remote shell interprets them
// naturally.
//
// It prefers the OS-provided raw argv (os.Args on POSIX, the native Windows
// command line on Windows) and falls back to joining the already-parsed
// restArgs when no raw text is available.
func rawCommandText(restArgs []string) string {
	return rawCommandTextFromArgv(rawArgv(), restArgs)
}

// rawCommandTextFromArgv is the pure, testable core of rawCommandText: it
// returns the raw command text found after the `exec` token in argv, or the
// joined restArgs when argv carries no usable raw command.
func rawCommandTextFromArgv(argv, restArgs []string) string {
	if tail, ok := rawTailFromArgv(argv, restArgs); ok {
		if raw := strings.Join(tail, " "); strings.TrimSpace(raw) != "" {
			return raw
		}
	}
	return strings.Join(restArgs, " ")
}

// rawCommandFromArgv extracts the command text that follows the `exec` token in
// argv, dropping any leading interactive flags. It returns "" when argv does
// not contain an `exec` token so callers can fall back to parsed args.
func rawCommandFromArgv(argv []string) string {
	tail, ok := rawTailFromArgv(argv, nil)
	if !ok {
		return ""
	}
	return strings.Join(tail, " ")
}

// rawTailFromArgv returns the argv tokens after the `exec` subcommand.
//
// Several candidate `exec` positions may exist (a user command can itself
// contain the word `exec`, and a global flag value could be "exec"), so when
// restArgs is provided we prefer the candidate whose remaining tokens match
// restArgs exactly. Otherwise the first candidate is used, which is the normal
// `<program> exec <command...>` layout. ok is false when there is no `exec`
// token at all.
func rawTailFromArgv(argv, restArgs []string) (tail []string, ok bool) {
	var candidates []int
	for i, a := range argv {
		if a == execSubcommandName {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		return nil, false
	}

	if restArgs != nil {
		for _, idx := range candidates {
			candidate, _ := splitInteractiveFlags(argv[idx+1:])
			if sameArgs(candidate, restArgs) {
				return candidate, true
			}
		}
	}

	first, _ := splitInteractiveFlags(argv[candidates[0]+1:])
	return first, true
}

// sameArgs reports whether a and b hold the same tokens in the same order.
func sameArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// buildPosixExecCommand builds the remote POSIX command that runs the user's
// raw command text. The raw text is forwarded verbatim (operators are NOT
// escaped) so the remote `bash -lc` interprets it exactly like direct typing.
// Only the injected remotePath prefix is escaped.
func buildPosixExecCommand(rawCmd, remotePath string) string {
	combined := rawCmd
	if remotePath != "" {
		combined = "cd " + shellEscapePOSIX(remotePath) + " && " + rawCmd
	}
	return "bash -lc " + shellEscapePOSIX(combined)
}

// buildWindowsExecCommand builds the remote Windows command that runs the
// user's raw command text via cmd.exe /C. The raw text is forwarded verbatim,
// without re-quoting shell operators; only the injected remotePath is quoted.
func buildWindowsExecCommand(rawCmd, remotePath string) string {
	if remotePath != "" {
		return "cmd.exe /C cd /d " + quoteWindows(remotePath) + " && " + rawCmd
	}
	return "cmd.exe /C " + rawCmd
}
