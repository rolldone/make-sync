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

		// --- Re-quote argv (issue #2) ---
		// Shell lokal sudah menghapus quoting sebelum argv sampai ke make-sync,
		// jadi struktur token asli hanya bisa dipulihkan dengan me-re-quote tiap
		// argumen: arg ber-spasi/tab/kutipan dibungkus POSIX-safe ('...'),
		// sedangkan operator shell mandiri (`|`, `>`, `>>`, `<`, `&&`, `||`, `;`,
		// `&`) dan arg polos dibiarkan telanjang agar tetap diinterpretasikan
		// shell remote (menjaga fix issue #1).
		//
		// Token tunggal yang mengandung spasi dianggap satu baris command utuh
		// yang user ketik dalam satu kutipan lokal
		// (mis. make-sync exec 'echo hi > /tmp/x') dan diteruskan apa adanya.
		//
		// Catatan: kami TIDAK memakai base64 + `bash -s`. Uji nyata menunjukkan
		// `bash -s` (script via stdin) gagal karena seluruh perintah dibungkus
		// petik tunggal menjadi satu kata; `bash -lc '<combined>'` memenuhi
		// perilaku "seperti akses server langsung".
		argv := commandArgv(restArgs)
		posixCmd := requoteArgvPOSIX(argv)
		winCmd := requoteArgvWindows(argv)

		// Build both POSIX and Windows variants so we can run regardless of remote OS
		buildPosix := func() string {
			return buildPosixExecCommand(posixCmd, remotePath)
		}
		buildWindows := func() string {
			return buildWindowsExecCommand(winCmd, remotePath)
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

			// For interactive PTY we forward the same re-quoted command text so the
			// remote shell interprets quoting and operators naturally.
			userCmd := strings.TrimSpace(posixCmd)
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

// commandArgv returns the argv tokens that form the remote command.
//
// The tokens come from the OS-provided argv tail (os.Args on POSIX, the native
// Windows command line on Windows) because that is the most faithful view of
// how the local shell tokenized the input. When no raw tail is available we
// fall back to the already-parsed restArgs.
func commandArgv(restArgs []string) []string {
	if tail, ok := rawTailFromArgv(rawArgv(), restArgs); ok && len(tail) > 0 {
		return tail
	}
	return restArgs
}

// bareShellOperatorRE matches tokens that consist only of shell redirection or
// control operators, optionally with an fd prefix (e.g. `>`, `>>`, `|`, `&&`,
// `;`, `2>&1`). Such tokens must stay bare so the remote shell interprets them.
var bareShellOperatorRE = regexp.MustCompile(`^[0-9]*(?:[<>|&;][0-9&<>|;-]*)+$`)

// isBareShellOperator reports whether tok is a standalone shell operator token
// that must not be quoted.
func isBareShellOperator(tok string) bool {
	return tok != "" && bareShellOperatorRE.MatchString(tok)
}

// posixRequoteChars lists the characters that force a POSIX argument to be
// quoted: whitespace, quotes, and every metacharacter that would otherwise
// change tokenization, expansion, or word splitting on the remote shell.
const posixRequoteChars = " \t\n'\"\\$`*?[]{}~#!();|&<>"

// posixNeedsRequote reports whether a POSIX argument must be wrapped in single
// quotes so it survives the remote shell as exactly one word.
func posixNeedsRequote(s string) bool {
	if s == "" {
		// An empty argument must become '' to survive as an argument at all.
		return true
	}
	return strings.ContainsAny(s, posixRequoteChars)
}

// windowsNeedsRequote reports whether a Windows argument must be wrapped in
// double quotes so cmd.exe keeps it as a single argument. `%` and `!` are
// intentionally excluded: cmd.exe expands them even inside double quotes, so
// quoting would only add noise without protecting anything.
func windowsNeedsRequote(s string) bool {
	if s == "" {
		return true
	}
	return strings.ContainsAny(s, " \t\n\"&|<>^()")
}

// isFindExecTerminator reports whether args[i] is the `;` that terminates a
// find(1) -exec/-execdir primary. In that position the semicolon is data for
// find, not a command separator, so it must be quoted instead of staying bare.
// The local shell strips the user's `\;` before we see it, so this position is
// the only way to tell the two uses apart.
func isFindExecTerminator(args []string, i int) bool {
	if args[i] != ";" {
		return false
	}
	for j := i - 1; j >= 0; j-- {
		if args[j] == "-exec" || args[j] == "-execdir" {
			return true
		}
	}
	return false
}

// requoteArgvPOSIX rebuilds a remote POSIX command string from already-parsed
// argv tokens.
//
// Local shells strip quoting before argv reaches us, so argument boundaries can
// only be restored by re-quoting: tokens containing whitespace, quotes, or
// shell metacharacters are wrapped POSIX-safe via shellEscapePOSIX, while
// standalone operators and plain tokens stay bare so the remote shell still
// interprets `|`, `>`, `&&`, `;` naturally (issue #1).
//
// A single token that contains whitespace is treated as one whole command line
// typed inside one pair of local quotes (e.g. `exec 'echo hi > /tmp/x'`) and is
// forwarded verbatim, otherwise that documented usage would break.
func requoteArgvPOSIX(args []string) string {
	if len(args) == 1 && strings.ContainsAny(args[0], " \t\n") {
		return args[0]
	}
	parts := make([]string, 0, len(args))
	for i, a := range args {
		switch {
		case isBareShellOperator(a) && !isFindExecTerminator(args, i):
			parts = append(parts, a)
		case posixNeedsRequote(a):
			parts = append(parts, shellEscapePOSIX(a))
		default:
			parts = append(parts, a)
		}
	}
	return strings.Join(parts, " ")
}

// requoteArgvWindows rebuilds a remote Windows command string from
// already-parsed argv tokens using the equivalent strategy: quote only what
// cmd.exe would otherwise split or interpret, keep operators bare.
func requoteArgvWindows(args []string) string {
	if len(args) == 1 && strings.ContainsAny(args[0], " \t\n") {
		return args[0]
	}
	parts := make([]string, 0, len(args))
	for _, a := range args {
		switch {
		case isBareShellOperator(a):
			parts = append(parts, a)
		case windowsNeedsRequote(a):
			parts = append(parts, quoteWindows(a))
		default:
			parts = append(parts, a)
		}
	}
	return strings.Join(parts, " ")
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
// command text. Operators stay unescaped so the remote `bash -lc` interprets
// them exactly like direct typing; only the injected remotePath prefix is
// escaped.
func buildPosixExecCommand(cmdText, remotePath string) string {
	combined := cmdText
	if remotePath != "" {
		combined = "cd " + shellEscapePOSIX(remotePath) + " && " + cmdText
	}
	return "bash -lc " + shellEscapePOSIX(combined)
}

// buildWindowsExecCommand builds the remote Windows command that runs the
// user's command text via cmd.exe /C. Operators stay unescaped; only the
// injected remotePath is quoted.
func buildWindowsExecCommand(cmdText, remotePath string) string {
	if remotePath != "" {
		return "cmd.exe /C cd /d " + quoteWindows(remotePath) + " && " + cmdText
	}
	return "cmd.exe /C " + cmdText
}
