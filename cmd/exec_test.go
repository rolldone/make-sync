package cmd

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestShellEscapePOSIX(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "'hello'"},
		{"it's", "'it'\\''s'"},
		{"", "''"},
		{"a b", "'a b'"},
	}
	for _, tt := range tests {
		got := shellEscapePOSIX(tt.input)
		if got != tt.want {
			t.Errorf("shellEscapePOSIX(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSplitInteractiveFlags(t *testing.T) {
	tests := []struct {
		name            string
		args            []string
		wantRest        []string
		wantInteractive bool
	}{
		{
			name:            "no flag",
			args:            []string{"echo", "hi"},
			wantRest:        []string{"echo", "hi"},
			wantInteractive: false,
		},
		{
			name:            "leading -it",
			args:            []string{"-it", "docker", "compose", "exec", "app", "bash"},
			wantRest:        []string{"docker", "compose", "exec", "app", "bash"},
			wantInteractive: true,
		},
		{
			name:            "stacked leading flags",
			args:            []string{"-it", "--interactive", "-i", "ls"},
			wantRest:        []string{"ls"},
			wantInteractive: true,
		},
		{
			name:            "only flags",
			args:            []string{"-it"},
			wantRest:        nil,
			wantInteractive: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rest, interactive := splitInteractiveFlags(tt.args)
			if interactive != tt.wantInteractive {
				t.Errorf("interactive = %t, want %t", interactive, tt.wantInteractive)
			}
			if !reflect.DeepEqual(rest, tt.wantRest) {
				t.Errorf("rest = %v, want %v", rest, tt.wantRest)
			}
		})
	}
}

func TestRawTailFromArgv(t *testing.T) {
	tests := []struct {
		name     string
		argv     []string
		restArgs []string
		want     []string
		wantOK   bool
	}{
		{
			name:   "redirect tokens preserved as separate tokens",
			argv:   []string{"make-sync", "exec", "echo", "hi", ">", "/tmp/f"},
			want:   []string{"echo", "hi", ">", "/tmp/f"},
			wantOK: true,
		},
		{
			name:   "leading interactive flag stripped, inner -it kept",
			argv:   []string{"make-sync", "exec", "-it", "docker", "compose", "exec", "-it", "app", "bash"},
			want:   []string{"docker", "compose", "exec", "-it", "app", "bash"},
			wantOK: true,
		},
		{
			name:     "global flag value exec prefers the candidate matching restArgs",
			argv:     []string{"make-sync", "--config", "exec", "exec", "ls", "-la"},
			restArgs: []string{"ls", "-la"},
			want:     []string{"ls", "-la"},
			wantOK:   true,
		},
		{
			name:   "no exec token",
			argv:   []string{"make-sync", "pull"},
			want:   nil,
			wantOK: false,
		},
		{
			name:   "exec token but empty command",
			argv:   []string{"make-sync", "exec"},
			want:   nil,
			wantOK: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := rawTailFromArgv(tt.argv, tt.restArgs)
			if ok != tt.wantOK {
				t.Fatalf("ok = %t, want %t", ok, tt.wantOK)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("rawTailFromArgv(%v, %v) = %v, want %v", tt.argv, tt.restArgs, got, tt.want)
			}
		})
	}
}

func TestIsBareShellOperator(t *testing.T) {
	bare := []string{">", ">>", "<", "<<", "|", "||", "&&", ";", "&", "2>", "2>&1", "1>>", "2>>"}
	quoted := []string{"", "a;b", "a|b", "echo", "-la", "--format={{.Image}}", "a>b", "1", "x&y"}
	for _, tok := range bare {
		if !isBareShellOperator(tok) {
			t.Errorf("isBareShellOperator(%q) = false, want true", tok)
		}
	}
	for _, tok := range quoted {
		if isBareShellOperator(tok) {
			t.Errorf("isBareShellOperator(%q) = true, want false", tok)
		}
	}
}

