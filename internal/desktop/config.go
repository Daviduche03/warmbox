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

	// Backend selects the hypervisor backend (default "vfkit").
	Backend string

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

	// SquashPath is a read-only squashfs rootfs base shared by every VM. When
	// it (and OverlayInitrdPath) exist, VMs boot it with a tmpfs overlay, so no
	// per-VM disk copy is made and all writes live in RAM. Takes precedence
	// over DiskPath.
	SquashPath string

	// OverlayInitrdPath is the boot initramfs that mounts SquashPath read-only
	// and layers a tmpfs overlay on top (see deploy/guest/overlay-init).
	OverlayInitrdPath string

	// Image names the default guest image to boot when a caller does not ask
	// for one explicitly. Names resolve under ImageDir. Empty falls back to
	// the built-in image (overlay > disk > initramfs), or to EFIDisk when set.
	Image string

	// ImageDir is where named guest images live: <ImageDir>/<name>/ containing
	// disk.raw (an EFI-bootable disk), an optional efi-vars.fd seed, and an
	// optional meta.json ({"gpu","mem_mib","cpus","input"}).
	ImageDir string

	// EFIDisk is a full EFI-bootable disk image used when Image is empty (a
	// legacy anonymous EFI image). Every VM boots its own clone.
	EFIDisk string

	// EFIVars is an optional seed EFI variable store copied into each VM. An
	// EFI install often keeps its boot entry only in NVRAM, so a fresh store
	// may not find anything to boot; seeding from the machine that installed
	// the disk fixes that.
	EFIVars string

	// GPU, when non-empty (e.g. "1440x900"), attaches a virtio-gpu device so a
	// Wayland compositor in the guest has an output to render to. Empty leaves
	// the guest headless (the default image streams Xvnc over VNC).
	GPU string

	// Input attaches virtio keyboard and pointing devices to the guest.
	Input bool

	// GUI opens the hypervisor's native window (vfkit --gui). A bring-up aid
	// for image guests; normal operation streams over VNC.
	GUI bool

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

	// AgentPort is the port warmbox-agent listens on inside the guest.
	AgentPort int

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
	return &Config{
		WorkDir:           wd,
		VfkitPath:         "vfkit",
		Backend:           "vfkit",
		KernelPath:        filepath.Join(wd, "vmlinux"),
		InitrdPath:        filepath.Join(wd, "initramfs.zst"),
		BootInitrdPath:    filepath.Join(wd, "initramfs-virt"),
		DiskPath:          filepath.Join(wd, "rootfs.img"),
		SquashPath:        filepath.Join(wd, "rootfs.squashfs"),
		OverlayInitrdPath: filepath.Join(wd, "initramfs-overlay"),
		ImageDir:          filepath.Join(wd, "images"),
		NoVNCDir:          filepath.Join(wd, "novnc"),
		HostAddr:          "192.168.64.1",
		APIAddr:           ":7070",
		MemMiB:            4096,
		CPUs:              4,
		Display:           "1280x800",
		GuestVNCPort:      5900,
		AgentPort:         7077,
		PoolSize:          2,
		ShareTag:          "workspace",
		VolumeDir:         filepath.Join(wd, "volumes"),
		VolumeBase:        filepath.Join(wd, "volume-base.img"),
		VolumeChunkMiB:    16,
		VolumePrefix:      "volumes",
	}
}

// VMDir returns the per-VM state directory.
func (c *Config) VMDir(id string) string {
	return filepath.Join(c.WorkDir, "vms", id)
}

// EnsureDirs creates the working directories.
func (c *Config) EnsureDirs() error {
	dirs := []string{c.WorkDir, filepath.Join(c.WorkDir, "vms")}
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
