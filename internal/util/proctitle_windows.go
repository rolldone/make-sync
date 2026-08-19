//go:build windows

package util

// SetProcTitle is a no-op on Windows. Process titles are not supported there.
func SetProcTitle(title string) {
	_ = title
}