// TestRequoteArgvPOSIX is the issue #2 regression suite: quoted multi-word
// arguments must survive as a single token while operators stay interpretable.
func TestRequoteArgvPOSIX(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "issue 2: docker inspect --format with spaced template",
			args: []string{"docker", "inspect", "registry", "--format", "{{.Config.Image}} {{.State.Status}}"},
			want: "docker inspect registry --format '{{.Config.Image}} {{.State.Status}}'",
		},
		{
			name: "arg with double space is preserved",
			args: []string{"echo", "a b  c"},
			want: "echo 'a b  c'",
		},
		{
			name: "arg with tab is quoted",
			args: []string{"echo", "a\tb"},
			want: "echo 'a\tb'",
		},
		{
			name: "single quotes inside a double-quoted local arg",
			args: []string{"echo", "docker inspect --format '{{.Config.Image}}'"},
			want: "echo 'docker inspect --format '\\''{{.Config.Image}}'\\'''",
		},
		{
			name: "plain args stay bare",
			args: []string{"ls", "-la"},
			want: "ls -la",
		},
		{
			name: "standalone operators stay bare",
			args: []string{"echo", "hi", ">", "/tmp/f"},
			want: "echo hi > /tmp/f",
		},
		{
			name: "compound and pipeline stay bare",
			args: []string{"docker", "compose", "down", "&&", "docker", "compose", "up"},
			want: "docker compose down && docker compose up",
		},
		{
			name: "fd redirect operator stays bare",
			args: []string{"cmd", "2>&1"},
			want: "cmd 2>&1",
		},
		{
			name: "empty argument becomes empty quotes",
			args: []string{"echo", ""},
			want: "echo ''",
		},
		{
			name: "arg containing semicolon is quoted to stay literal",
			args: []string{"echo", "a;b"},
			want: "echo 'a;b'",
		},
		{
			name: "single token with spaces is a whole command line",
			args: []string{"echo hi > /tmp/f"},
			want: "echo hi > /tmp/f",
		},
		{
			name: "single plain token untouched",
			args: []string{"pwd"},
			want: "pwd",
		},
		{
			name: "mixed: quoted arg plus redirect plus compound",
			args: []string{"printf", "[%s]\\n", "{{.Image}} {{.State.Status}}", ">", "/tmp/out", "&&", "cat", "/tmp/out"},
			want: "printf '[%s]\\n' '{{.Image}} {{.State.Status}}' > /tmp/out && cat /tmp/out",
		},
		{
			name: "find -exec terminator is quoted (data, not separator)",
			args: []string{"find", "/tmp", "-maxdepth", "0", "-exec", "echo", "found", "{}", ";"},
			want: "find /tmp -maxdepth 0 -exec echo found '{}' ';'",
		},
		{
			name: "find -execdir terminator is quoted",
			args: []string{"find", ".", "-execdir", "ls", "{}", ";"},
			want: "find . -execdir ls '{}' ';'",
		},
		{
			name: "semicolon separator without -exec stays bare",
			args: []string{"echo", "a", ";", "echo", "b"},
			want: "echo a ; echo b",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := requoteArgvPOSIX(tt.args)
			if got != tt.want {
				t.Errorf("requoteArgvPOSIX(%v)\n  got  = %q\n  want = %q", tt.args, got, tt.want)
			}
			// A re-quoted command must be safe to embed in bash -lc '...'.
			if strings.Contains(got, "\n") {
				t.Errorf("re-quoted command must not contain a raw newline: %q", got)
			}
		})
	}
}

