//go:build linux

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// winsize is the kernel's struct winsize (four unsigned shorts); stdlib's
// syscall package has no Winsize on Linux and this module stays
// dependency-free, so it lives here.
type winsize struct {
	Row, Col, Xpixel, Ypixel uint16
}

// openPTY allocates a pty pair. The agent keeps the master — it reads the
// child's output, takes its input, and owns the window size — and the child
// gets the slave as its stdio plus controlling terminal, so shells and
// full-screen programs see a real tty.
//
// Deliberately raw syscalls (open /dev/ptmx, unlock, number, winsize) rather
// than a pty library: this module is dependency-free and the Dockerfile
// builds it with plain `go build`.
func openPTY() (master *os.File, slavePath string, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, "", fmt.Errorf("open /dev/ptmx: %w", err)
	}
	unlock := func() {
		master.Close()
	}
	// unlockpt: clear the lock flag (0) so the slave side can be opened.
	var lock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(),
		uintptr(syscall.TIOCSPTLCK), uintptr(unsafe.Pointer(&lock))); errno != 0 {
		unlock()
		return nil, "", fmt.Errorf("unlockpt: %w", errno)
	}
	// pty number -> /dev/pts/N.
	var num uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(),
		uintptr(syscall.TIOCGPTN), uintptr(unsafe.Pointer(&num))); errno != 0 {
		unlock()
		return nil, "", fmt.Errorf("pty number: %w", errno)
	}
	slavePath = fmt.Sprintf("/dev/pts/%d", num)
	// Both sides run as root (agent and shell alike), so the stock 0600 slave
	// is already openable; 0620 keeps group-tty conventions without depending
	// on devpts mount options. Best effort: a failure here must not fail the
	// session when open() would still succeed.
	_ = os.Chmod(slavePath, 0o620)
	return master, slavePath, nil
}

// setWinsize sizes the terminal and notifies the foreground process group
// (the kernel raises SIGWINCH itself on TIOCSWINSZ).
func setWinsize(master *os.File, cols, rows int) error {
	if cols <= 0 || rows <= 0 || cols > 1000 || rows > 1000 {
		return fmt.Errorf("bad size %dx%d (want 1-1000 each)", cols, rows)
	}
	ws := winsize{Row: uint16(rows), Col: uint16(cols)}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(),
		uintptr(syscall.TIOCSWINSZ), uintptr(unsafe.Pointer(&ws))); errno != 0 {
		return fmt.Errorf("window size: %w", errno)
	}
	return nil
}

// getWinsize reads the terminal size back (tests, and anything that needs to
// confirm a resize landed).
func getWinsize(master *os.File) (cols, rows int, err error) {
	var ws winsize
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(),
		uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws))); errno != 0 {
		return 0, 0, fmt.Errorf("window size: %w", errno)
	}
	return int(ws.Col), int(ws.Row), nil
}
