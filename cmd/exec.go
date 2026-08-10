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
		interactive := false
		var restArgs []string
		for len(args) > 0 {
			switch args[0] {
			case "-it", "--it", "-i", "--interactive":
				interactive = true
				args = args[1:]
			default:
				restArgs = args
				args = nil
			}
		}

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
		// User memilih: teks setelah 'exec' diperlakukan persis seperti diketik
		// langsung ke terminal server (bebas pakai petik tunggal/ganda, $var,
		// perintah majemuk, dll). Cobra dengan DisableFlagParsing memberi kita
		// args yang nilainya sudah dipecah oleh shell lokal (petik asli dibuang,
		// nilai arg sudah benar). Kita gabung dengan spasi menjadi satu baris
		// perintah, lalu serahkan ke remote `bash -lc '<baris>'`. Bash remote-lah
		// yang menginterpretasi petik/variabel/operator secara natural.
		//
		// Catatan: kami TIDAK lagi memakai base64 + `bash -s`. Uji nyata
		// menunjukkan `bash -s` (script via stdin) gagal untuk `'bash -c echo hi'`
		// dan `'ls && echo done'` karena seluruh perintah dibungkus petik tunggal
		// menjadi satu kata (command not found). Sebaliknya `bash -lc '<raw>'`
		// memenuhi persis perilaku "seperti akses server langsung".
		rawCmd := strings.Join(restArgs, " ")

		// Build both POSIX and Windows variants so we can run regardless of remote OS
		buildPosix := func() string {
			// bash -lc '<combined>' — seluruh baris dibungkus petik tunggal aman via
			// shellEscapePOSIX. Bash remote-lah yang menginterpretasi petik/variabel
			// yang diketik user, persis seperti terminal langsung.
			combined := rawCmd
			if remotePath != "" {
				combined = "cd " + shellEscapePOSIX(remotePath) + " && " + rawCmd
			}
			return "bash -lc " + shellEscapePOSIX(combined)
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

			userCmd := strings.TrimSpace(strings.Join(restArgs, " "))
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
