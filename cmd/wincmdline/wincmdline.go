package wincmdline

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// WindowsRawArgs re-parses the process command line using the Windows API
// CommandLineToArgvW so that quoting/escaping rules match what cmd.exe and
// PowerShell use. This fixes cases where Go's default os.Args parser splits
// tokens differently (e.g. a trailing semicolon is lost).
//
// Returns a non-nil slice only on Windows when a real command line exists.
func WindowsRawArgs() []string {
	cmdline := windows.GetCommandLine()
	if cmdline == nil {
		return nil
	}

	var argc int32
	argv, err := windows.CommandLineToArgv(cmdline, &argc)
	if err != nil || argv == nil {
		return nil
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(argv))))

	out := make([]string, 0, argc)
	for i := 0; i < int(argc); i++ {
		p := argv[i]
		if p == nil {
			break
		}
		out = append(out, windows.UTF16ToString(p[:]))
	}
	return out
}
