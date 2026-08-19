//go:build !linux && !windows

package util

// SetProcTitle is a no-op on platforms other than Linux and Windows.
// gspt requires cgo which is not always available on macOS/BSD builds.
func SetProcTitle(title string) {
	_ = title
}
