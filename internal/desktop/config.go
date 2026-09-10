// Package desktop manages the lifecycle of Firecracker-class microVMs on
// hosts where Firecracker is unavailable (e.g. Apple Silicon), using vfkit and
// Apple's Virtualization.framework. It boots a pre-baked GUI rootfs and streams
// it over VNC.
package desktop

import (
	"os"
	"path/filepath"
)

// Config holds everything the orchestrator needs to boot guest microVMs.
type Config struct {
	// WorkDir is where kernels, rootfs images and per-VM state live.
	WorkDir string

	// VfkitPath is the vfkit binary (Apple Virtualization.framework frontend).
	VfkitPath string

	// KernelPath is the uncompressed arm64 vmlinux.
	KernelPath string

	// InitrdPath is the zstd-compressed cpio initramfs containing the whole rootfs.
	InitrdPath string

	// BootInitrdPath is the small Alpine boot initramfs (with virtio-blk/ext4)
	// used when booting from DiskPath instead of the all-RAM InitrdPath.
	BootInitrdPath string

	// DiskPath is the base ext4 rootfs image for low-RAM disk boot. When it
	// (and BootInitrdPath) exist, each VM boots its own clone of this image and
	// pages the rootfs in on demand instead of loading it all into RAM. Empty
	// or missing => fall back to the all-RAM initramfs.
	DiskPath string

	// NoVNCDir is a directory containing the noVNC static assets.
	NoVNCDir string

	// HostAddr is the address the guest uses to reach the orchestrator
	// (the host's vmnet/NAT gateway, e.g. 192.168.64.1).
	HostAddr string

	// APIAddr is the listen address for the orchestrator's HTTP server.
	APIAddr string

	// MemMiB / CPUs size each microVM.
	MemMiB uint
	CPUs   uint

	// Display is the Xvnc geometry (e.g. 1280x800).
	Display string

	// GuestVNCPort is the VNC port the guest listens on.
	GuestVNCPort int

	// PoolSize is the number of pre-booted idle VMs kept warm.
	PoolSize int

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
}

// DefaultConfig returns a Config rooted at ~/.warmbox.
func DefaultConfig() *Config {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	wd := filepath.Join(home, ".warmbox")
	return &Config{
		WorkDir:        wd,
		VfkitPath:      "vfkit",
		KernelPath:     filepath.Join(wd, "vmlinux"),
		InitrdPath:     filepath.Join(wd, "initramfs.zst"),
		BootInitrdPath: filepath.Join(wd, "initramfs-virt"),
		DiskPath:       filepath.Join(wd, "rootfs.img"),
		NoVNCDir:       filepath.Join(wd, "novnc"),
		HostAddr:       "192.168.64.1",
		APIAddr:        ":7070",
		MemMiB:         4096,
		CPUs:           4,
		Display:        "1280x800",
		GuestVNCPort:   5900,
		PoolSize:       2,
		ShareTag:       "workspace",
	}
}

// VMDir returns the per-VM state directory.
func (c *Config) VMDir(id string) string {
	return filepath.Join(c.WorkDir, "vms", id)
}

// EnsureDirs creates the working directories.
func (c *Config) EnsureDirs() error {
	for _, d := range []string{c.WorkDir, filepath.Join(c.WorkDir, "vms")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
