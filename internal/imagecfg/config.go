// Package imagecfg reads the guest-image registry: one YAML file per image
// under deploy/images.
//
// A config is the single source of truth for an image. It picks the engine that
// builds it (an Alpine rootfs from a Dockerfile, or a staged EFI disk) and it
// produces the image's meta.json — the run-time contract the daemon reads.
// The daemon deliberately does not import this package: at run time it looks at
// what is installed under the images directory, never at what could be built.
//
// Rule that keeps the two from drifting: the config is the source, meta.json is
// generated. Never hand-edit an installed image's meta.json.
package imagecfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"warmbox/deploy/images"
	"warmbox/internal/desktop"
)

// Engines. Each one is a script under deploy/ that turns a config into image
// artifacts; they are genuinely different pipelines, so the config selects one
// rather than describing steps.
const (
	// EngineOverlay builds a read-only squashfs rootfs from a Dockerfile.
	EngineOverlay = "overlay"
	// EngineEFI stages an already-installed, EFI-bootable disk. Not hermetic:
	// it needs OMARCHY_SRC (or equivalent) from the host.
	EngineEFI = "efi"
)

// Config is one image in the registry.
type Config struct {
	// Name is the image name: the file name, the directory under
	// $WARMBOX_HOME/images, and what `--image <name>` takes.
	Name string `yaml:"name"`

	Engine string `yaml:"engine"`

	// Desktop, Theme and Browser are build knobs for the overlay engine.
	Desktop string `yaml:"desktop"`
	Theme   string `yaml:"theme"`
	Browser string `yaml:"browser"`

	// Headless bakes a no-screen marker into the guest. It is both a build knob
	// and a run-time fact, so it lands in meta.json too.
	Headless bool `yaml:"headless"`

	// Resolution is a build knob for the EFI engine.
	Resolution string `yaml:"resolution"`

	// Runtime becomes the image's meta.json: per-VM boot overrides. Leave it
	// empty to inherit whatever the daemon was started with.
	Runtime Runtime `yaml:"runtime"`

	// Publish ships this image on the `images` release.
	Publish bool `yaml:"publish"`
}

// Runtime mirrors the per-VM overrides the daemon understands.
type Runtime struct {
	GPU    string `yaml:"gpu"`
	MemMiB uint   `yaml:"mem_mib"`
	CPUs   uint   `yaml:"cpus"`
	Input  bool   `yaml:"input"`
	// AgentAPI is the guest-agent contract this image was built against; 0
	// means unstated (an image built before the field existed).
	AgentAPI int `yaml:"agent_api"`
}

// Desktops and browsers the overlay engine knows how to install. "none" is the
// screenless case: no X server, no session, none of the packages that make up
// a GUI image — which is most of its weight.
var (
	desktops = []string{"none", "xfce", "lxqt"}
	browsers = []string{"none", "netsurf", "epiphany", "firefox", "chromium"}
)

// Dir is the registry directory inside a checkout.
func Dir(repoRoot string) string { return filepath.Join(repoRoot, "deploy", "images") }

// Catalogue is the registry this binary shipped with: the images that exist for
// this build whether or not they are installed. Nil means the embedded registry
// could not be read, which is a build bug — the caller should carry on with
// whatever is installed rather than fail.
func Catalogue() []Config {
	cfgs, err := LoadFS(images.FS)
	if err != nil {
		return nil
	}
	return cfgs
}

// Find returns the config with this name, if the registry has one.
func Find(cfgs []Config, name string) (Config, bool) {
	for _, c := range cfgs {
		if c.Name == name {
			return c, true
		}
	}
	return Config{}, false
}

// Load reads every image config under dir, sorted by name.
func Load(dir string) ([]Config, error) { return LoadFS(os.DirFS(dir)) }

// LoadFS reads every image config in a filesystem, sorted by name. Names come
// from the file names, so an embedded registry and a checkout behave the same.
func LoadFS(fsys fs.FS) ([]Config, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("reading the image registry: %w", err)
	}
	var out []Config
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		c, err := loadFile(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no image configs in the registry")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// LoadOne reads a single image config by name from a registry directory.
func LoadOne(dir, name string) (Config, error) { return loadOne(os.DirFS(dir), name) }

func loadOne(fsys fs.FS, name string) (Config, error) {
	if !desktop.ValidImageName(name) {
		return Config{}, fmt.Errorf("invalid image name %q", name)
	}
	c, err := loadFile(fsys, name+".yaml")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			have, _ := names(fsys)
			return Config{}, fmt.Errorf("no image called %q in the registry (have: %s)",
				name, strings.Join(have, ", "))
		}
		return Config{}, err
	}
	return c, nil
}

// LoadFile reads and validates one config file from disk.
func LoadFile(path string) (Config, error) {
	return loadFile(os.DirFS(filepath.Dir(path)), filepath.Base(path))
}

// loadFile parses and validates one config out of a filesystem.
func loadFile(fsys fs.FS, name string) (Config, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", name, err)
	}
	if c.Name == "" {
		c.Name = strings.TrimSuffix(filepath.Base(name), ".yaml")
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", name, err)
	}
	return c, nil
}

