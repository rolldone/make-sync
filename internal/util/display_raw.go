package util

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/term"
)

func NewRaw() (*term.State, error) {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return nil, err
	}
	return oldState, nil
}

func ResetRaw(oldState *term.State) error {
	if oldState == nil {
		return nil
	}
	err := term.Restore(int(os.Stdin.Fd()), oldState)
	fmt.Print("\033[2J\033[H")
	time.Sleep(300 * time.Millisecond)
	return err
}
