package imagecfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The configs that actually ship have to validate: a typo in deploy/images/
// should fail here, not halfway through a build on someone's laptop.
func TestCheckedInRegistryValidates(t *testing.T) {
	dir := filepath.Join("..", "..", "deploy", "images")
	cfgs, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(%s): %v", dir, err)
	}
	if len(cfgs) == 0 {
		t.Fatal("the registry is empty")
	}
	names := map[string]bool{}
	for _, c := range cfgs {
		if names[c.Name] {
			t.Errorf("duplicate image %q", c.Name)
		}
		names[c.Name] = true
		if c.Script(filepath.Join("..", "..", "deploy")) == "" {
			t.Errorf("%s: no engine script", c.Name)
		}
	}
	for _, want := range []string{DefaultImageName, "headless", "omarchy"} {
		if !names[want] {
			t.Errorf("registry is missing %q (have %v)", want, names)
		}
	}
}

// DefaultImageName mirrors what the daemon falls back to; the test above pins
// that the registry actually contains it.
const DefaultImageName = "xfce"

func TestValidateRejectsCombinationsTheEnginesCannotBuild(t *testing.T) {
	base := Config{Name: "x", Engine: EngineOverlay, Desktop: "xfce", Theme: "win11", Browser: "chromium"}
	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"no engine", func(c *Config) { c.Engine = "" }, "engine is required"},
		{"unknown engine", func(c *Config) { c.Engine = "docker" }, "unknown engine"},
		{"bad desktop", func(c *Config) { c.Desktop = "gnome" }, "desktop"},
		{"no theme", func(c *Config) { c.Theme = "" }, "theme is required"},
		{"bad browser", func(c *Config) { c.Browser = "netscape" }, "browser"},
		{"overlay with a resolution", func(c *Config) { c.Resolution = "800x600" }, "resolution"},
		{
			"headless efi",
			func(c *Config) {
				c.Engine = EngineEFI
				c.Desktop, c.Theme, c.Browser = "", "", ""
				c.Headless = true
				c.Runtime.GPU = "800x600"
			},
			"cannot build a headless image",
		},
		{
			"efi without a resolution",
			func(c *Config) {
				c.Engine = EngineEFI
				c.Desktop, c.Theme, c.Browser = "", "", ""
			},
			"runtime.gpu",
		},
		{
			"efi with overlay fields",
			func(c *Config) {
				c.Engine = EngineEFI
				c.Runtime.GPU = "800x600"
			},
			"overlay-engine fields",
		},
	}
	for _, tc := range cases {
		c := base
		tc.mutate(&c)
		err := c.Validate()
		if err == nil {
			t.Errorf("%s: accepted, want an error mentioning %q", tc.name, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not mention %q", tc.name, err, tc.want)
		}
	}
	if err := base.Validate(); err != nil {
		t.Errorf("the base config should be valid: %v", err)
	}
}

func TestMetaJSONIsTheRunTimeContract(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			"headless",
			Config{Headless: true},
			`{"headless":true}`,
		},
		{
			"nothing to override",
			Config{},
			`{}`,
		},
		{
			"efi sizing and contract",
			Config{Runtime: Runtime{GPU: "800x600", MemMiB: 3072, CPUs: 4, Input: true, AgentAPI: 1}},
			`{"gpu":"800x600","mem_mib":3072,"cpus":4,"input":true,"agent_api":1}`,
		},
	}
	for _, tc := range cases {
		if got := tc.cfg.MetaJSON(); got != tc.want {
			t.Errorf("%s: MetaJSON() = %s, want %s", tc.name, got, tc.want)
		}
	}

	// Fail loudly if an image declares a contract the daemon cannot read back.
	var m struct {
		AgentAPI int `json:"agent_api"`
	}
	if err := json.Unmarshal([]byte(cases[2].cfg.MetaJSON()), &m); err != nil {
		t.Fatal(err)
	}
	if m.AgentAPI != 1 {
		t.Errorf("agent_api did not survive the round trip: %d", m.AgentAPI)
	}
}

// The env is the script's existing vocabulary: the config is a nicer way to say
// the same thing, not a second build system.
func TestEnvSpeaksTheScriptsLanguage(t *testing.T) {
	overlay := Config{
		Name: "headless-lxqt", Engine: EngineOverlay,
		Desktop: "lxqt", Theme: "ambiance", Browser: "chromium", Headless: true,
	}
	got := overlay.Env("linux/arm64")
	for k, want := range map[string]string{
		"DESKTOP": "lxqt", "THEME": "ambiance", "BROWSER": "chromium",
		"VARIANT": "headless-lxqt", "HEADLESS": "1",
		"PLATFORM": "linux/arm64", "META_JSON": `{"headless":true}`,
		// One docker tag per image, so building one does not re-tag another.
		"IMAGE": "warmbox-guest:headless-lxqt",
	} {
		if got[k] != want {
			t.Errorf("overlay env %s = %q, want %q", k, got[k], want)
		}
	}
	// Not an EFI image, so it must not carry EFI variables.
	if _, ok := got["OMARCHY_NAME"]; ok {
		t.Error("overlay config leaked OMARCHY_NAME into the env")
	}

	efi := Config{Name: "omarchy", Engine: EngineEFI, Resolution: "800x600",
		Runtime: Runtime{GPU: "800x600"}}
	got = efi.Env("")
	if got["OMARCHY_NAME"] != "omarchy" || got["RESOLUTION"] != "800x600" {
		t.Errorf("efi env = %v", got)
	}
	if _, ok := got["DESKTOP"]; ok {
		t.Error("efi config leaked overlay variables into the env")
	}
	if _, ok := got["PLATFORM"]; ok {
		t.Error("an unset platform should not be passed through")
	}
	if efi.RequiresInputs() != true || overlay.RequiresInputs() != false {
		t.Error("RequiresInputs should be true only for the EFI engine")
	}
}

func TestLoadOneReportsAMissingImageUsefully(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lxqt.yaml"),
		[]byte("name: lxqt\nengine: overlay\ndesktop: lxqt\ntheme: ambiance\nbrowser: chromium\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOne(dir, "ghost"); err == nil || !strings.Contains(err.Error(), "lxqt") {
		t.Errorf("missing image error should list what exists, got %v", err)
	}
	// A name that could escape the images directory is refused before any file
	// is opened.
	if _, err := LoadOne(dir, "../etc/passwd"); err == nil {
		t.Error("a path-like name was accepted")
	}
}

func TestLoadFileRejectsInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(p, []byte("name: broken\nengine: overlay\ndesktop: gnome\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(p); err == nil {
		t.Fatal("an invalid config was accepted")
	}
}
