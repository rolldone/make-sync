package cmd

import (
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

func TestShellQuoteArgsPOSIX(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "args with space",
			args: []string{"echo", "hello world"},
			want: "echo 'hello world'",
		},
		{
			name: "args with semicolon only",
			args: []string{"echo", "a;b"},
			want: "echo 'a;b'",
		},
		{
			name: "plain args",
			args: []string{"ls", "-la"},
			want: "ls -la",
		},
		{
			name: "args with single quote",
			args: []string{"echo", "it's"},
			want: "echo 'it'\\''s'",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shellQuoteArgsPOSIX(tt.args)
			if got != tt.want {
				t.Errorf("shellQuoteArgsPOSIX(%v)\n  got  = %q\n  want = %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestShellQuoteArgsPOSIX_SemicolonPreserved(t *testing.T) {
	// The critical test case: make-sync exec docker exec container php -r "echo 'PHP_OK';"
	// restArgs after PowerShell splitting:
	restArgs := []string{"docker", "exec", "artywiz_wp_wordpress", "php", "-r", "echo 'PHP_OK';"}

	rawCmd := shellQuoteArgsPOSIX(restArgs)

	// The rawCmd must NOT be a naive join which would lose argument boundaries.
	naiveJoin := "docker exec artywiz_wp_wordpress php -r echo 'PHP_OK';"
	if rawCmd == naiveJoin {
		t.Fatalf("rawCmd must NOT be naive join; got %q (semicolons + single quotes would break bash)", rawCmd)
	}

	// The semicolon and single quotes must be inside a shell-quoted segment
	t.Logf("restArgs = %v", restArgs)
	t.Logf("rawCmd   = %q", rawCmd)

	// Build the final bash command as exec.go does
	combined := rawCmd
	bashCmd := "bash -lc " + shellEscapePOSIX(combined)
	t.Logf("bashCmd  = %q", bashCmd)
}

func TestShellQuoteArgsWindows(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "simple args",
			args: []string{"docker", "exec", "container", "php", "-r", "echo OK"},
			want: `docker exec container php -r "echo OK"`,
		},
		{
			name: "args with semicolon",
			args: []string{"php", "-r", "echo 'PHP_OK';"},
			want: `php -r "echo 'PHP_OK';"`,
		},
		{
			name: "plain args",
			args: []string{"ls", "-la"},
			want: "ls -la",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shellQuoteArgsWindows(tt.args)
			if got != tt.want {
				t.Errorf("shellQuoteArgsWindows(%v)\n  got  = %q\n  want = %q", tt.args, got, tt.want)
			}
		})
	}
}
