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

// Images lists the named images available under cfg.ImageDir.
func (m *Manager) Images() []string {
	entries, err := os.ReadDir(m.cfg.ImageDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(m.cfg.ImageDir, e.Name(), "disk.raw")); err == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
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
