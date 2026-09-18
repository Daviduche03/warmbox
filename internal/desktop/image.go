package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// BuiltinImage is the reserved name of the built-in image (the Alpine + XFCE
// desktop built by deploy/guest/build.sh). "xfce" and "alpine" are aliases.
const BuiltinImage = "default"

// ImageMeta optionally overrides boot parameters for a named image. It lives
// at <ImageDir>/<name>/meta.json.
type ImageMeta struct {
	GPU    string `json:"gpu"`     // virtio-gpu size, e.g. "800x600"
	MemMiB uint   `json:"mem_mib"` // memory per VM
	CPUs   uint   `json:"cpus"`    // vCPUs per VM
	Input  bool   `json:"input"`   // attach virtio keyboard/pointing
}

// resolved describes how to boot a named image. An image is either a full EFI
// disk (disk.raw, booted via firmware) or an overlay image (vmlinux +
// rootfs.squashfs + initramfs-overlay, shared read-only with a writable upper).
type resolved struct {
	name   string
	kind   string // "efi" | "overlay"
	disk   string // efi
	vars   string // efi (optional)
	kernel string // overlay
	initrd string // overlay (boot initramfs)
	squash string // overlay (read-only base)
	meta   ImageMeta
}

// isBuiltinImage reports whether a name refers to the built-in image.
func isBuiltinImage(name string) bool {
	switch name {
	case "", BuiltinImage, "xfce", "alpine":
		return true
	}
	return false
}

// SameImage reports whether two image names refer to the same image, treating
// the built-in aliases ("", "default", "xfce", "alpine") as equivalent. Used to
// decide whether a request can be served from the warm pool.
func SameImage(a, b string) bool {
	if isBuiltinImage(a) && isBuiltinImage(b) {
		return true
	}
	return a == b
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// imageKind reports how a directory's image boots, if it is one at all.
func imageKind(dir string) (string, bool) {
	if fileExists(filepath.Join(dir, "disk.raw")) {
		return "efi", true
	}
	if fileExists(filepath.Join(dir, "vmlinux")) &&
		fileExists(filepath.Join(dir, "rootfs.squashfs")) &&
		fileExists(filepath.Join(dir, "initramfs-overlay")) {
		return "overlay", true
	}
	return "", false
}

// builtinAvailable reports whether the built-in image's artifacts exist.
func (m *Manager) builtinAvailable() bool {
	if m.overlayAvailable() || m.diskAvailable() {
		return true
	}
	return fileExists(m.cfg.InitrdPath)
}

// Images lists the images the daemon can boot: the built-in image (when its
// artifacts exist) followed by the named images under cfg.ImageDir.
func (m *Manager) Images() []string {
	var out []string
	if m.builtinAvailable() {
		out = append(out, BuiltinImage)
	}
	entries, err := os.ReadDir(m.cfg.ImageDir)
	if err != nil {
		return out
	}
	var named []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, ok := imageKind(filepath.Join(m.cfg.ImageDir, e.Name())); ok {
			named = append(named, e.Name())
		}
	}
	sort.Strings(named)
	return append(out, named...)
}

// resolveImage returns how to boot a named image, plus any per-image overrides.
func (m *Manager) resolveImage(name string) (resolved, error) {
	dir := filepath.Join(m.cfg.ImageDir, name)
	kind, ok := imageKind(dir)
	if !ok {
		return resolved{}, fmt.Errorf("unknown image %q (need disk.raw, or vmlinux + rootfs.squashfs + initramfs-overlay in %s)", name, dir)
	}
	r := resolved{name: name, kind: kind}
	switch kind {
	case "efi":
		r.disk = filepath.Join(dir, "disk.raw")
		if v := filepath.Join(dir, "efi-vars.fd"); fileExists(v) {
			r.vars = v
		}
	case "overlay":
		r.kernel = filepath.Join(dir, "vmlinux")
		r.initrd = filepath.Join(dir, "initramfs-overlay")
		r.squash = filepath.Join(dir, "rootfs.squashfs")
	}
	if b, err := os.ReadFile(filepath.Join(dir, "meta.json")); err == nil {
		_ = json.Unmarshal(b, &r.meta)
	}
	return r, nil
}
