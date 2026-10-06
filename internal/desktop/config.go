// Package desktop manages the lifecycle of Firecracker-class microVMs on
// hosts where Firecracker is unavailable (e.g. Apple Silicon), using vfkit and
// Apple's Virtualization.framework. It boots a pre-baked GUI rootfs and streams
// it over VNC.
package desktop

import (
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Config holds everything the orchestrator needs to boot guest microVMs.
type Config struct {
	// WorkDir is where kernels, rootfs images and per-VM state live.
	WorkDir string

	// VfkitPath is the vfkit binary (Apple Virtualization.framework frontend).
	VfkitPath string

	// Backend selects the hypervisor backend (default "vfkit").
	Backend string

	// Image names the default guest image to boot when a caller does not ask
	// for one. Names resolve under ImageDir; empty means DefaultImage.
	Image string

	// ImageDir holds every guest image: <ImageDir>/<name>/ with either a
	// bootable disk (disk.raw, EFI) or a shared read-only rootfs (vmlinux +
	// rootfs.squashfs + initramfs-overlay), plus meta.json — the per-image
	// boot overrides. There is no separate "built-in" slot: every image is a
	// named image, and one of them is the default.
	ImageDir string

	// EgressAddr, when non-empty, runs the egress policy proxy on this address
	// (e.g. ":8099"). Guests are told to route through it.
	EgressAddr string

	// Allow and Deny are domain patterns for the egress proxy. A non-empty
	// Allow makes the policy default-deny (only Allow matches pass).
	Allow []string
	Deny  []string

	// GPU, when non-empty (e.g. "1440x900"), attaches a virtio-gpu device so a
	// Wayland compositor in the guest has an output to render to. Empty leaves
	// the guest headless (the default image streams Xvnc over VNC).
	GPU string

	// Input attaches virtio keyboard and pointing devices to the guest.
	Input bool

	// Accel is QEMU's accelerator: "kvm" (the default) or "tcg" (software
	// emulation, ~20x slower). Only tcg works on a machine without KVM, which
	// is what CI has — it is how the smoke test boots an image before the image
	// is published. vfkit ignores it.
	Accel string

	// TLSCert and TLSKey, when both are set, serve the dashboard over HTTPS.
	// The daemon refuses a non-loopback address without them (or without
	// Insecure): everything it serves — the session cookie, the console, the
	// guest API — is plaintext, and a mistake here is not visible until it is
	// someone else's.
	TLSCert string
	TLSKey  string

	// Insecure permits a non-loopback address with no TLS. It exists so the
	// decision is explicit and greppable rather than accidental.
	Insecure bool

	// GUI opens the hypervisor's native window (vfkit --gui). A bring-up aid
	// for image guests; normal operation streams over VNC.
	GUI bool

	// NoVNCDir is a directory containing the noVNC static assets.
	NoVNCDir string

	// HostAddr is the address the guest uses to reach the orchestrator
	// (the host's vmnet/NAT gateway, e.g. 192.168.64.1).
	HostAddr string

	// APIAddr is the listen address for the orchestrator's HTTP server
	// (dashboard + API). Loopback by default: nothing outside this host should
	// reach the dashboard.
	APIAddr string

	// GuestAddr is where guests post readiness callbacks. Empty derives it from
	// HostAddr and APIAddr's port; "off" disables the listener (only correct
	// when APIAddr already answers on the gateway address).
	GuestAddr string

	// MemMiB / CPUs size each microVM.
	MemMiB uint
	CPUs   uint

	// Display is the Xvnc geometry (e.g. 1280x800).
	Display string

	// GuestVNCPort is the VNC port the guest listens on.
	GuestVNCPort int

	// AgentPort is the port warmbox-agent listens on inside the guest.
	AgentPort int

	// PoolSize is the number of pre-booted idle VMs kept warm.
	PoolSize int

	// PoolIdleTimeout, when > 0, reclaims warm-pool VMs that have gone unused
	// for this long, handing their RAM back to the host. 0 keeps them warm
	// indefinitely (creates stay instant, at a standing RAM cost).
	PoolIdleTimeout time.Duration

	// ShareDir, when non-empty, is a host directory exposed to every guest as
	// a virtiofs share (mounted at /workspace in the guest).
	ShareDir string

	// ShareTag is the virtiofs mount tag; it is also passed on the kernel
	// cmdline as warmbox.share=<tag>.
	ShareTag string

	// Token, when non-empty, is required on API/UI routes either as a
	// ?token= query parameter or an "Authorization: Bearer <token>" header.
	// The guest readiness callback is never gated.
	Token string

	// VolumeDir is the local cache directory for volume disk images.
	VolumeDir string

	// VolumeBase is the base ext4 image cloned for new volumes.
	VolumeBase string

	// VolumeChunkMiB is the volume transfer chunk size in MiB.
	VolumeChunkMiB int

	// VolumePrefix is the remote prefix under which volumes are stored.
	VolumePrefix string
}

// DefaultConfig returns a Config rooted at ~/.warmbox.
func DefaultConfig() *Config {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	wd := filepath.Join(home, ".warmbox")
	// vfkit wraps Apple's Virtualization.framework and only exists on macOS;
	// everywhere else QEMU is what the host actually has, so nobody should have
	// to pass --backend qemu just to start the daemon.
	backend := "vfkit"
	if runtime.GOOS != "darwin" {
		backend = "qemu"
	}
	return &Config{
		WorkDir:         wd,
		VfkitPath:       "vfkit",
		Backend:         backend,
		Accel:           "kvm",
		ImageDir:        filepath.Join(wd, "images"),
		NoVNCDir:        filepath.Join(wd, "novnc"),
		HostAddr:        "192.168.64.1",
		APIAddr:         "127.0.0.1:7070",
		MemMiB:          4096,
		CPUs:            4,
		Display:         "1280x800",
		GuestVNCPort:    5900,
		AgentPort:       7077,
		PoolSize:        2,
		PoolIdleTimeout: 15 * time.Minute,
		ShareTag:        "workspace",
		VolumeDir:       filepath.Join(wd, "volumes"),
		VolumeBase:      filepath.Join(wd, "volume-base.img"),
		VolumeChunkMiB:  16,
		VolumePrefix:    "volumes",
	}
}

// VMDir returns the per-VM state directory.
func (c *Config) VMDir(id string) string {
	return filepath.Join(c.WorkDir, "vms", id)
}

// SnapshotDir returns the directory holding a VM's memory checkpoints. Each
// checkpoint is RAM contents (guest secrets included), so the directory is
// created 0700 like the VM directories.
func (c *Config) SnapshotDir(id string) string {
	return filepath.Join(c.WorkDir, "snapshots", id)
}

// EnsureDirs creates the working directories.
func (c *Config) EnsureDirs() error {
	dirs := []string{c.WorkDir, filepath.Join(c.WorkDir, "vms"), filepath.Join(c.WorkDir, "snapshots")}
	if c.VolumeDir != "" {
		dirs = append(dirs, c.VolumeDir)
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