// TestRequoteArgvWindows covers the cmd.exe equivalent of the POSIX rules.
func TestRequoteArgvWindows(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "spaced arg is double quoted",
			args: []string{"docker", "inspect", "registry", "--format", "{{.Config.Image}} {{.State.Status}}"},
			want: `docker inspect registry --format "{{.Config.Image}} {{.State.Status}}"`,
		},
		{
			name: "plain args stay bare",
			args: []string{"dir", "/b"},
			want: "dir /b",
		},
		{
			name: "standalone operator stays bare",
			args: []string{"dir", "|", "findstr", "x"},
			want: "dir | findstr x",
		},
		{
			name: "embedded quote is doubled",
			args: []string{"echo", `say "hi"`},
			want: `echo "say ""hi"""`,
		},
		{
			name: "single token with spaces is a whole command line",
			args: []string{`dir | findstr x`},
			want: `dir | findstr x`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := requoteArgvWindows(tt.args); got != tt.want {
				t.Errorf("requoteArgvWindows(%v)\n  got  = %q\n  want = %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestCommandArgv(t *testing.T) {
	// restore os.Args so the real raw tail is not used by accident.
	orig := os.Args
	defer func() { os.Args = orig }()

	os.Args = []string{"make-sync", "exec", "printf", "%s", "a b"}
	restArgs := []string{"printf", "%s", "a b"}
	got := commandArgv(restArgs)
	if !reflect.DeepEqual(got, restArgs) {
		t.Errorf("commandArgv with raw tail = %v, want %v", got, restArgs)
	}

	// No exec token in argv -> fall back to restArgs.
	os.Args = []string{"make-sync", "pull"}
	got = commandArgv(restArgs)
	if !reflect.DeepEqual(got, restArgs) {
		t.Errorf("commandArgv fallback = %v, want %v", got, restArgs)
	}
}

func TestBuildPosixExecCommand(t *testing.T) {
	tests := []struct {
		name       string
		rawCmd     string
		remotePath string
		want       string
	}{
		{
			name:   "redirect forwarded verbatim",
			rawCmd: "echo hi > /tmp/f",
			want:   `bash -lc 'echo hi > /tmp/f'`,
		},
		{
			name:   "pipeline and compound not escaped",
			rawCmd: "cat /etc/hostname | head -1; echo ok",
			want:   `bash -lc 'cat /etc/hostname | head -1; echo ok'`,
		},
		{
			name:   "double ampersand preserved",
			rawCmd: "docker compose down && docker compose up",
			want:   `bash -lc 'docker compose down && docker compose up'`,
		},
		{
			name:       "remote path prefix escaped, command verbatim",
			rawCmd:     "echo hi > /tmp/f",
			remotePath: "/home/u/proj",
			want:       `bash -lc 'cd '\''/home/u/proj'\'' && echo hi > /tmp/f'`,
		},
		{
			name:   "single quotes in command escaped safely",
			rawCmd: "echo 'hi'",
			want:   `bash -lc 'echo '\''hi'\'''`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildPosixExecCommand(tt.rawCmd, tt.remotePath); got != tt.want {
				t.Errorf("buildPosixExecCommand(%q, %q)\n  got  = %q\n  want = %q", tt.rawCmd, tt.remotePath, got, tt.want)
			}
		})
	}
}

func TestBuildWindowsExecCommand(t *testing.T) {
	tests := []struct {
		name       string
		rawCmd     string
		remotePath string
		want       string
	}{
		{
			name:   "redirect forwarded verbatim",
			rawCmd: `echo hi > C:\tmp\f`,
			want:   `cmd.exe /C echo hi > C:\tmp\f`,
		},
		{
			name:       "remote path quoted, operators untouched",
			rawCmd:     `dir | findstr x`,
			remotePath: `C:\proj`,
			want:       `cmd.exe /C cd /d "C:\proj" && dir | findstr x`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildWindowsExecCommand(tt.rawCmd, tt.remotePath); got != tt.want {
				t.Errorf("buildWindowsExecCommand(%q, %q)\n  got  = %q\n  want = %q", tt.rawCmd, tt.remotePath, got, tt.want)
			}
		})
	}
}

func TestIsShellUnavailable(t *testing.T) {
	tests := []struct {
		name      string
		stderr    string
		attempted shellKind
		want      bool
	}{
		{
			name:      "bare bash missing",
			stderr:    "bash: command not found",
			attempted: shellPOSIX,
			want:      true,
		},
		{
			name:      "bash not found through sh wrapper",
			stderr:    "/bin/sh: 1: bash: not found",
			attempted: shellPOSIX,
			want:      true,
		},
		{
			name:      "bash path missing through sh wrapper",
			stderr:    "/bin/sh: 1: /usr/bin/bash: command not found",
			attempted: shellPOSIX,
			want:      true,
		},
		{
			name:      "bash unrecognized on Windows",
			stderr:    "'bash' is not recognized as an internal or external command, operable program or batch file.",
			attempted: shellPOSIX,
			want:      true,
		},
		{
			name:      "reported docker compose failure is command-level",
			stderr:    "bash: docker-compose exec -T app php -v: command not found",
			attempted: shellPOSIX,
			want:      false,
		},
		{
			name:      "missing docker command is command-level",
			stderr:    "bash: line 1: docker-compose: command not found",
			attempted: shellPOSIX,
			want:      false,
		},
		{
			name:      "ordinary docker error",
			stderr:    "Cannot connect to the Docker daemon",
			attempted: shellPOSIX,
			want:      false,
		},
		{
			name:      "Windows shell missing",
			stderr:    "'cmd.exe' is not recognized as an internal or external command, operable program or batch file.",
			attempted: shellWindows,
			want:      true,
		},
		{
			name:      "Windows shell missing through POSIX wrapper",
			stderr:    "bash: line 1: cmd.exe: command not found",
			attempted: shellWindows,
			want:      true,
		},
		{
			name:      "POSIX diagnostic does not identify Windows shell missing",
			stderr:    "bash: command not found",
			attempted: shellWindows,
			want:      false,
		},
		{
			name:      "Windows diagnostic does not identify POSIX shell missing",
			stderr:    "'cmd.exe' is not recognized as an internal or external command",
			attempted: shellPOSIX,
			want:      false,
		},
		{
			name:      "mention of bash is not sufficient",
			stderr:    "application error: bash returned exit status 127",
			attempted: shellPOSIX,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isShellUnavailable(tt.stderr, tt.attempted); got != tt.want {
				t.Errorf("isShellUnavailable(%q, %v) = %t, want %t", tt.stderr, tt.attempted, got, tt.want)
			}
		})
	}
}
