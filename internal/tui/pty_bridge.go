package tui

import (
	"io"
	"os"
	"os/signal"

	"golang.org/x/term"
)

// AttachLocalTTY attaches local terminal (stdin/stdout) to rwc (remote PTY).
// It puts local terminal into raw mode, starts copying stdin->rwc and rwc->stdout,
// and returns when rwc closes or an interrupt occurs. Caller must supply a valid rwc.
func AttachLocalTTY(rwc io.ReadWriteCloser) error {
	fd := int(os.Stdin.Fd())
	isTerm := term.IsTerminal(fd)

	// If not a terminal, just proxy without raw mode
	if !isTerm {
		done := make(chan struct{})
		go func() { _, _ = io.Copy(rwc, os.Stdin); done <- struct{}{} }()
		go func() { _, _ = io.Copy(os.Stdout, rwc); done <- struct{}{} }()
		<-done
		return nil
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		// fallback: proxy without raw mode
		defer rwc.Close()
		_, _ = io.Copy(rwc, os.Stdin)
		_, _ = io.Copy(os.Stdout, rwc)
		return nil
	}
	// ensure restore
	defer term.Restore(fd, oldState)

	// handle signals: os.Interrupt + platform-specific winchSignals (may be empty on Windows)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, append([]os.Signal{os.Interrupt}, winchSignals...)...)
	defer func() {
		signal.Stop(sigCh)
		close(sigCh)
	}()

	// copy streams
	errCh := make(chan error, 2)
	go func() {
		_, e := io.Copy(rwc, os.Stdin)
		errCh <- e
	}()
	go func() {
		_, e := io.Copy(os.Stdout, rwc)
		errCh <- e
	}()

	// wait for first event: error/copy done or signal
	for {
		select {
		case s := <-sigCh:
			// handle window-change only if platform provides it
			if len(winchSignals) > 0 && s == winchSignals[0] {
				// best-effort: consumer (SSH session) may handle window change separately.
				continue
			}
			// for other signals (e.g. interrupt) just break and allow cleanup/restore
			return nil
		case e := <-errCh:
			// done or copy error
			if e != nil && e != io.EOF {
				_ = rwc.Close()
				return e
			}
			// normal EOF -> return
			_ = rwc.Close()
			return nil
		}
	}
}
