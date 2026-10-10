//go:build linux

package main

import (
	"syscall"
	"unsafe"
)

// kernelSigaction is the kernel's struct sigaction as rt_sigaction expects
// it (asm-generic layout, same on amd64 and arm64): handler, flags,
// restorer, then the 1024-bit mask. stdlib has no Sigaction wrapper and this
// module stays dependency-free, so the dozen lines live here.
type kernelSigaction struct {
	handler  uintptr
	flags    uint64
	restorer uintptr
	mask     [16]uint64
}

// resetsignals restores SIG_DFL for the job-control signals via the
// rt_sigaction syscall directly.
//
// Why not os/signal.Reset: Reset restores the runtime's bookkeeping, which
// does not undo an inherited SIG_IGN (verified: a child of an
// Ignore-then-Reset parent still ignores). The raw syscall sets the real
// disposition, which is what children inherit across exec.
//
// Call once at startup, before spawning anything: the guest init runs with
// SIGINT ignored and that disposition would otherwise reach every shell the
// agent starts — and everything those shells run — leaving Ctrl-C dead.
func resetsignals() error {
	var dfl kernelSigaction // zero value: handler 0 (SIG_DFL), no flags
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGHUP} {
		// sigsetsize 8 matches what libc passes (verified against musl via
		// strace); flags 0 selects the kernel's default rt_sigreturn path.
		_, _, errno := syscall.RawSyscall6(syscall.SYS_RT_SIGACTION,
			uintptr(sig), uintptr(unsafe.Pointer(&dfl)), 0, 8, 0, 0)
		if errno != 0 {
			return errno
		}
	}
	return nil
}
