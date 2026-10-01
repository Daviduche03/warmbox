package api

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"warmbox/internal/desktop"
)

// installImage writes the least a directory needs to count as an overlay image.
func installImage(t *testing.T, dir, name, meta string) {
	t.Helper()
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"vmlinux", "rootfs.squashfs", "initramfs-overlay"} {
		if err := os.WriteFile(filepath.Join(d, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if meta != "" {
		if err := os.WriteFile(filepath.Join(d, "meta.json"), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func testServer(t *testing.T, imageDir string) *Server {
	t.Helper()
	cfg := &desktop.Config{WorkDir: t.TempDir(), ImageDir: imageDir, APIAddr: "127.0.0.1:7070"}
	return New(desktop.NewManager(cfg, io.Discard), nil, cfg, nil, nil, io.Discard)
}

// The Images page lists what is installed and what this build knows about. Both
// halves matter: the registry alone would hide an image built locally, and the
// installed list alone is why an available-but-unfetched image used to be
// invisible.
func TestImageStatusesMergesInstalledAndCatalogue(t *testing.T) {
	dir := t.TempDir()
	installImage(t, dir, "lxqt", `{"headless":false}`)
	installImage(t, dir, "mine", `{"headless":true}`) // built here, unknown to the binary
	s := testServer(t, dir)

	byName := map[string]ImageStatus{}
	for _, st := range s.imageStatuses() {
		byName[st.Name] = st
	}

	// Installed and in the registry.
	if st := byName["lxqt"]; !st.Installed || !st.Pullable || st.State != "installed" {
		t.Errorf("lxqt = %+v, want installed and pullable", st)
	}
	// In the registry, not installed: the row that used to be missing.
	st, ok := byName["xfce"]
	if !ok {
		t.Fatal("xfce should be listed even though it is not installed")
	}
	if st.Installed || !st.Pullable || st.State != "available" {
		t.Errorf("xfce = %+v, want available and pullable", st)
	}
	// Installed but unknown to this build: visible, and not offered for download.
	if st := byName["mine"]; !st.Installed || st.Pullable || !st.Headless {
		t.Errorf("mine = %+v, want installed, not pullable, headless", st)
	}
	// Defined but not published: still listed, since it tells you the image
	// exists and must be built rather than downloaded.
	if st := byName["omarchy"]; st.Pullable {
		t.Errorf("omarchy = %+v, want a row with no download offered", st)
	}
}

// The installed image is the authority on what it is: its meta.json is what the
// daemon acts on, even if the registry disagrees.
func TestImageStatusesPreferTheInstalledMeta(t *testing.T) {
	dir := t.TempDir()
	installImage(t, dir, "headless", `{"headless":true}`)
	installImage(t, dir, "lxqt", `{"headless":true}`) // registry says otherwise
	s := testServer(t, dir)

	byName := map[string]ImageStatus{}
	for _, st := range s.imageStatuses() {
		byName[st.Name] = st
	}
	if !byName["lxqt"].Headless {
		t.Error("an installed meta.json saying headless should win over the registry")
	}
}

func TestPullRegistryAllowsOnePullPerImage(t *testing.T) {
	r := newPullRegistry()
	if _, ok := r.start("xfce"); !ok {
		t.Fatal("the first pull was refused")
	}
	if _, ok := r.start("xfce"); ok {
		t.Error("a second pull of the same image was allowed")
	}
	if _, ok := r.start("lxqt"); !ok {
		t.Error("a different image was refused")
	}
}

func TestPullStateTracksProgressAndFailure(t *testing.T) {
	r := newPullRegistry()
	job, _ := r.start("xfce")
	job.report(100, 900)
	st, ok := r.state("xfce")
	if !ok || !st.active || st.done != 100 || st.total != 900 {
		t.Fatalf("state = %+v, want active with 100/900", st)
	}
	job.finish(errors.New("no published image"))
	st, _ = r.state("xfce")
	if st.active {
		t.Error("a finished pull still reports as active")
	}
	if st.err == nil || st.err.Error() != "no published image" {
		t.Errorf("state error = %v, want the failure kept for the dashboard", st.err)
	}
	// A failed pull can be started again.
	if _, ok := r.start("xfce"); !ok {
		t.Error("a failed pull should be retryable")
	}
	// Unknown images have no state at all.
	if _, ok := r.state("never-heard-of-it"); ok {
		t.Error("an image nobody pulled reported a state")
	}
}
