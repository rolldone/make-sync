//go:build !windows

package cmd

import "os"

// rawArgv returns the process argument vector as the host shell produced it.
// On POSIX systems os.Args already carries the tokens the user typed (with the
// shell's outer quoting removed), which is the best raw source available for
// reconstructing the remote command text.
func rawArgv() []string {
	return os.Args
}
