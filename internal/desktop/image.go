package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultImage is the image a daemon boots when a caller names none and the
// daemon was started without --image. It is an ordinary named image built from
// deploy/images/xfce.yaml — there is no separate built-in slot.
const DefaultImage = "xfce"

// ImageMeta optionally overrides boot parameters for a named image. It lives
// at <ImageDir>/<name>/meta.json, and is generated from the image config in
// deploy/images/ — never hand-edited, or the two drift.
type ImageMeta struct {
	GPU    string `json:"gpu,omitempty"`     // virtio-gpu size, e.g. "800x600"
	MemMiB uint   `json:"mem_mib,omitempty"` // memory per VM
	CPUs   uint   `json:"cpus,omitempty"`    // vCPUs per VM
	Input  bool   `json:"input,omitempty"`   // attach virtio keyboard/pointing
	// Headless marks an image with no screen: the guest skips Xvnc and the
	// desktop session and reports ready off the agent port instead of the VNC
	// port. The guest knows it from a file baked into the image; this is how
	// the daemon and dashboard learn the same thing.
	Headless bool `json:"headless,omitempty"`
	// AgentAPI is the guest-agent contract the image was built against. Zero
	// means the image predates the field, which reads as "no claim".
	AgentAPI int `json:"agent_api,omitempty"`
}

// AgentAPI is the guest-agent contract this daemon speaks. An image built
// against a newer one may use requests this daemon cannot make, so the daemon
// warns rather than failing obscurely at the first call.
const AgentAPI = 1

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

// isAlias reports whether a name stands for "whatever this daemon's default
// image is" rather than naming an image itself. "default" stays accepted so
// scripts written against the old built-in naming keep working.
func isAlias(name string) bool { return name == "" || name == "default" }

// CanonicalImage resolves a requested name against the daemon's configured
// default: an alias means the default, and a daemon with no configured default
// means DefaultImage. Returns a name that Images() lists.
func CanonicalImage(name, daemonDefault string) string {
	if !isAlias(name) {
		return name
	}
	if daemonDefault != "" {
		return daemonDefault
	}
	return DefaultImage
}

// DefaultFor is the image this daemon boots when a caller names none.
func DefaultFor(daemonDefault string) string { return CanonicalImage("", daemonDefault) }

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

// Images lists the images the daemon can boot.
func (m *Manager) Images() []string { return ImagesIn(m.cfg.ImageDir) }

// ImagesIn lists the bootable images in an image directory: every subdirectory
// that holds a complete image. It is the same rule the daemon applies at run
// time, for callers that do not have a Manager yet.
func ImagesIn(imageDir string) []string {
	entries, err := os.ReadDir(imageDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, ok := imageKind(filepath.Join(imageDir, e.Name())); ok {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// ImageMetas reports each bootable image's meta.json overrides, keyed by the
// name Images reports. An image whose meta.json cannot be read is left out
// rather than reported as headless by accident. This is how the API tells the
// dashboard which images have no screen.
func (m *Manager) ImageMetas() map[string]ImageMeta {
	names := m.Images()
	out := make(map[string]ImageMeta, len(names))
	for _, name := range names {
		r, err := m.resolveImage(name)
		if err != nil {
			continue
		}
		out[name] = r.meta
	}
	return out
}

// ValidImageName rejects anything that could step outside the images directory
// (or name a file rather than a directory). Exported because the image registry
// validates config names with the same rule the daemon applies at run time.
func ValidImageName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// resolveImage returns how to boot a named image, plus any per-image overrides.
func (m *Manager) resolveImage(name string) (resolved, error) {
	if !ValidImageName(name) {
		return resolved{}, fmt.Errorf("invalid image name %q", name)
	}
	dir := filepath.Join(m.cfg.ImageDir, name)
	// Belt and braces: whatever the name, the directory we read from must stay
	// inside ImageDir. A request-supplied name is not a path.
	if rel, err := filepath.Rel(m.cfg.ImageDir, dir); err != nil ||
		rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return resolved{}, fmt.Errorf("invalid image name %q", name)
	}
	kind, ok := imageKind(dir)
	if !ok {
		return resolved{}, fmt.Errorf("unknown image %q", name)
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