// names lists the image names in a registry filesystem.
func names(fsys fs.FS) ([]string, error) {
	all, err := LoadFS(fsys)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(all))
	for _, c := range all {
		out = append(out, c.Name)
	}
	return out, nil
}

// Validate reports whether a config makes sense, and refuses combinations the
// engines cannot actually build rather than failing halfway through a build.
func (c Config) Validate() error {
	if !desktop.ValidImageName(c.Name) {
		return fmt.Errorf("invalid image name %q", c.Name)
	}
	switch c.Engine {
	case EngineOverlay:
		if !contains(desktops, c.Desktop) {
			return fmt.Errorf("desktop %q is not one of %v", c.Desktop, desktops)
		}
		if c.Theme == "" {
			return fmt.Errorf("theme is required for the overlay engine")
		}
		if !contains(browsers, c.Browser) {
			return fmt.Errorf("browser %q is not one of %v", c.Browser, browsers)
		}
		if c.Resolution != "" {
			return fmt.Errorf("resolution is an EFI-engine field (overlay sizing is --gpu at run time)")
		}
		// A desktop of "none" is an image with no X at all, so it can only be
		// a headless one, it has nothing to theme, and nothing to run a
		// browser in. Refusing these here rather than at boot keeps a config
		// from producing an image that boots to a failing Xvnc.
		if c.Desktop == "none" {
			if !c.Headless {
				return fmt.Errorf(`desktop "none" has no screen to show: headless must be true`)
			}
			if c.Theme != "default" {
				return fmt.Errorf(`desktop "none" pairs only with theme "default" (got %q)`, c.Theme)
			}
			if c.Browser != "none" {
				return fmt.Errorf(`desktop "none" cannot run browser %q: there is no X to run it in`, c.Browser)
			}
		}
	case EngineEFI:
		if c.Headless {
			// The EFI readiness unit waits for the VNC port, so a headless EFI
			// image would never report ready. See deploy/omarchy/README.md.
			return fmt.Errorf("the EFI engine cannot build a headless image yet")
		}
		if c.Desktop != "" || c.Browser != "" {
			return fmt.Errorf("desktop/browser are overlay-engine fields")
		}
		if c.Runtime.GPU == "" {
			return fmt.Errorf("an EFI image needs runtime.gpu: its compositor renders to virtio-gpu")
		}
	case "":
		return fmt.Errorf("engine is required (%s or %s)", EngineOverlay, EngineEFI)
	default:
		return fmt.Errorf("unknown engine %q (%s or %s)", c.Engine, EngineOverlay, EngineEFI)
	}
	if c.Runtime.AgentAPI < 0 {
		return fmt.Errorf("agent_api cannot be negative")
	}
	return nil
}

// Meta is the image's meta.json: the run-time half of this config.
func (c Config) Meta() desktop.ImageMeta {
	return desktop.ImageMeta{
		GPU:      c.Runtime.GPU,
		MemMiB:   c.Runtime.MemMiB,
		CPUs:     c.Runtime.CPUs,
		Input:    c.Runtime.Input,
		Headless: c.Headless,
		AgentAPI: c.Runtime.AgentAPI,
	}
}

// MetaJSON renders Meta as the single line handed to the build script.
func (c Config) MetaJSON() string {
	b, err := json.Marshal(c.Meta())
	if err != nil {
		// ImageMeta is all scalars; this cannot fail.
		return "{}"
	}
	return string(b)
}

// Script is the build script that implements this config's engine.
func (c Config) Script(deployDir string) string {
	if c.Engine == EngineEFI {
		return filepath.Join(deployDir, "omarchy", "build.sh")
	}
	return filepath.Join(deployDir, "guest", "build.sh")
}

// RequiresInputs reports an engine that cannot build from the checkout alone:
// the EFI engine stages a disk that was installed on a machine by hand.
func (c Config) RequiresInputs() bool { return c.Engine == EngineEFI }

// Env is what the build script needs, as environment variables. It is the
// script's existing vocabulary — the config is a nicer way to say the same
// thing, not a second build system.
func (c Config) Env(platform string) map[string]string {
	env := map[string]string{
		"META_JSON": c.MetaJSON(),
		// One docker tag per image: sharing `warmbox-guest:latest` meant
		// building lxqt silently re-tagged the image xfce had been built as.
		"IMAGE": "warmbox-guest:" + c.Name,
	}
	if platform != "" {
		env["PLATFORM"] = platform
	}
	switch c.Engine {
	case EngineOverlay:
		env["DESKTOP"] = c.Desktop
		env["THEME"] = c.Theme
		env["BROWSER"] = c.Browser
		env["VARIANT"] = c.Name
		if c.Headless {
			env["HEADLESS"] = "1"
		} else {
			env["HEADLESS"] = "0"
		}
	case EngineEFI:
		env["OMARCHY_NAME"] = c.Name
		env["RESOLUTION"] = c.Resolution
	}
	return env
}

// OutDir is where this image's artifacts land.
func (c Config) OutDir(workdir string) string {
	return filepath.Join(workdir, "images", c.Name)
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
