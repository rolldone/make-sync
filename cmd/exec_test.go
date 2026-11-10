package cmd

import (
	"reflect"
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

func TestRawCommandFromArgv(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want string
	}{
		{
			name: "redirect tokens preserved",
			argv: []string{"make-sync", "exec", "echo", "hi", ">", "/tmp/f"},
			want: "echo hi > /tmp/f",
		},
		{
			name: "quoted compound command from a single os.Arg",
			argv: []string{"make-sync", "exec", "cat /etc/hostname | head -1; echo ok"},
			want: "cat /etc/hostname | head -1; echo ok",
		},
		{
			name: "leading interactive flag stripped, inner -it kept",
			argv: []string{"make-sync", "exec", "-it", "docker", "compose", "exec", "-it", "app", "bash"},
			want: "docker compose exec -it app bash",
		},
		{
			name: "global flag before subcommand",
			argv: []string{"make-sync", "--config", "x.yaml", "exec", "ls", "-la"},
			want: "ls -la",
		},
		{
			name: "no exec token",
			argv: []string{"make-sync", "pull"},
			want: "",
		},
		{
			name: "exec token but empty command",
			argv: []string{"make-sync", "exec"},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rawCommandFromArgv(tt.argv); got != tt.want {
				t.Errorf("rawCommandFromArgv(%v) = %q, want %q", tt.argv, got, tt.want)
			}
		})
	}
}

func TestRawCommandTextFromArgv(t *testing.T) {
	// Raw command available: argv wins, restArgs is ignored.
	got := rawCommandTextFromArgv(
		[]string{"make-sync", "exec", "echo hi > /tmp/f"},
		[]string{"echo", "hi", ">", "/tmp/f"},
	)
	if got != "echo hi > /tmp/f" {
		t.Errorf("raw from argv = %q, want %q", got, "echo hi > /tmp/f")
	}

	// No raw available: fall back to joining the parsed args.
	got = rawCommandTextFromArgv(
		[]string{"make-sync", "pull"},
		[]string{"echo", "hi", ">", "/tmp/f"},
	)
	if got != "echo hi > /tmp/f" {
		t.Errorf("fallback = %q, want %q", got, "echo hi > /tmp/f")
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
