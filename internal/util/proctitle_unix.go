//go:build linux

package util

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// prSetName is PR_SET_NAME from <linux/prctl.h>.
const prSetName = 15

// SetProcTitle sets the process title on Linux. It uses prctl(PR_SET_NAME) which
// updates the process comm name shown in ps/top. On other platforms this is a no-op.
func SetProcTitle(title string) {
	name := title
	if len(name) > 15 {
		name = name[:15]
	}
	b := append([]byte(name), 0)
	_, _, _ = unix.Syscall(unix.SYS_PRCTL, prSetName, uintptr(unsafe.Pointer(&b[0])), 0)
}
