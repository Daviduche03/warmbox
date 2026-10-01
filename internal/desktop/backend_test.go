package desktop

import (
	"testing"
)

// QEMU must be told there is no display.
//
// Left to its default it picks GTK, which on a machine without X cannot open a
// display: it prints "gtk initialization failed" and exits 1 *before loading the
// kernel*. The guest never runs, so its console log stays empty and the only
// explanation is on stderr — which is why this cost a day to find on a headless
// VPS while every host with a desktop booted fine.
//
// -nodefaults does not cover this: it disables default devices, not the choice
// of display backend. The guest draws into Xvnc's own framebuffer and the daemon
// streams that, so no host display is needed or wanted.
func TestQEMUAsksForNoHostDisplay(t *testing.T) {
	b := &qemuBackend{}
	inst, err := b.Launch(LaunchSpec{
		CPUs: 2, MemMiB: 1024,
		Kernel:  "/tmp/vmlinux",
		Initrd:  "/tmp/initramfs-overlay",
		Console: "/tmp/console.log",
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	args := inst.Cmd.Args

	display := valueAfter(args, "-display")
	if display != "none" {
		t.Errorf("-display %q in %v, want none", display, args)
	}
	// The guest's console has to go somewhere readable; losing it is how a
	// guest-side failure becomes invisible.
	if !hasArg(args, "-serial") {
		t.Errorf("no -serial in %v", args)
	}
	if !hasArg(args, "-no-reboot") {
		// -no-reboot is what makes a dead guest exit QEMU instead of looping,
		// and the exit status is what the daemon reports.
		t.Errorf("no -no-reboot in %v", args)
	}
}

// The kernel and initrd the caller asked for must reach QEMU: warmbox passes a
// bzImage or an ELF vmlinux depending on the platform, and either is fine, but
// it must be the one the image actually shipped.
func TestQEMUPassesTheKernelItWasGiven(t *testing.T) {
	b := &qemuBackend{}
	inst, err := b.Launch(LaunchSpec{
		CPUs: 1, MemMiB: 512,
		Kernel:  "/img/vmlinux",
		Initrd:  "/img/initramfs-overlay",
		Console: "/tmp/console.log",
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if got := valueAfter(inst.Cmd.Args, "-kernel"); got != "/img/vmlinux" {
		t.Errorf("-kernel %q, want /img/vmlinux", got)
	}
	if got := valueAfter(inst.Cmd.Args, "-initrd"); got != "/img/initramfs-overlay" {
		t.Errorf("-initrd %q, want /img/initramfs-overlay", got)
	}
}

// QEMU's own output must be kept: a hypervisor that refuses to start says why,
// and the tail is what a failed boot quotes.
func TestQEMUKeepsItsOwnOutput(t *testing.T) {
	b := &qemuBackend{}
	inst, err := b.Launch(LaunchSpec{CPUs: 1, MemMiB: 512, Kernel: "/k", Initrd: "/i", Console: "/tmp/c.log"})
	if err != nil {
		t.Fatal(err)
	}
	if inst.Stderr == nil {
		t.Fatal("the QEMU instance has nowhere to keep its output")
	}
	if inst.Cmd.Stderr == nil {
		t.Error("QEMU's stderr is not connected to anything; a refusal would be discarded")
	}
}

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// valueAfter returns the argument following flag, or "" when it is absent.
func valueAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
