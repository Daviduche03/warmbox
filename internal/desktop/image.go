package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ImageMeta optionally overrides boot parameters for a named image. It lives
// at <ImageDir>/<name>/meta.json.
type ImageMeta struct {
	GPU    string `json:"gpu"`     // virtio-gpu size, e.g. "800x600"
	MemMiB uint   `json:"mem_mib"` // memory per VM
	CPUs   uint   `json:"cpus"`    // vCPUs per VM
	Input  bool   `json:"input"`   // attach virtio keyboard/pointing
}

// BuiltinImage is the reserved name of the built-in image (the Alpine + XFCE
// desktop built by deploy/guest/build.sh). "xfce" and "alpine" are aliases.
const BuiltinImage = "default"

// isBuiltinImage reports whether a name refers to the built-in image.
func isBuiltinImage(name string) bool {
	switch name {
	case "", BuiltinImage, "xfce", "alpine":
		return true
	}
	return false
}

// builtinAvailable reports whether the built-in image's artifacts exist.
func (m *Manager) builtinAvailable() bool {
	if m.overlayAvailable() || m.diskAvailable() {
		return true
	}
	if m.cfg.InitrdPath == "" {
		return false
	}
	_, err := os.Stat(m.cfg.InitrdPath)
	return err == nil
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
		if _, err := os.Stat(filepath.Join(m.cfg.ImageDir, e.Name(), "disk.raw")); err == nil {
			named = append(named, e.Name())
		}
	}
	sort.Strings(named)
	return append(out, named...)
}

// resolveImage returns the EFI disk and optional variable-store seed for a
// named image, plus any per-image overrides.
func (m *Manager) resolveImage(name string) (disk, vars string, meta ImageMeta, err error) {
	dir := filepath.Join(m.cfg.ImageDir, name)
	disk = filepath.Join(dir, "disk.raw")
	if _, err := os.Stat(disk); err != nil {
		return "", "", ImageMeta{}, fmt.Errorf("unknown image %q (no %s)", name, disk)
	}
	vars = filepath.Join(dir, "efi-vars.fd")
	if _, err := os.Stat(vars); err != nil {
		vars = ""
	}
	if b, e := os.ReadFile(filepath.Join(dir, "meta.json")); e == nil {
		_ = json.Unmarshal(b, &meta)
	}
	return disk, vars, meta, nil
}
