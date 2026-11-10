//go:build windows

package cmd

import (
	"os"

	"make-sync/cmd/wincmdline"
)

// rawArgv returns the process argument vector re-parsed with the native Windows
// command-line rules (CommandLineToArgvW) so quoting and trailing punctuation
// (e.g. a trailing semicolon) match what cmd.exe / PowerShell produced. It
// falls back to os.Args when the native parsing is unavailable.
func rawArgv() []string {
	if raw := wincmdline.WindowsRawArgs(); len(raw) > 0 {
		return raw
	}
	return os.Args
}
