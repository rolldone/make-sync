//go:build windows
// +build windows

package localclient

import (
	"errors"
	"io"
)

func (b *PTYLocalBridge) StartInteractiveShell(cb func([]byte)) error {
	_ = cb
	return errors.New("local PTY not supported on Windows")
}
func (b *PTYLocalBridge) StartInteractiveShellWithCommand(command string) error {
	_ = command
	return errors.New("local PTY not supported on Windows")
}
func (b *PTYLocalBridge) Pause() error  { return errors.New("not supported") }
func (b *PTYLocalBridge) Resume() error { return errors.New("not supported") }
func (b *PTYLocalBridge) Close() error  { return nil }

// no-op stdin writer getters to satisfy Bridge interface
func (b *PTYLocalBridge) SetStdinWriter(w io.WriteCloser) {}
func (b *PTYLocalBridge) GetStdinWriter() io.WriteCloser  { return nil }

// no-op stdin hook getters/setters
func (b *PTYLocalBridge) SetStdinMatcher(m func([]byte) bool) {}
func (b *PTYLocalBridge) GetStdinMatcher() func([]byte) bool  { return nil }
func (b *PTYLocalBridge) SetStdinCallback(cb func([]byte))    {}
func (b *PTYLocalBridge) GetStdinCallback() func([]byte)      { return nil }
func (b *PTYLocalBridge) SetStdinObserver(o func([]byte))     {}
func (b *PTYLocalBridge) GetStdinObserver() func([]byte)      { return nil }
