//go:build !linux

package main

import (
	"errors"
	"os"
)

// The agent only ever runs on Linux (it is baked into the guest image), so a
// pty here is a build error made explicit: anything requesting one gets a
// clear failure instead of a silently pipe-backed session.
func openPTY() (*os.File, string, error) {
	return nil, "", errors.New("pseudoterminals are only supported on linux")
}

func setWinsize(_ *os.File, _, _ int) error {
	return errors.New("pseudoterminals are only supported on linux")
}

func getWinsize(_ *os.File) (int, int, error) {
	return 0, 0, errors.New("pseudoterminals are only supported on linux")
}
