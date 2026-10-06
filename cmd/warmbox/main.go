// Command warmbox is the self-hosted GUI-desktop orchestrator: it boots
// microVMs from a pre-baked image with vfkit, keeps a warm pool, and streams
// each desktop to the browser over noVNC.
package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	_ "github.com/rclone/rclone/backend/all"
	rfs "github.com/rclone/rclone/fs"

	"warmbox/internal/api"
	"warmbox/internal/catalog"
	"warmbox/internal/config"
	"warmbox/internal/desktop"
	"warmbox/internal/egress"
	"warmbox/internal/imagecfg"
	"warmbox/internal/release"
	"warmbox/internal/update"
	"warmbox/internal/volume"
)

// version is the release string reported by `warmbox version` and /api/status.
// "dev" is a build that is not any release, so `warmbox version` says so and the
// update check stays quiet rather than claiming a release is newer than a tree
// it knows nothing about. Releases override it: goreleaser passes
// -X main.version={{ .Tag }}.
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		cmdHome()
		return
	}
	switch os.Args[1] {
	case "daemon":
		cmdDaemon(os.Args[2:])
	case "service":
		cmdService(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println(version)
	case "help", "-h", "--help":
		usage()
	case "setup":
		cmdSetup(os.Args[2:])
	case "login":
		cmdLogin(os.Args[2:])
	case "logout":
		cmdLogout(os.Args[2:])
	case "create":
		cmdCreate(os.Args[2:])
	case "images":
		cmdImages(os.Args[2:])
	case "image":
		cmdImage(os.Args[2:])
	case "list":
		cmdList(os.Args[2:])
	case "destroy":
		cmdDestroy(os.Args[2:])
	case "checkpoint":
		cmdCheckpoint(os.Args[2:])
	case "checkpoints":
		cmdCheckpoints(os.Args[2:])
	case "restore":
		cmdRestore(os.Args[2:])
	case "hibernate":
		cmdHibernate(os.Args[2:])
	case "wake":
		cmdWake(os.Args[2:])
	case "volume":
		cmdVolume(os.Args[2:])
	case "snapshot":
		cmdSnapshot(os.Args[2:])
	case "cloud":
		cmdCloud(os.Args[2:])
	default:
		unknownCommand(os.Args[1])
	}
}

// cmdHome is what a bare `warmbox` prints: where the daemon is and what to do
// next, instead of a wall of usage.
func cmdHome() {
	base := apiBase(":7070")
	fmt.Fprintln(os.Stderr, "warmbox — self-hosted GUI desktop microVMs")

	client := &http.Client{Timeout: 2 * time.Second}
	homeReq, _ := http.NewRequest(http.MethodGet, withToken(base+"/api/desktops", tokenDefault()), nil)
	if s := sessionCookieValue(); s != "" {
		homeReq.AddCookie(&http.Cookie{Name: "warmbox_session", Value: s})
	}
	resp, err := client.Do(homeReq)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\ndaemon: not running (%s)\n", base)
		fmt.Fprintln(os.Stderr, "  start it in the background:  warmbox service start")
		fmt.Fprintln(os.Stderr, "  or in the foreground:        warmbox daemon")
	} else if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		fmt.Fprintf(os.Stderr, "\ndaemon: running at %s, but no session\n", base)
		fmt.Fprintln(os.Stderr, "  sign in first:  warmbox login")
	} else {
		defer resp.Body.Close()
		var out struct {
			Desktops []struct {
				ID      string `json:"id"`
				State   string `json:"state"`
				GuestIP string `json:"guest_ip"`
			} `json:"desktops"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		fmt.Fprintf(os.Stderr, "\ndaemon: running at %s\n", base)
		fmt.Fprintf(os.Stderr, "dashboard: %s/\n", base)
		if len(out.Desktops) == 0 {
			fmt.Fprintln(os.Stderr, "desktops: none")
		} else {
			for _, d := range out.Desktops {
				fmt.Fprintf(os.Stderr, "  %-14s %-6s %s%s\n", d.ID, d.State, base, "/d/"+d.ID)
			}
		}
	}
	fmt.Fprintln(os.Stderr, "\ncommands: login · logout · create [--image NAME] · images · list · destroy · volume · snapshot · service")
	fmt.Fprintln(os.Stderr, "          warmbox help for everything")
}

// unknownCommand prints a suggestion for a mistyped command (e.g. "deamon").
func unknownCommand(cmd string) {
	known := []string{"daemon", "service", "setup", "login", "logout", "create", "images", "image",
		"list", "destroy", "volume", "snapshot", "version", "help"}
	best, dist := "", 3
	for _, k := range known {
		if d := editDistance(cmd, k); d < dist {
			best, dist = k, d
		}
	}
	if best != "" {
		fmt.Fprintf(os.Stderr, "unknown command %q — did you mean %q?\n", cmd, best)
	} else {
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
	}
	fmt.Fprintln(os.Stderr, "run `warmbox help` for the list")
	os.Exit(1)
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

func usage() {
	fmt.Fprint(os.Stderr, `warmbox — self-hosted GUI desktop orchestrator (vfkit + noVNC)

Usage:
  warmbox daemon            Run the orchestrator (web dashboard + REST API)
  warmbox service <cmd>     Manage the background daemon (launchd on macOS,
                            systemd on Linux):
                            install | start | stop | restart | status
  warmbox setup             Install what's missing: hypervisor, guest image, noVNC
  warmbox login             Sign in as a user (stores a session)
  warmbox logout            End the CLI session
  warmbox create            Provision a desktop, print its noVNC URL
  warmbox images            List the named guest images the daemon can boot
  warmbox image build <name> Build an image from the registry in deploy/images
                            (--all builds every image the checkout can build)
  warmbox image pack <name> Pack an image into <name>.tar.zst (compressed)
  warmbox image pull <name> [file|url]
                            Expand an image into the images directory; with no
                            source, the published image for this platform
  warmbox image list        List the local images (offline)
  warmbox list              List desktops
  warmbox destroy <id>      Destroy a desktop
  warmbox checkpoint <id> [name]
                            Save a desktop's live memory to disk (keeps running)
  warmbox checkpoints <id>  List a desktop's memory checkpoints
  warmbox restore <id> [name]
                            Roll a desktop back to a checkpoint, in place
  warmbox hibernate <id>    Checkpoint a desktop and stop it (no RAM, no CPU)
  warmbox wake <id>         Resume a hibernated desktop from its checkpoint
  warmbox version           Print the version

  warmbox volume create <name> [--size 8G] [--from <name>] [--from-snapshot <id>]
  warmbox volume list
  warmbox volume clone <name> <new>
  warmbox volume rm <name>

  warmbox snapshot create <volume> [--name <id>]
  warmbox snapshot list
  warmbox snapshot rm <id>

  warmbox cloud set --bucket <name> --endpoint <url> --access-key <k> --secret-key <s>
  warmbox cloud show        Show where volume bytes are stored
  warmbox cloud clear       Forget the cloud settings (back to local storage)

The daemon needs vfkit (brew install vfkit) and a pre-baked guest image
(see deploy/guest/Dockerfile).
`)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// stat reports whether path is non-empty and exists.
func stat(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func addCommonFlags(fs *flag.FlagSet, cfg *desktop.Config) {
	fs.StringVar(&cfg.WorkDir, "workdir", cfg.WorkDir, "state directory")
	fs.StringVar(&cfg.Image, "image", cfg.Image, "default guest image to boot and warm (default: xfce)")
	fs.StringVar(&cfg.ImageDir, "image-dir", cfg.ImageDir, "directory of guest images (<name>/ with meta.json)")
	fs.StringVar(&cfg.GPU, "gpu", cfg.GPU, "virtio-gpu size, e.g. 1440x900 (empty = headless)")
	fs.BoolVar(&cfg.Input, "input", cfg.Input, "attach virtio keyboard/pointing devices")
	fs.StringVar(&cfg.Accel, "accel", cfg.Accel, "QEMU accelerator: kvm (default) or tcg (software emulation, slow)")
	fs.BoolVar(&cfg.GUI, "gui", cfg.GUI, "open the hypervisor window (vfkit only; bring-up aid)")
	fs.StringVar(&cfg.TLSCert, "tls-cert", cfg.TLSCert, "serve the dashboard over HTTPS with this certificate")
	fs.StringVar(&cfg.TLSKey, "tls-key", cfg.TLSKey, "private key for --tls-cert")
	fs.BoolVar(&cfg.Insecure, "insecure", cfg.Insecure, "allow a non-loopback --addr with no TLS (plaintext cookies and console)")
	fs.StringVar(&cfg.NoVNCDir, "novnc", cfg.NoVNCDir, "noVNC asset directory")
	fs.StringVar(&cfg.VfkitPath, "vfkit", cfg.VfkitPath, "vfkit binary")
	fs.StringVar(&cfg.Backend, "backend", cfg.Backend, "hypervisor backend (vfkit)")
	fs.StringVar(&cfg.APIAddr, "addr", cfg.APIAddr, "HTTP listen address (dashboard + API)")
	fs.StringVar(&cfg.GuestAddr, "guest-addr", cfg.GuestAddr, "address for guest readiness callbacks (default: the guest gateway on the daemon port; \"off\" disables)")
	fs.StringVar(&cfg.HostAddr, "host", cfg.HostAddr, "address guests use to reach this host")
	fs.UintVar(&cfg.MemMiB, "mem", cfg.MemMiB, "memory per VM (MiB)")
	fs.UintVar(&cfg.CPUs, "cpus", cfg.CPUs, "vCPUs per VM")
	fs.IntVar(&cfg.PoolSize, "pool", cfg.PoolSize, "warm pool size")
	fs.DurationVar(&cfg.PoolIdleTimeout, "pool-idle-timeout", cfg.PoolIdleTimeout, "drain warm-pool VMs after this long without a lease, reclaiming their RAM (0 = keep warm)")
	fs.StringVar(&cfg.ShareDir, "share", cfg.ShareDir, "host directory shared with guests via virtiofs (mounted at /workspace)")
	fs.StringVar(&cfg.ShareTag, "share-tag", cfg.ShareTag, "virtiofs mount tag")
	fs.StringVar(&cfg.Token, "token", cfg.Token, "deprecated and ignored; use account login")
	fs.StringVar(&cfg.VolumeDir, "volume-dir", cfg.VolumeDir, "local cache dir for volume images")
	fs.StringVar(&cfg.VolumeBase, "volume-base", cfg.VolumeBase, "base ext4 image cloned for new volumes")
	fs.IntVar(&cfg.VolumeChunkMiB, "volume-chunk", cfg.VolumeChunkMiB, "volume transfer chunk size (MiB)")
	fs.StringVar(&cfg.VolumePrefix, "volume-prefix", cfg.VolumePrefix, "remote prefix for volumes")
	fs.StringVar(&cfg.EgressAddr, "egress", cfg.EgressAddr, "run the egress policy proxy on this address (e.g. :8099; empty = off)")
	fs.Var(listFlag{&cfg.Allow}, "allow", "comma-separated allowed domains for guest egress (default-deny when set)")
	fs.Var(listFlag{&cfg.Deny}, "deny", "comma-separated denied domains for guest egress")
}

// listFlag collects a comma-separated flag into a []string.
type listFlag struct{ v *[]string }

func (l listFlag) String() string {
	if l.v == nil {
		return ""
	}
	return strings.Join(*l.v, ",")
}
func (l listFlag) Set(s string) error {
	*l.v = egress.ParseList(s)
	return nil
}

const warmboxLabel = "com.warmbox.daemon"

func launchAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", warmboxLabel+".plist")
}

// launchdLoaded reports whether the warmbox background service is loaded.
func launchdLoaded() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	c := exec.Command("launchctl", "print", domain+"/"+warmboxLabel)
	c.Stdout, c.Stderr = nil, nil
	return c.Run() == nil
}

// preflightAddr refuses to start a daemon on an address another one holds: that
// would silently run a second orchestrator (e.g. alongside the launchd service)
// with different settings. A short retry covers a service handover.
func preflightAddr(addr string) {
	var lerr error
	for i := 0; i < 6; i++ {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			ln.Close()
			return
		}
		lerr = err
		time.Sleep(500 * time.Millisecond)
	}
	if serviceRunning() {
		fatal("another daemon is already listening on %s (the background service).\n  stop it first:  warmbox service stop\n  (or run this on a different --addr)", addr)
	}
	fatal("can't listen on %s: %v", addr, lerr)
}

// serviceToken used to mint the shared daemon token at ~/.warmbox/token. The
// daemon now ignores --token entirely (accounts own access), so new service
// installs no longer write it; the flag itself stays accepted so plists written
// by older versions keep starting.

// appendServiceFlags puts the configured knobs on the daemon's command line, so
// a service runs with what it was installed with instead of the built-in
// defaults. Anything still at its default is left off, which keeps the unit
// honest about what was chosen and lets a later default reach people who never
// asked for anything.
//
// Sizing used to be accepted by `service install` and then silently dropped:
// the unit was written without it, the daemon started on 4 GiB and 4 vCPUs, and
// nothing anywhere said the flag had been ignored. On a small host that is more
// memory than the machine has.
func appendServiceFlags(args []string, cfg *desktop.Config) []string {
	def := desktop.DefaultConfig()
	if cfg.MemMiB != def.MemMiB {
		args = append(args, "--mem", fmt.Sprint(cfg.MemMiB))
	}
	if cfg.CPUs != def.CPUs {
		args = append(args, "--cpus", fmt.Sprint(cfg.CPUs))
	}
	if cfg.GPU != def.GPU {
		args = append(args, "--gpu", cfg.GPU)
	}
	if cfg.Input != def.Input {
		args = append(args, "--input")
	}
	if cfg.Accel != def.Accel {
		args = append(args, "--accel", cfg.Accel)
	}
	if cfg.GuestAddr != def.GuestAddr {
		args = append(args, "--guest-addr", cfg.GuestAddr)
	}
	// A service bound to the network needs its certificate remembered, or it
	// would refuse to start when the unit runs it without one.
	if cfg.TLSCert != "" {
		args = append(args, "--tls-cert", cfg.TLSCert)
	}
	if cfg.TLSKey != "" {
		args = append(args, "--tls-key", cfg.TLSKey)
	}
	if cfg.Insecure {
		args = append(args, "--insecure")
	}
	return args
}

func writeLaunchAgent(cfg *desktop.Config, pool int) error {
	if vf, err := exec.LookPath(cfg.VfkitPath); err == nil {
		cfg.VfkitPath = vf
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(cfg.WorkDir, "daemon.log")

	args := []string{
		self, "daemon",
		"--workdir", cfg.WorkDir,
		"--addr", cfg.APIAddr,
		"--pool", fmt.Sprint(pool),
		"--pool-idle-timeout", cfg.PoolIdleTimeout.String(),
		"--vfkit", cfg.VfkitPath,
	}
	// Sizing is persisted so `service install --mem 1024` means something. It
	// used to be accepted and silently dropped, which left the daemon on the
	// built-in 4 GiB and 4 vCPUs with no way to tell it otherwise — on a small
	// host that is the whole machine.
	if cfg.Image != "" {
		args = append(args, "--image", cfg.Image)
	}
	args = appendServiceFlags(args, cfg)
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace
	var b strings.Builder
	for _, a := range args {
		fmt.Fprintf(&b, "    <string>%s</string>\n", esc(a))
	}

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
%s  </array>
  <key>EnvironmentVariables</key>
  <dict><key>PATH</key><string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string></dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, warmboxLabel, b.String(), esc(logPath), esc(logPath))
	if err := os.MkdirAll(filepath.Dir(launchAgentPath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(launchAgentPath(), []byte(plist), 0o644)
}

// cmdService manages the warmbox daemon as a background service: launchd on
// macOS, systemd on Linux.
func cmdService(args []string) {
	switch runtime.GOOS {
	case "darwin":
		serviceLaunchd(args)
	case "linux":
		serviceSystemd(args)
	default:
		fatal("service management supports macOS (launchd) and Linux (systemd); elsewhere run `warmbox daemon` under your init system")
	}
}

// serviceLaunchd manages the daemon as a launchd user agent (macOS).
func serviceLaunchd(args []string) {
	cfg := desktop.DefaultConfig()
	pool := 0
	fs := flag.NewFlagSet("service", flag.ExitOnError)
	fs.StringVar(&cfg.WorkDir, "workdir", cfg.WorkDir, "state directory")
	fs.StringVar(&cfg.APIAddr, "addr", cfg.APIAddr, "HTTP listen address (dashboard + API)")
	fs.StringVar(&cfg.GuestAddr, "guest-addr", cfg.GuestAddr, "address for guest readiness callbacks (default: the guest gateway on the daemon port; \"off\" disables)")
	fs.StringVar(&cfg.Image, "image", cfg.Image, "default image")
	fs.UintVar(&cfg.MemMiB, "mem", cfg.MemMiB, "memory per VM (MiB)")
	fs.UintVar(&cfg.CPUs, "cpus", cfg.CPUs, "vCPUs per VM")
	fs.StringVar(&cfg.GPU, "gpu", cfg.GPU, "virtio-gpu size, e.g. 1440x900 (empty = headless)")
	fs.BoolVar(&cfg.Input, "input", cfg.Input, "attach virtio keyboard/pointing devices")
	fs.StringVar(&cfg.TLSCert, "tls-cert", cfg.TLSCert, "serve the dashboard over HTTPS with this certificate")
	fs.StringVar(&cfg.TLSKey, "tls-key", cfg.TLSKey, "private key for --tls-cert")
	fs.BoolVar(&cfg.Insecure, "insecure", cfg.Insecure, "allow a non-loopback --addr with no TLS")
	fs.IntVar(&pool, "pool", 0, "warm pool size (0 = boot on demand; 1+ = instant create, holds RAM)")
	sub := ""
	if len(args) > 0 {
		sub = args[0]
		_ = fs.Parse(args[1:])
	}

	setFlags := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })

	domain := fmt.Sprintf("gui/%d", os.Getuid())
	target := domain + "/" + warmboxLabel
	lc := func(a ...string) error {
		cmd := exec.Command("launchctl", a...)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		return cmd.Run()
	}
	lcQuiet := func(a ...string) error {
		return exec.Command("launchctl", a...).Run()
	}
	loaded := func() bool {
		c := exec.Command("launchctl", "print", target)
		c.Stdout, c.Stderr = nil, nil
		return c.Run() == nil
	}
	// bootstrap can race a just-issued bootout (launchd returns before the old
	// process exits), so retry briefly.
	bootstrap := func() error {
		var err error
		for i := 0; i < 10; i++ {
			if err = lc("bootstrap", domain, launchAgentPath()); err == nil {
				return nil
			}
			time.Sleep(400 * time.Millisecond)
		}
		return err
	}
	// Rewrite the plist when asked to (install), when flags are given, or when
	// no plist exists yet — but not on a bare start/restart, which must keep
	// the configured pool/image.
	needWrite := sub == "install" || len(setFlags) > 0 || !stat(launchAgentPath())

	switch sub {
	case "install", "start":
		if err := cfg.EnsureDirs(); err != nil {
			fatal("Error: %v", err)
		}
		if needWrite {
			if err := writeLaunchAgent(cfg, pool); err != nil {
				fatal("Error: %v", err)
			}
		}
		if sub == "install" || !loaded() {
			if sub == "install" {
				_ = lcQuiet("bootout", target)
			}
			if err := bootstrap(); err != nil {
				fatal("Error: %v", err)
			}
		} else if err := lc("kickstart", target); err != nil {
			fatal("Error: %v", err)
		}
		fmt.Fprintf(os.Stderr, "started %s\n", warmboxLabel)
	case "stop":
		if !loaded() {
			fmt.Fprintln(os.Stderr, "not running")
			return
		}
		if err := lc("bootout", target); err != nil {
			fatal("Error: %v", err)
		}
		fmt.Fprintln(os.Stderr, "stopped")
	case "restart":
		if needWrite {
			if err := writeLaunchAgent(cfg, pool); err != nil {
				fatal("Error: %v", err)
			}
		}
		if loaded() {
			_ = lcQuiet("bootout", target)
		}
		if err := bootstrap(); err != nil {
			fatal("Error (install first?): %v", err)
		}
		fmt.Fprintln(os.Stderr, "restarted")
	case "status":
		if !loaded() {
			fmt.Fprintln(os.Stderr, "not running")
			return
		}
		_ = lc("print", target)
	default:
		fatal("Usage: warmbox service <install|start|stop|restart|status> [--pool N] [--image NAME]")
	}
}

// --- systemd (Linux) ---

// warmboxUnit is the systemd unit name; the launchd equivalent is warmboxLabel.
const warmboxUnit = "warmbox.service"

// systemdUserScope reports whether we manage a per-user unit rather than a
// machine-wide one: root installs a system unit (usable for KVM and networking
// without extra grants), everyone else a user unit.
func systemdUserScope() bool { return os.Geteuid() != 0 }

func systemdUnitPath() string {
	if !systemdUserScope() {
		return filepath.Join("/etc/systemd/system", warmboxUnit)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", warmboxUnit)
}

// systemctlArgs prefixes the verb with --user when we manage a user unit.
func systemctlArgs(verb ...string) []string {
	if systemdUserScope() {
		return append([]string{"--user"}, verb...)
	}
	return verb
}

func systemctlOut() string {
	if systemdUserScope() {
		return "--user "
	}
	return ""
}

// systemdActive reports whether the unit exists and is running.
func systemdActive() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	cmd := exec.Command("systemctl", systemctlArgs("is-active", "--quiet", warmboxUnit)...)
	cmd.Stdout, cmd.Stderr = nil, nil
	return cmd.Run() == nil
}

// systemdState is the unit's state as systemd sees it ("active", "activating",
// "failed", …), for reporting honestly when it will not stay up.
func systemdState() string {
	out, err := exec.Command("systemctl", systemctlArgs("is-active", warmboxUnit)...).Output()
	if err != nil && len(out) == 0 {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// systemdWaitActive waits briefly for the unit to settle. A daemon that is
// missing QEMU or the guest image exits immediately, and with Restart=always
// systemd reports "activating" — worth waiting to distinguish that from a
// success.
func systemdWaitActive(within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if systemdState() == "active" {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// serviceRunning reports whether the background service is running: launchd on
// macOS, systemd on Linux. Used to explain a port that is already taken.
func serviceRunning() bool {
	switch runtime.GOOS {
	case "darwin":
		return launchdLoaded()
	case "linux":
		return systemdActive()
	}
	return false
}

// systemdQuote renders one ExecStart argument the way systemd parses it.
func systemdQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\"'\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// writeSystemdUnit renders the unit from the same settings the launchd plist
// carries, so both hosts describe the same daemon.
func writeSystemdUnit(cfg *desktop.Config, pool int, backend string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{
		self, "daemon",
		"--workdir", cfg.WorkDir,
		"--addr", cfg.APIAddr,
		"--pool", fmt.Sprint(pool),
		"--pool-idle-timeout", cfg.PoolIdleTimeout.String(),
		"--backend", backend,
	}
	if cfg.Image != "" {
		args = append(args, "--image", cfg.Image)
	}
	args = appendServiceFlags(args, cfg)
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		quoted = append(quoted, systemdQuote(a))
	}
	// A system unit waits for the network and is wanted by multi-user.target. The
	// per-user manager has no network-online.target, so a user unit just wants
	// default.target (and stops when the user's last session ends unless
	// lingering is on — see lingerHint).
	after, wantedBy := "After=network-online.target\nWants=network-online.target\n", "multi-user.target"
	if systemdUserScope() {
		after, wantedBy = "", "default.target"
	}
	// A system service inherits almost no environment, and warmbox resolves its
	// default paths (~/.warmbox/vmlinux, …) from $HOME. Without HOME the daemon
	// ends up looking for ".warmbox/vmlinux" relative to the root directory and
	// dies at startup, so set it explicitly.
	home, herr := os.UserHomeDir()
	if herr != nil || home == "" {
		return fmt.Errorf("can't determine the home directory to run the service from: %w", herr)
	}
	unit := fmt.Sprintf(`[Unit]
Description=warmbox — self-hosted GUI desktop microVMs
%s
[Service]
Type=simple
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
Environment=HOME=%s
ExecStart=%s
Restart=always
RestartSec=2
# The daemon parents each VM process, so stop the whole tree together.
KillMode=mixed
TimeoutStopSec=30

[Install]
WantedBy=%s
`, after, systemdQuote(home), strings.Join(quoted, " "), wantedBy)

	if err := os.MkdirAll(filepath.Dir(systemdUnitPath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(systemdUnitPath(), []byte(unit), 0o644)
}

// lingerHint makes a user service survive logout (and therefore start at boot).
// Without it, systemd stops the unit when the last session ends.
func lingerHint() {
	user := os.Getenv("USER")
	if user == "" {
		user = fmt.Sprint(os.Getuid())
	}
	cmd := exec.Command("loginctl", "enable-linger", user)
	cmd.Stdout, cmd.Stderr = nil, nil
	if cmd.Run() == nil {
		fmt.Fprintln(os.Stderr, "enabled lingering: the service starts at boot, no login needed")
		return
	}
	fmt.Fprintf(os.Stderr, "note: a user service stops when you log out. To start it at boot:\n  sudo loginctl enable-linger %s\n", user)
}

// serviceSystemd manages the daemon as a systemd unit (Linux).
func serviceSystemd(args []string) {
	cfg := desktop.DefaultConfig()
	pool := 0
	backend := "qemu"
	fs := flag.NewFlagSet("service", flag.ExitOnError)
	fs.StringVar(&cfg.WorkDir, "workdir", cfg.WorkDir, "state directory")
	fs.StringVar(&cfg.APIAddr, "addr", cfg.APIAddr, "HTTP listen address (dashboard + API)")
	fs.StringVar(&cfg.GuestAddr, "guest-addr", cfg.GuestAddr, "address for guest readiness callbacks (default: the guest gateway on the daemon port; \"off\" disables)")
	fs.StringVar(&cfg.Image, "image", cfg.Image, "default image")
	fs.UintVar(&cfg.MemMiB, "mem", cfg.MemMiB, "memory per VM (MiB)")
	fs.UintVar(&cfg.CPUs, "cpus", cfg.CPUs, "vCPUs per VM")
	fs.StringVar(&cfg.GPU, "gpu", cfg.GPU, "virtio-gpu size, e.g. 1440x900 (empty = headless)")
	fs.BoolVar(&cfg.Input, "input", cfg.Input, "attach virtio keyboard/pointing devices")
	fs.StringVar(&cfg.Accel, "accel", cfg.Accel, "QEMU accelerator: kvm (default) or tcg (software emulation, slow)")
	fs.StringVar(&cfg.TLSCert, "tls-cert", cfg.TLSCert, "serve the dashboard over HTTPS with this certificate")
	fs.StringVar(&cfg.TLSKey, "tls-key", cfg.TLSKey, "private key for --tls-cert")
	fs.BoolVar(&cfg.Insecure, "insecure", cfg.Insecure, "allow a non-loopback --addr with no TLS")
	fs.IntVar(&pool, "pool", 0, "warm pool size (0 = boot on demand; 1+ = instant create, holds RAM)")
	fs.StringVar(&backend, "backend", backend, "hypervisor backend (qemu on Linux)")
	sub := ""
	if len(args) > 0 {
		sub = args[0]
		_ = fs.Parse(args[1:])
	}

	setFlags := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })

	// Confirm the daemon actually stays up: systemd reports success as soon as
	// the process is spawned, so a daemon that cannot start (no QEMU, no guest
	// image) would otherwise look like a win while it crash-loops.
	verifyUp := func() {
		if systemdWaitActive(10 * time.Second) {
			return
		}
		fmt.Fprintf(os.Stderr, "✗ %s is not staying up (systemd reports %q)\n", warmboxUnit, systemdState())
		fmt.Fprintf(os.Stderr, "  the daemon exits immediately when something is missing; see why:\n")
		fmt.Fprintf(os.Stderr, "    journalctl %s-u %s -n 20 --no-pager\n", systemctlOut(), warmboxUnit)
		fmt.Fprintln(os.Stderr, "  then re-run:  warmbox setup")
		os.Exit(1)
	}

	unitPath := systemdUnitPath()
	sc := func(a ...string) error {
		cmd := exec.Command("systemctl", systemctlArgs(a...)...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}
	// Rewrite the unit when asked to (install), when flags are given, or when no
	// unit exists yet — but not on a bare start/restart, which must keep the
	// configured pool/image.
	needWrite := sub == "install" || len(setFlags) > 0 || !stat(unitPath)

	switch sub {
	case "install", "start":
		if err := cfg.EnsureDirs(); err != nil {
			fatal("Error: %v", err)
		}
		if needWrite {
			if err := writeSystemdUnit(cfg, pool, backend); err != nil {
				fatal("Error: %v", err)
			}
		}
		if err := sc("daemon-reload"); err != nil {
			fatal("Error: %v", err)
		}
		if sub == "install" {
			if err := sc("enable", warmboxUnit); err != nil {
				fatal("Error: %v", err)
			}
		}
		verb := "start"
		if systemdActive() {
			verb = "restart"
		}
		if err := sc(verb, warmboxUnit); err != nil {
			fatal("Error: %v", err)
		}
		fmt.Fprintf(os.Stderr, "started %s\n", warmboxUnit)
		verifyUp()
		fmt.Fprintf(os.Stderr, "unit: %s\nlogs: journalctl %s-u %s -f\n", unitPath, systemctlOut(), warmboxUnit)
		if sub == "install" && systemdUserScope() {
			lingerHint()
		}
	case "stop":
		if !systemdActive() {
			fmt.Fprintln(os.Stderr, "not running")
			return
		}
		if err := sc("stop", warmboxUnit); err != nil {
			fatal("Error: %v", err)
		}
		fmt.Fprintln(os.Stderr, "stopped")
	case "restart":
		if needWrite {
			if err := writeSystemdUnit(cfg, pool, backend); err != nil {
				fatal("Error: %v", err)
			}
			if err := sc("daemon-reload"); err != nil {
				fatal("Error: %v", err)
			}
		}
		verb := "start"
		if systemdActive() {
			verb = "restart"
		}
		if err := sc(verb, warmboxUnit); err != nil {
			fatal("Error (install first?): %v", err)
		}
		verifyUp()
		fmt.Fprintln(os.Stderr, "restarted")
	case "status":
		if !stat(unitPath) {
			fmt.Fprintf(os.Stderr, "not installed (%s)\n", unitPath)
			return
		}
		if !systemdActive() {
			fmt.Fprintln(os.Stderr, "not running")
		}
		_ = sc("status", "--no-pager", warmboxUnit)
		fmt.Fprintf(os.Stderr, "unit: %s\nlogs: journalctl %s-u %s -f\n", unitPath, systemctlOut(), warmboxUnit)
	default:
		fatal("Usage: warmbox service <install|start|stop|restart|status> [--pool N] [--image NAME]")
	}
}

func cmdDaemon(args []string) {
	cfg := desktop.DefaultConfig()
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	addCommonFlags(fs, cfg)
	_ = fs.Parse(args)

	if cfg.Token != "" {
		fmt.Fprintln(os.Stderr, "warmbox: --token is deprecated and ignored; sign in through setup or `warmbox login`")
	}
	if err := cfg.EnsureDirs(); err != nil {
		fatal("Error: %v", err)
	}
	preflightAddr(cfg.APIAddr)
	requireSafeBind(cfg)
	// Clear anything a previous daemon left behind before booting new VMs: we
	// hold the address now, so survivors belong to a crashed run (RAM, control
	// sockets and attached volumes included).
	if n := desktop.Reap(cfg, os.Stderr); n > 0 {
		fmt.Fprintf(os.Stderr, "warmbox: reaped %d leftover vm(s) from a previous run\n", n)
	}
	mgr := desktop.NewManager(cfg, os.Stderr)
	images := mgr.Images()
	if len(images) == 0 {
		fatal("no guest image in %s\n  run `warmbox setup` to install one,\n  or build one with `warmbox image build <name>` (see deploy/images/)", cfg.ImageDir)
	}
	fmt.Fprintf(os.Stderr, "warmbox: images: %v\n", images)
	// The default image must be one of them: a daemon whose --image points at
	// something it cannot boot would fail every unnamed create instead of
	// failing here, where the reason is obvious.
	deflt := desktop.DefaultFor(cfg.Image)
	if !slices.Contains(images, deflt) {
		fatal("default image %q not found under %s (have: %v)", deflt, cfg.ImageDir, images)
	}
	// An image built against a newer guest agent may make calls this daemon
	// cannot serve; say so now rather than at the first exec.
	for name, meta := range mgr.ImageMetas() {
		if meta.AgentAPI > desktop.AgentAPI {
			fmt.Fprintf(os.Stderr, "warmbox: warning: image %q targets agent API %d, this daemon speaks %d\n",
				name, meta.AgentAPI, desktop.AgentAPI)
		}
	}
	// A guest sized larger than its host is not caught anywhere else: it boots,
	// it reports ready, and then everything crawls while the host swaps. Say so
	// here, while the numbers are still in front of whoever set them.
	if total := hostRAMMiB(); total > 0 {
		pool := cfg.PoolSize + 1 // the pool, plus the desktop someone will ask for
		switch {
		case int(cfg.MemMiB) > total:
			fmt.Fprintf(os.Stderr, "warmbox: warning: each VM is sized at %d MiB, more than this host's %d MiB\n", cfg.MemMiB, total)
			fmt.Fprintln(os.Stderr, "warmbox:   lower it with --mem (a service keeps what `service install --mem` was given)")
		case int(cfg.MemMiB)*pool > total*9/10:
			fmt.Fprintf(os.Stderr, "warmbox: warning: %d VM(s) at %d MiB is %d MiB on a %d MiB host\n",
				pool, cfg.MemMiB, int(cfg.MemMiB)*pool, total)
			fmt.Fprintln(os.Stderr, "warmbox:   lower --pool or --mem if creates start to crawl")
		}
	}

	p := desktop.NewPool(mgr, cfg.PoolSize, cfg.PoolIdleTimeout, os.Stderr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, backedBy := newVolumeStore(ctx, cfg)
	cat := openCatalog(cfg)
	if store != nil {
		backfillCatalog(ctx, cat, store, cfg)
		mgr.SetOnDestroy(func(vm *desktop.VM) {
			if vm.Volume == "" {
				return
			}
			cctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := store.Commit(cctx, vm.Volume); err != nil {
				fmt.Fprintf(os.Stderr, "volume: commit %s: %v\n", vm.Volume, err)
			}
			if cat != nil {
				_ = cat.ReleaseLease(vm.Volume, vm.ID)
			} else {
				store.Release(vm.Volume, vm.ID)
			}
		})
	}

	go p.Run(ctx)

	apiSrv := api.New(mgr, p, cfg, store, cat, os.Stderr)

	if cfg.EgressAddr != "" {
		policy := egress.Policy{Allow: cfg.Allow, Deny: cfg.Deny}
		eg := egress.New(cfg.EgressAddr, policy, os.Stderr, apiSrv.EgressPolicyForIP)
		if err := eg.Start(); err != nil {
			fatal("egress proxy: %v", err)
		}
		fmt.Fprintf(os.Stderr, "warmbox: egress policy on %s (default-deny=%v allow=%v deny=%v)\n",
			cfg.EgressAddr, policy.DefaultDeny(), cfg.Allow, cfg.Deny)
	}
	apiSrv.SetStatusInfo(api.StatusInfo{
		Version:       version,
		Backend:       cfg.Backend,
		VolumesBacked: backedBy,
		DefaultImage:  desktop.DefaultFor(cfg.Image),
	})
	// "Is there something newer?" for the Daemon tab. It runs in the
	// background and caches its answer: /api/status is polled, GitHub's API is
	// rate-limited, and a settings page should never wait on the network. The
	// installed image's checksum lives beside the image, so a daemon started
	// after `warmbox setup` picks the new one up on its own.
	updater := update.New(update.Config{
		Repo:         release.Repo,
		Current:      version,
		Arch:         runtime.GOARCH,
		ImageURL:     release.ImageURL(desktop.DefaultFor(cfg.Image), runtime.GOARCH),
		ImageRelease: release.ImagesTag,
		ImageSHA: func() string {
			return desktop.ReadImageSHA(filepath.Join(cfg.ImageDir, desktop.DefaultFor(cfg.Image)))
		},
	})
	apiSrv.SetUpdater(updater)
	updater.Start(ctx)
	srv := &http.Server{Addr: cfg.APIAddr, Handler: apiSrv.Handler()}
	go func() {
		fmt.Fprintf(os.Stderr, "warmbox: listening on %s (pool=%d)\n", cfg.APIAddr, cfg.PoolSize)
		var err error
		if cfg.TLSCert != "" && cfg.TLSKey != "" {
			fmt.Fprintf(os.Stderr, "warmbox: TLS on: %s\n", cfg.TLSCert)
			err = srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "warmbox: server error: %v\n", err)
			stop()
		}
	}()
	// Guests dial the gateway address, never loopback, so their readiness
	// callbacks get a separate listener that serves nothing else. Keeping the
	// dashboard on --addr means it is not reachable from the network.
	guestSrv := serveGuestCallbacks(apiSrv, cfg)

	<-ctx.Done()
	fmt.Fprintln(os.Stderr, "warmbox: shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	if guestSrv != nil {
		_ = guestSrv.Shutdown(shutCtx)
	}
	mgr.Shutdown()
}

// serveGuestCallbacks listens for guest readiness callbacks on the gateway
// address guests are told to dial. The vmnet interface may not exist until the
// first VM boots, so it retries instead of failing startup. Returns the server
// (nil when no separate listener is needed) for shutdown.
func serveGuestCallbacks(apiSrv *api.Server, cfg *desktop.Config) *http.Server {
	addr := guestListenAddr(cfg)
	if addr == "" {
		return nil
	}
	srv := &http.Server{Handler: apiSrv.GuestHandler()}
	go func() {
		warned := false
		for {
			ln, err := net.Listen("tcp", addr)
			if err == nil {
				fmt.Fprintf(os.Stderr, "warmbox: guest callbacks on %s\n", addr)
				if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
					fmt.Fprintf(os.Stderr, "warmbox: guest listener: %v\n", err)
				}
				return
			}
			if !warned {
				fmt.Fprintf(os.Stderr, "warmbox: waiting for %s before guests can report ready (%v)\n", addr, err)
				warned = true
			}
			time.Sleep(2 * time.Second)
		}
	}()
	return srv
}

// guestListenAddr is where the guest-only listener belongs: the gateway guests
// dial, on the daemon's port. Empty when the main listener already covers it
// (wildcard bind, or the same address) or when it has been turned off.
func guestListenAddr(cfg *desktop.Config) string {
	if cfg.GuestAddr == "off" {
		return ""
	}
	if cfg.GuestAddr != "" {
		return cfg.GuestAddr
	}
	if cfg.HostAddr == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(cfg.APIAddr)
	if err != nil || port == "" {
		return ""
	}
	if host == "" {
		// A wildcard bind already answers on the gateway address.
		return ""
	}
	addr := net.JoinHostPort(cfg.HostAddr, port)
	if addr == cfg.APIAddr {
		return ""
	}
	return addr
}

// newVolumeStore builds a volume store backed by the configured cloud remote
// (S3/R2) when credentials are set, otherwise by a local directory so volumes
// still work for local development. It returns the store and a short human
// description of where volume bytes are kept (surfaced in the dashboard). The
// store is nil if none could be created.
func newVolumeStore(ctx context.Context, cfg *desktop.Config) (*volume.Store, string) {
	chunk := int64(cfg.VolumeChunkMiB) << 20

	if rc, err := config.LoadGlobal(); err == nil && rc != nil && rc.DefaultBucket != "" {
		if f, err := rc.NewFs(ctx, rc.DefaultBucket, ""); err == nil {
			fmt.Fprintf(os.Stderr, "warmbox: volumes backed by remote bucket %q\n", rc.DefaultBucket)
			return volume.NewStore(f, cfg.VolumePrefix, cfg.VolumeDir, cfg.VolumeBase, chunk),
				"rclone · " + rc.DefaultBucket
		} else {
			fmt.Fprintf(os.Stderr, "warmbox: volume remote unavailable (%v); using local storage\n", err)
		}
	}

	local := filepath.Join(cfg.WorkDir, "volume-remote")
	if err := os.MkdirAll(local, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "warmbox: volumes disabled: %v\n", err)
		return nil, "disabled"
	}
	f, err := rfs.NewFs(ctx, local)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warmbox: volumes disabled: %v\n", err)
		return nil, "disabled"
	}
	fmt.Fprintf(os.Stderr, "warmbox: volumes backed by local dir %s (no cloud remote configured; run `warmbox cloud set`)\n", local)
	return volume.NewStore(f, cfg.VolumePrefix, cfg.VolumeDir, cfg.VolumeBase, chunk), "local"
}

// openCatalog opens the SQLite catalog at <workdir>/warmbox.db.
func openCatalog(cfg *desktop.Config) *catalog.DB {
	cat, err := catalog.Open(filepath.Join(cfg.WorkDir, "warmbox.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "warmbox: catalog disabled: %v\n", err)
		return nil
	}
	return cat
}

// backfillCatalog mirrors volumes that already exist in remote storage into the
// local catalog, so volumes created before the catalog existed still show up.
func backfillCatalog(ctx context.Context, cat *catalog.DB, store *volume.Store, cfg *desktop.Config) {
	if cat == nil {
		return
	}
	vols, err := store.List(ctx)
	if err != nil {
		return
	}
	for _, m := range vols {
		if _, err := cat.GetVolume(m.Name); err == nil {
			continue
		}
		_ = cat.UpsertVolume(&catalog.Volume{
			Name:      m.Name,
			Size:      m.Size,
			ChunkSize: m.ChunkSize,
			Remote:    cfg.VolumePrefix + "/" + m.Name,
			From:      m.From,
			CreatedAt: m.Created,
		})
	}
}

// requireSafeBind refuses to expose a plaintext daemon to the network.
//
// The dashboard, its session cookie, the console websocket and the guest agent
// API all travel in the clear, and each is a way into a machine. Loopback is the
// default and the only safe answer without TLS, so anything else has to be
// paired with a certificate or with an explicit --insecure — a decision someone
// makes on purpose, rather than one that happens because a colon got typed in
// front of the port.
func requireSafeBind(cfg *desktop.Config) {
	if bindIsLoopback(cfg.APIAddr) {
		return
	}
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		return
	}
	if cfg.Insecure {
		fmt.Fprintf(os.Stderr, "warmbox: warning: %s is reachable from the network without TLS\n", cfg.APIAddr)
		fmt.Fprintln(os.Stderr, "warmbox:   the dashboard, its session cookie and every console are plaintext")
		return
	}
	fatal("refusing to listen on %s without TLS.\n"+
		"  Everything the daemon serves is plaintext: the dashboard, its session cookie,\n"+
		"  the console stream and the guest API. Pick one:\n"+
		"    bind 127.0.0.1 and tunnel to it    ssh -L 7070:127.0.0.1:7070 <user>@<host>\n"+
		"    give it a certificate             --tls-cert cert.pem --tls-key key.pem\n"+
		"    accept it knowingly               --insecure", cfg.APIAddr)
}

// bindIsLoopback reports whether an address only accepts connections from this
// machine. An empty host (":7070") means every interface, which is the opposite
// of loopback and the case most likely to be typed by accident.
func bindIsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// hostRAMMiB is the machine's total memory, or 0 when it cannot be read.
// It is used only to warn: a guest sized larger than its host still boots, it
// just makes everything else slow, and nothing else in the daemon would notice.
func hostRAMMiB() int {
	switch runtime.GOOS {
	case "linux":
		b, err := os.ReadFile("/proc/meminfo")
		if err != nil {
			return 0
		}
		for _, line := range strings.Split(string(b), "\n") {
			if !strings.HasPrefix(line, "MemTotal:") {
				continue
			}
			f := strings.Fields(line)
			if len(f) < 2 {
				return 0
			}
			kb, err := strconv.Atoi(f[1])
			if err != nil {
				return 0
			}
			return kb / 1024
		}
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
		if err != nil {
			return 0
		}
		b, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		if err != nil {
			return 0
		}
		return int(b / (1 << 20))
	}
	return 0
}

// missingImages names the images the daemon expects but cannot find.
func missingImages(cfg *desktop.Config, want ...string) []string {
	have := desktop.ImagesIn(cfg.ImageDir)
	var missing []string
	for _, name := range want {
		if !slices.Contains(have, name) {
			missing = append(missing, name)
		}
	}
	return missing
}

// --- making the host ready ---------------------------------------------------
//
// `setup` should leave a working machine, not a list of chores: when the
// hypervisor or the guest image is missing it installs or builds it, unless
// --no-install says otherwise.

func have(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// runCmd runs a command with its output attached, so the user can see what is
// being installed.
func runCmd(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	fmt.Fprintf(os.Stderr, "    $ %s %s\n", name, strings.Join(args, " "))
	return cmd.Run()
}

// installPackages installs host packages with whichever package manager this
// distro uses, as root (through sudo when needed).
func installPackages(names []string) bool {
	asRoot := func(bin string, args ...string) error {
		if os.Geteuid() == 0 {
			return runCmd("", os.Environ(), bin, args...)
		}
		if !have("sudo") {
			return fmt.Errorf("%s needs root and sudo is not installed", bin)
		}
		return runCmd("", os.Environ(), "sudo", append([]string{bin}, args...)...)
	}
	switch {
	case have("apt-get"):
		_ = asRoot("apt-get", "update", "-qq") // a fresh machine has no package index
		return asRoot("apt-get", append([]string{"install", "-y"}, names...)...) == nil
	case have("dnf"):
		return asRoot("dnf", append([]string{"install", "-y"}, names...)...) == nil
	case have("yum"):
		return asRoot("yum", append([]string{"install", "-y"}, names...)...) == nil
	case have("zypper"):
		return asRoot("zypper", append([]string{"install", "-y"}, names...)...) == nil
	case have("pacman"):
		return asRoot("pacman", append([]string{"-S", "--noconfirm"}, names...)...) == nil
	case have("apk"):
		return asRoot("apk", append([]string{"add"}, names...)...) == nil
	}
	return false
}

// ensureHypervisor makes sure the backend this host will actually use is
// installed.
func ensureHypervisor(cfg *desktop.Config, allowInstall bool) bool {
	if cfg.Backend != "qemu" {
		if have(cfg.VfkitPath) {
			fmt.Fprintln(os.Stderr, "✓ vfkit found")
			return true
		}
		if !allowInstall {
			fmt.Fprintln(os.Stderr, "✗ vfkit not found (--no-install)")
			return false
		}
		if !have("brew") {
			fmt.Fprintln(os.Stderr, "✗ vfkit not found, and Homebrew is not installed — see https://brew.sh")
			return false
		}
		fmt.Fprintln(os.Stderr, "… installing vfkit")
		if runCmd("", os.Environ(), "brew", "install", "vfkit") != nil || !have(cfg.VfkitPath) {
			fmt.Fprintln(os.Stderr, "✗ could not install vfkit")
			return false
		}
		fmt.Fprintln(os.Stderr, "✓ vfkit installed")
		return true
	}

	bin := desktop.QEMUSystemBinary()
	if !have(bin) {
		if !allowInstall {
			fmt.Fprintf(os.Stderr, "✗ %s not found (--no-install)\n", bin)
			return false
		}
		// Package names differ across distros, so try the usual suspects.
		candidates := [][]string{{"qemu-system-x86"}, {"qemu-kvm"}}
		if runtime.GOARCH == "arm64" {
			candidates = [][]string{{"qemu-system-arm"}, {"qemu-system-aarch64"}}
		}
		fmt.Fprintf(os.Stderr, "… installing %s\n", bin)
		for _, c := range candidates {
			if installPackages(c) && have(bin) {
				break
			}
		}
		if !have(bin) {
			fmt.Fprintf(os.Stderr, "✗ could not install %s — install it yourself, then re-run `warmbox setup`\n", bin)
			return false
		}
		fmt.Fprintf(os.Stderr, "✓ %s installed\n", bin)
	} else {
		fmt.Fprintf(os.Stderr, "✓ %s found\n", bin)
	}

	if err := kvmUsable(); err != nil {
		// Virtualisation is sometimes just not loaded yet; try the module once.
		_ = runCmd("", os.Environ(), "modprobe", "kvm")
		if err := kvmUsable(); err != nil {
			fmt.Fprintf(os.Stderr, "✗ /dev/kvm is not usable: %v\n", err)
			fmt.Fprintln(os.Stderr, "  enable virtualisation (BIOS/VM settings), or let this user open /dev/kvm (the kvm group)")
			return false
		}
	}
	fmt.Fprintln(os.Stderr, "✓ /dev/kvm")
	return true
}

// kvmUsable reports whether QEMU could actually use /dev/kvm.
//
// Existing is not the same as usable: QEMU opens this read-write, and a daemon
// running as a user outside the kvm group finds a file it can stat but not open.
// A stat-only check prints "✓ /dev/kvm" and leaves the VM to die at boot with
// "Could not access KVM kernel module: Permission denied".
func kvmUsable() error {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return f.Close()
}

// buildGuestImage builds one guest image on this machine — there is no artifact
// to download for this platform, so it fetches the source for this version and
// runs the same registry-driven build `warmbox image build` does. Needs Docker,
// and about fifteen minutes.
func buildGuestImage(cfg *desktop.Config, allowInstall bool, name string) error {
	if !have("docker") {
		if !allowInstall {
			return fmt.Errorf("Docker is needed to build the guest image (--no-install given)")
		}
		fmt.Fprintln(os.Stderr, "… Docker is required to build the image; installing it")
		if !installPackages([]string{"docker.io"}) && !installPackages([]string{"docker"}) {
			return fmt.Errorf("could not install Docker — install it, then re-run `warmbox setup`")
		}
		if !have("docker") {
			return fmt.Errorf("Docker is installed but not on PATH — start it and re-run `warmbox setup`")
		}
	}

	dir, err := os.MkdirTemp("", "warmbox-src-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	fmt.Fprintf(os.Stderr, "… fetching the source for %s\n", version)
	url := release.SourceURL(version)
	if err := fetchTarball(url, dir); err != nil {
		return fmt.Errorf("%v\n  (or build it by hand: ./deploy/guest/build.sh in a checkout)", err)
	}

	fmt.Fprintln(os.Stderr, "… building the guest image (about 15 minutes; the build prints its own progress)")
	c, err := imagecfg.LoadOne(imagecfg.Dir(dir), name)
	if err != nil {
		return fmt.Errorf("the source for %s has no image called %q: %v", version, name, err)
	}
	if err := buildImage(c, filepath.Join(dir, "deploy"), cfg.WorkDir, "linux/"+runtime.GOARCH, false); err != nil {
		return fmt.Errorf("the guest image build failed: %v", err)
	}
	return nil
}

func cmdSetup(args []string) {
	cfg := desktop.DefaultConfig()
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	fs.StringVar(&cfg.WorkDir, "workdir", cfg.WorkDir, "state directory")
	fs.StringVar(&cfg.ImageDir, "image-dir", cfg.ImageDir, "directory of guest images")
	fs.StringVar(&cfg.Image, "image", cfg.Image, "guest image to install and make the default (default: xfce)")
	fs.StringVar(&cfg.NoVNCDir, "novnc", cfg.NoVNCDir, "noVNC asset directory")
	fs.StringVar(&cfg.Backend, "backend", cfg.Backend, "hypervisor backend (vfkit, qemu)")
	imageURL := fs.String("image-url", "", "guest image archive (default: the published release for this image)")
	imageSHA := fs.String("image-sha256", "", "expected sha256 (default: fetched from <url>.sha256)")
	force := fs.Bool("force", false, "re-download the guest image even if it is already installed")
	noImage := fs.Bool("no-image", false, "skip the guest image; check everything else")
	noInstall := fs.Bool("no-install", false, "don't install missing prerequisites, just report them")
	_ = fs.Parse(args)
	allowInstall := !*noInstall
	image := desktop.DefaultFor(cfg.Image)

	if err := cfg.EnsureDirs(); err != nil {
		fatal("Error: %v", err)
	}

	fmt.Fprintln(os.Stderr, "warmbox setup")
	ready := ensureHypervisor(cfg, allowInstall)

	switch {
	case *noImage:
		fmt.Fprintln(os.Stderr, "… skipping the guest image (--no-image)")
	case !*force && len(missingImages(cfg, image)) == 0:
		fmt.Fprintf(os.Stderr, "✓ guest image %q in %s\n", image, filepath.Join(cfg.ImageDir, image))
	default:
		if err := installGuestImage(cfg, image, *imageURL, *imageSHA, *force, allowInstall); err != nil {
			fmt.Fprintf(os.Stderr, "✗ guest image: %v\n", err)
			ready = false
		}
	}

	if _, err := os.Stat(filepath.Join(cfg.NoVNCDir, "vnc.html")); err == nil {
		fmt.Fprintf(os.Stderr, "✓ noVNC at %s\n", cfg.NoVNCDir)
	} else {
		fmt.Fprintf(os.Stderr, "… fetching noVNC to %s\n", cfg.NoVNCDir)
		if err := fetchNoVNC(cfg.NoVNCDir); err != nil {
			fmt.Fprintf(os.Stderr, "✗ noVNC download failed: %v\n", err)
			ready = false
		} else {
			fmt.Fprintf(os.Stderr, "✓ noVNC installed\n")
		}
	}

	if !ready {
		// Say so plainly: the daemon cannot start, and telling someone to
		// install the service here would just leave them with a unit that
		// crash-loops.
		fmt.Fprintln(os.Stderr, "\nnot ready: fix the ✗ lines above, then run `warmbox setup` again.")
		os.Exit(1)
	}

	fmt.Fprintln(os.Stderr, "\nready. start the daemon:")
	fmt.Fprintln(os.Stderr, "  warmbox service install --pool 1   # background service")
	fmt.Fprintln(os.Stderr, "  warmbox create                     # a desktop; prints its URL")
	fmt.Fprintf(os.Stderr, "\nthe dashboard is on %s\n", dashboardURL(cfg.APIAddr))
}

// dashboardURL says where to point a browser. The daemon binds loopback by
// default, so on a remote host the useful instruction is a tunnel rather than
// the machine's public address — and it is plaintext HTTP, so exposing it is a
// poor idea.
func dashboardURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		port = "7070"
	}
	switch host {
	case "", "0.0.0.0", "::":
		return fmt.Sprintf("http://<this-host>:%s\n  (bound to every interface: reachable from the network, and plaintext — put a TLS proxy in front)", port)
	case "127.0.0.1", "::1", "localhost":
		return fmt.Sprintf("http://localhost:%s\n  from another machine, tunnel it:  ssh -L %s:127.0.0.1:%s <user>@<host>", port, port, port)
	}
	return "http://" + net.JoinHostPort(host, port)
}

// installGuestImage gets one guest image onto this machine: it downloads the
// published archive when there is one, and builds it from source when there is
// not. The archive is verified against the published checksum before anything
// lands under ImageDir.
func installGuestImage(cfg *desktop.Config, name, url, sha string, force, allowInstall bool) error {
	explicit := url != ""
	if !explicit {
		url = release.ImageURL(name, runtime.GOARCH)
	}
	if force {
		_ = os.Remove(filepath.Join(cfg.ImageDir, "."+name+".download.tar.zst"))
	}
	dir := filepath.Join(cfg.ImageDir, name)

	if sha == "" {
		s, err := desktop.FetchChecksum(url + ".sha256")
		if err != nil {
			if explicit {
				return fmt.Errorf("could not read %s.sha256: %v", url, err)
			}
			// Nothing published for this platform, so make one here rather than
			// leaving the user to do it.
			fmt.Fprintf(os.Stderr, "… no published image %q for %s/%s\n", name, runtime.GOOS, runtime.GOARCH)
			if err := buildGuestImage(cfg, allowInstall, name); err != nil {
				return err
			}
			if missing := missingImages(cfg, name); len(missing) > 0 {
				return fmt.Errorf("the build finished but %s has no %q image", cfg.ImageDir, name)
			}
			fmt.Fprintf(os.Stderr, "✓ guest image %q built in %s\n", name, dir)
			// Not a published archive, so there is nothing to compare it to;
			// the dashboard says "built locally" instead of offering an update.
			_ = desktop.WriteImageSHA(dir, "local")
			return seedVolumeBase(cfg, name)
		}
		sha = s
	}

	fmt.Fprintf(os.Stderr, "… fetching the %s guest image for %s (several hundred MB, resumable)\n  %s\n", name, runtime.GOARCH, url)
	if err := desktop.FetchImage(cfg.ImageDir, name, url, sha, os.Stderr); err != nil {
		return fmt.Errorf("%v\n  (re-run to resume; pass --force to start clean)", err)
	}
	fmt.Fprintf(os.Stderr, "✓ guest image %q installed in %s\n", name, dir)
	return seedVolumeBase(cfg, name)
}

// seedVolumeBase puts an image's volume-base.img where volumes look for it. The
// base is a shared sparse ext4 seed that travels inside the image archive, so
// the first image installed with one provides it for the whole machine.
func seedVolumeBase(cfg *desktop.Config, name string) error {
	if cfg.VolumeBase == "" || stat(cfg.VolumeBase) {
		return nil
	}
	src := filepath.Join(cfg.ImageDir, name, "volume-base.img")
	if !stat(src) {
		return nil
	}
	if err := desktop.CopyFile(src, cfg.VolumeBase); err != nil {
		return fmt.Errorf("installing the volume base: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ volume base seeded from the %s image\n", name)
	return nil
}

const noVNCTarball = "https://github.com/novnc/noVNC/archive/refs/tags/v1.6.0.tar.gz"

// fetchNoVNC is a thin wrapper: noVNC ships as a GitHub source archive.
func fetchNoVNC(dest string) error { return fetchTarball(noVNCTarball, dest) }

// fetchTarball downloads a gzipped tar and unpacks it into dest, stripping the
// single leading directory that GitHub wraps source archives in.
func fetchTarball(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		// Strip the leading "<name>-<version>/" path component.
		name := hdr.Name
		if i := strings.IndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		} else {
			continue
		}
		if name == "" {
			continue
		}
		out := filepath.Join(dest, name)
		if hdr.Typeflag == tar.TypeDir {
			_ = os.MkdirAll(out, 0o755)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return err
		}
		f.Close()
	}
	return nil
}

func apiBase(addr string) string {
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/")
	}
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	return "http://" + addr
}

// withToken appends the daemon token as a query parameter when set.
func withToken(rawURL, token string) string {
	if token == "" {
		return rawURL
	}
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	return rawURL + sep + "token=" + url.QueryEscape(token)
}

// tokenDefault resolves a user API token: $WARMBOX_TOKEN, else the legacy
// token file (~/.warmbox/token) if one exists. Prefer `warmbox login`, which
// stores a session cookie instead.
func tokenDefault() string {
	if t := os.Getenv("WARMBOX_TOKEN"); t != "" {
		return t
	}
	if home, err := os.UserHomeDir(); err == nil {
		if b, err := os.ReadFile(filepath.Join(home, ".warmbox", "token")); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return ""
}

// sessionFile is where `warmbox login` keeps the session cookie (0600).
func sessionFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".warmbox", "session")
}

func sessionCookieValue() string {
	p := sessionFile()
	if p == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// doAuthed executes a request with the login session attached. An explicit
// --token still travels as ?token= (user API tokens work there); the session
// cookie takes precedence server-side. A 401 means login is missing or
// expired, never a transient error, so it explains itself.
func doAuthed(req *http.Request) (*http.Response, error) {
	if s := sessionCookieValue(); s != "" {
		req.AddCookie(&http.Cookie{Name: "warmbox_session", Value: s})
	}
	resp, err := http.DefaultClient.Do(req)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		fatal("not logged in — run `warmbox login` (or pass --token with a user API token)")
	}
	return resp, err
}

func getAuthed(rawURL string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	return doAuthed(req)
}

func postAuthed(rawURL, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, rawURL, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return doAuthed(req)
}

// readPassword prompts without echoing when a TTY is available.
func readPassword(prompt string) string {
	fmt.Fprintf(os.Stderr, "%s: ", prompt)
	if f, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0); err == nil {
		if pw, err := term.ReadPassword(int(f.Fd())); err == nil {
			f.Close()
			fmt.Fprintln(os.Stderr)
			return strings.TrimSpace(string(pw))
		}
		f.Close()
	}
	fmt.Fprintln(os.Stderr, "(warning: input will echo)")
	var line string
	_, _ = fmt.Scanln(&line)
	return strings.TrimSpace(line)
}

// cmdLogin authenticates as a user and stores the session cookie for later
// commands. `warmbox login --email a@b.c` prompts for the password;
// --password-stdin reads it (for scripts).
func cmdLogin(args []string) {
	addr := ":7070"
	email := ""
	passwordStdin := false
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	fs.StringVar(&addr, "addr", addr, "daemon API address")
	fs.StringVar(&email, "email", email, "account email")
	fs.BoolVar(&passwordStdin, "password-stdin", false, "read the password from stdin")
	_ = fs.Parse(args)
	if email == "" {
		fmt.Fprint(os.Stderr, "email: ")
		_, _ = fmt.Scanln(&email)
		email = strings.TrimSpace(email)
	}
	var password string
	if passwordStdin {
		b, _ := io.ReadAll(os.Stdin)
		password = strings.TrimSpace(string(b))
	} else {
		password = readPassword("password")
	}
	if email == "" || password == "" {
		fatal("email and password are required")
	}
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	resp, err := http.Post(apiBase(addr)+"/api/login", "application/json", strings.NewReader(string(body)))
	if err != nil {
		fatal("Error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		var msg struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &msg)
		if msg.Error == "" {
			msg.Error = resp.Status
		}
		fatal("login failed: %s", msg.Error)
	}
	var session string
	for _, c := range resp.Cookies() {
		if c.Name == "warmbox_session" {
			session = c.Value
		}
	}
	if session == "" {
		fatal("login failed: daemon returned no session")
	}
	p := sessionFile()
	if p == "" {
		fatal("cannot determine home directory")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		fatal("Error: %v", err)
	}
	if err := os.WriteFile(p, []byte(session+"\n"), 0o600); err != nil {
		fatal("Error: %v", err)
	}
	var me struct {
		User struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"user"`
		Role      string `json:"role"`
		Workspace struct {
			Name string `json:"name"`
		} `json:"workspace"`
	}
	req, _ := http.NewRequest(http.MethodGet, apiBase(addr)+"/api/me", nil)
	req.AddCookie(&http.Cookie{Name: "warmbox_session", Value: session})
	if res, err := doAuthed(req); err == nil {
		defer res.Body.Close()
		_ = json.NewDecoder(res.Body).Decode(&me)
	}
	fmt.Fprintf(os.Stderr, "logged in as %s (%s · %s)\n", me.User.Email, me.Role, me.Workspace.Name)
}

// cmdLogout ends the session and deletes the stored credential.
func cmdLogout(args []string) {
	addr := ":7070"
	fs := flag.NewFlagSet("logout", flag.ExitOnError)
	fs.StringVar(&addr, "addr", addr, "daemon API address")
	_ = fs.Parse(args)
	if s := sessionCookieValue(); s != "" {
		req, _ := http.NewRequest(http.MethodPost, apiBase(addr)+"/api/logout", nil)
		req.AddCookie(&http.Cookie{Name: "warmbox_session", Value: s})
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
		}
	}
	if p := sessionFile(); p != "" {
		_ = os.Remove(p)
	}
	fmt.Fprintln(os.Stderr, "logged out")
}

func cmdCreate(args []string) {
	addr := ":7070"
	token := tokenDefault()
	vol := ""
	image := ""
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	fs.StringVar(&addr, "addr", addr, "daemon API address")
	fs.StringVar(&token, "token", token, "user API token (or `warmbox login` first)")
	fs.StringVar(&vol, "volume", "", "attach a persistent volume by name")
	fs.StringVar(&image, "image", "", "boot from a named image (e.g. omarchy)")
	_ = fs.Parse(args)

	req := map[string]string{}
	if vol != "" {
		req["volume"] = vol
	}
	if image != "" {
		req["image"] = image
	}
	var body io.Reader
	if len(req) > 0 {
		b, _ := json.Marshal(req)
		body = strings.NewReader(string(b))
	}
	resp, err := postAuthed(withToken(apiBase(addr)+"/api/desktops", token), "application/json", body)
	if err != nil {
		fatal("Error: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		ID  string `json:"id"`
		VNC string `json:"vnc"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		fatal("Error decoding response: %v", err)
	}
	if out.ID == "" {
		fatal("daemon returned no desktop (status %s)", resp.Status)
	}
	// A headless image answers with no console URL: say so, and print the
	// desktop's API resource instead of an empty path.
	if out.VNC == "" {
		fmt.Fprintf(os.Stderr, "desktop %s ready (headless — no screen)\n", out.ID)
	} else {
		fmt.Fprintf(os.Stderr, "desktop %s ready\n", out.ID)
	}
	loc := out.VNC
	if loc == "" {
		loc = "/api/desktops/" + out.ID
	}
	fmt.Printf("%s%s\n", apiBase(addr), loc)
}

// cmdImage packs/pulls guest images and lists them offline.
func cmdImage(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: warmbox image <build|pack|pull|list>")
		os.Exit(1)
	}
	cfg := desktop.DefaultConfig()
	switch args[0] {
	case "build":
		cmdImageBuild(args[1:])
	case "list":
		names := desktop.ImagesIn(cfg.ImageDir)
		if len(names) == 0 {
			fmt.Fprintln(os.Stderr, "(no images)")
			return
		}
		for _, n := range names {
			fmt.Println(n)
		}
	case "pack":
		fs := flag.NewFlagSet("image pack", flag.ExitOnError)
		out := fs.String("o", "", "output path (default <images>/<name>.tar.zst)")
		rest := parseArgsAnywhere(fs, args[1:])
		if len(rest) < 1 {
			fatal("Usage: warmbox image pack <name> [-o out.tar.zst]")
		}
		label := rest[0]
		p, err := desktop.Pack(cfg.ImageDir, label, *out)
		if err != nil {
			fatal("Error: %v", err)
		}
		// Ship the checksum alongside, so `warmbox setup` can verify a download.
		sum, err := desktop.SHA256File(p)
		if err != nil {
			fatal("Error: %v", err)
		}
		side := p + ".sha256"
		if err := os.WriteFile(side, []byte(sum+"  "+filepath.Base(p)+"\n"), 0o644); err != nil {
			fatal("Error writing %s: %v", side, err)
		}
		fi, _ := os.Stat(p)
		fmt.Fprintf(os.Stderr, "packed %s -> %s (%.2f GiB)\n  sha256 %s\n  wrote %s\n",
			label, p, float64(fi.Size())/(1<<30), sum, side)
	case "pull":
		fs := flag.NewFlagSet("image pull", flag.ExitOnError)
		sha := fs.String("sha256", "", "expected sha256 (default: fetched from <url>.sha256)")
		rest := parseArgsAnywhere(fs, args[1:])
		if len(rest) < 1 || len(rest) > 2 {
			fatal("Usage: warmbox image pull <name> [file|url]\n" +
				"       warmbox image pull <name>   # the published image for this platform")
		}
		name := rest[0]
		src := release.ImageURL(name, runtime.GOARCH)
		if len(rest) == 2 {
			src = rest[1]
		}
		remote := strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://")
		if remote {
			// A download is checked against the published checksum before it is
			// expanded: an image is code that boots.
			if *sha == "" {
				s, err := desktop.FetchChecksum(src + ".sha256")
				if err != nil {
					if len(rest) == 2 {
						fatal("Error: could not read %s.sha256: %v\n  pass --sha256 to pull it anyway", src, err)
					}
					fatal("Error: no published image %q for %s/%s\n  build it here instead: warmbox image build %s",
						name, runtime.GOOS, runtime.GOARCH, name)
				}
				*sha = s
			}
			if err := desktop.FetchImage(cfg.ImageDir, name, src, *sha, os.Stderr); err != nil {
				fatal("Error: %v", err)
			}
		} else if err := desktop.Pull(cfg.ImageDir, name, src); err != nil {
			fatal("Error: %v", err)
		}
		fmt.Fprintf(os.Stderr, "pulled %s into %s\n", name, filepath.Join(cfg.ImageDir, name))
	default:
		fatal("unknown image command %q (build|pack|pull|list)", args[0])
	}
}

// parseArgsAnywhere parses flags that appear before, between or after the
// positional arguments. The flag package stops at the first positional, which
// would make `warmbox image build lxqt --dry-run` treat --dry-run as an image
// name. Returns the positionals, in order.
func parseArgsAnywhere(fs *flag.FlagSet, args []string) []string {
	var rest []string
	for {
		_ = fs.Parse(args)
		args = fs.Args()
		if len(args) == 0 {
			return rest
		}
		rest = append(rest, args[0])
		args = args[1:]
	}
}

// cmdImageBuild builds images from the registry in deploy/images.
//
// It is a checkout operation, like ./deploy/guest/build.sh itself: a released
// binary ships without an engine. What it adds is that the *definition* of an
// image lives in one place instead of in someone's shell history, and that the
// image's meta.json is generated from it rather than hand-written per engine.
func cmdImageBuild(args []string) {
	fs := flag.NewFlagSet("image build", flag.ExitOnError)
	deployDir := fs.String("deploy", "", "the checkout's deploy/ directory (default: found)")
	buildAll := fs.Bool("all", false, "build every image the checkout can build")
	publishable := fs.Bool("publishable", false, "print the names of the images marked publish: true, one per line")
	dryRun := fs.Bool("dry-run", false, "show what would run, building nothing")
	platform := fs.String("platform", "", "docker build platform, e.g. linux/arm64 (default: the script's)")
	rest := parseArgsAnywhere(fs, args)

	dir, err := findDeployDir(*deployDir)
	if err != nil {
		fatal("Error: %v", err)
	}
	reg := filepath.Join(dir, "images")
	cfgs, err := imagecfg.Load(reg)
	if err != nil {
		fatal("Error: %v", err)
	}

	workdir := desktop.DefaultConfig().WorkDir

	// Scripting hook: the release workflow asks the registry which images ship
	// rather than hard-coding a list that drifts from deploy/images/.
	if *publishable {
		for _, c := range cfgs {
			if c.Publish {
				fmt.Println(c.Name)
			}
		}
		return
	}

	// No target names the registry itself, which is the point of having one.
	if !*buildAll && len(rest) == 0 {
		printRegistry(cfgs, workdir)
		return
	}

	var targets []imagecfg.Config
	if *buildAll {
		for _, c := range cfgs {
			if c.RequiresInputs() {
				fmt.Fprintf(os.Stderr, "… skipping %s: the %s engine needs inputs from this host (%s)\n",
					c.Name, c.Engine, c.Script(dir))
				continue
			}
			targets = append(targets, c)
		}
	} else {
		for _, name := range rest {
			c, err := imagecfg.LoadOne(reg, name)
			if err != nil {
				fatal("Error: %v", err)
			}
			targets = append(targets, c)
		}
	}
	if len(targets) == 0 {
		fatal("nothing to build")
	}

	for _, c := range targets {
		if err := buildImage(c, dir, workdir, *platform, *dryRun); err != nil {
			fatal("Error: %v", err)
		}
	}
}

// buildImage renders one config into the environment its engine script already
// understands and runs it. The script stays the build system; the config is how
// the build is described.
func buildImage(c imagecfg.Config, deployDir, workdir, platform string, dryRun bool) error {
	script := c.Script(deployDir)
	if _, err := os.Stat(script); err != nil {
		return err
	}

	rendered := c.Env(platform)
	keys := make([]string, 0, len(rendered))
	for k := range rendered {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	env := os.Environ()
	for _, k := range keys {
		env = append(env, k+"="+rendered[k])
	}

	if dryRun {
		fmt.Fprintf(os.Stderr, "%s\n", script)
		for _, k := range keys {
			fmt.Fprintf(os.Stderr, "  %s=%s\n", k, rendered[k])
		}
		return nil
	}

	fmt.Fprintf(os.Stderr, "==> building %s (engine %s)\n", c.Name, c.Engine)
	cmd := exec.Command(script)
	cmd.Dir = filepath.Dir(deployDir)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building %s: %w", c.Name, err)
	}

	// The run-time contract is meta.json. If the engine did not honour META_JSON
	// the image would boot from a config nobody can see, so refuse it here
	// rather than discovering it on a desktop that refuses to open.
	out := c.OutDir(workdir)
	if _, err := os.Stat(filepath.Join(out, "meta.json")); err != nil {
		return fmt.Errorf("built %s but %s has no meta.json", c.Name, out)
	}
	fmt.Fprintf(os.Stderr, "✓ %s → %s\n  boot it with: warmbox create --image %s\n", c.Name, out, c.Name)
	return nil
}

// printRegistry lists what can be built and what is already installed.
func printRegistry(cfgs []imagecfg.Config, workdir string) {
	fmt.Fprintf(os.Stderr, "images that can be built (deploy/images)\n\n")
	fmt.Fprintf(os.Stderr, "  %-16s %-9s %-9s %-10s %s\n", "NAME", "ENGINE", "HEADLESS", "INSTALLED", "PUBLISHED")
	for _, c := range cfgs {
		headless, installed, published := "no", "no", "no"
		if c.Headless {
			headless = "yes"
		}
		if imageInstalled(c.OutDir(workdir)) {
			installed = "yes"
		}
		if c.Publish {
			published = "yes"
		}
		fmt.Fprintf(os.Stderr, "  %-16s %-9s %-9s %-10s %s\n", c.Name, c.Engine, headless, installed, published)
	}
	fmt.Fprintf(os.Stderr, "\nbuild one with: warmbox image build <name>\n")
}

// imageInstalled reports whether an image directory holds bootable artifacts —
// either kind of image, whether or not it has a meta.json.
func imageInstalled(dir string) bool {
	for _, f := range []string{"vmlinux", "disk.raw"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}

// findDeployDir locates a checkout's deploy/ directory: an explicit flag, then
// $WARMBOX_DEPLOY, then the working directory and its parents, then wherever
// this binary lives (so ./warmbox at a repo root works).
func findDeployDir(explicit string) (string, error) {
	var candidates []string
	if explicit != "" {
		candidates = append(candidates, explicit)
	}
	if env := os.Getenv("WARMBOX_DEPLOY"); env != "" {
		candidates = append(candidates, env)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, ancestors(wd)...)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, ancestors(filepath.Dir(exe))...)
	}
	for _, c := range candidates {
		// Accept either the deploy directory or the repo root containing it.
		for _, d := range []string{c, filepath.Join(c, "deploy")} {
			if isDeployDir(d) {
				return d, nil
			}
		}
	}
	return "", fmt.Errorf("no deploy/ directory found — run this from a checkout, or pass --deploy (or set WARMBOX_DEPLOY)")
}

// ancestors is dir and each of its parents, nearest first.
func ancestors(dir string) []string {
	var out []string
	for {
		out = append(out, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			return out
		}
		dir = parent
	}
}

func isDeployDir(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "images"))
	return err == nil && st.IsDir()
}

func cmdImages(args []string) {
	addr := ":7070"
	token := tokenDefault()
	fs := flag.NewFlagSet("images", flag.ExitOnError)
	fs.StringVar(&addr, "addr", addr, "daemon API address")
	fs.StringVar(&token, "token", token, "user API token (or `warmbox login` first)")
	_ = fs.Parse(args)

	resp, err := getAuthed(withToken(apiBase(addr)+"/api/images", token))
	if err != nil {
		fatal("Error: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Images []string `json:"images"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		fatal("Error decoding response: %v", err)
	}
	if len(out.Images) == 0 {
		fmt.Fprintln(os.Stderr, "(no named images)")
		return
	}
	for _, n := range out.Images {
		fmt.Println(n)
	}
}

func cmdList(args []string) {
	addr := ":7070"
	token := tokenDefault()
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	fs.StringVar(&addr, "addr", addr, "daemon API address")
	fs.StringVar(&token, "token", token, "user API token (or `warmbox login` first)")
	_ = fs.Parse(args)

	resp, err := getAuthed(withToken(apiBase(addr)+"/api/desktops", token))
	if err != nil {
		fatal("Error: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Desktops []struct {
			ID      string `json:"id"`
			State   string `json:"state"`
			GuestIP string `json:"guest_ip"`
		} `json:"desktops"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		fatal("Error decoding response: %v", err)
	}
	if len(out.Desktops) == 0 {
		fmt.Fprintln(os.Stderr, "(no desktops)")
		return
	}
	for _, d := range out.Desktops {
		fmt.Printf("%s  %-8s  %s\n", d.ID, d.State, d.GuestIP)
	}
}

func cmdDestroy(args []string) {
	addr := ":7070"
	token := tokenDefault()
	fs := flag.NewFlagSet("destroy", flag.ExitOnError)
	fs.StringVar(&addr, "addr", addr, "daemon API address")
	fs.StringVar(&token, "token", token, "user API token (or `warmbox login` first)")
	rest := parseInterspersed(fs, args)
	if len(rest) < 1 {
		fatal("Usage: warmbox destroy <id>")
	}
	req, _ := http.NewRequest(http.MethodDelete, withToken(apiBase(addr)+"/api/desktops/"+rest[0], token), nil)
	resp, err := doAuthed(req)
	if err != nil {
		fatal("Error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fatal("destroy failed: %s", resp.Status)
	}
	fmt.Fprintf(os.Stderr, "destroyed %s\n", rest[0])
}

// --- memory checkpoints (live RAM snapshots; qemu backend) ---

func checkpointFlags(args []string, name string) (addr, token string, rest []string) {
	addr = ":7070"
	token = tokenDefault()
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.StringVar(&addr, "addr", addr, "daemon API address")
	fs.StringVar(&token, "token", token, "user API token (or `warmbox login` first)")
	rest = parseInterspersed(fs, args)
	return addr, token, rest
}

// apiErr reads the daemon's {"error": ...} body, falling back to the status.
func apiErr(resp *http.Response) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.NewDecoder(resp.Body).Decode(&e) == nil && e.Error != "" {
		return e.Error
	}
	return resp.Status
}

func cmdCheckpoint(args []string) {
	addr, token, rest := checkpointFlags(args, "checkpoint")
	if len(rest) < 1 || len(rest) > 2 {
		fatal("Usage: warmbox checkpoint <id> [name]")
	}
	req := map[string]string{}
	if len(rest) > 1 {
		req["name"] = rest[1]
	}
	var body io.Reader
	if len(req) > 0 {
		b, _ := json.Marshal(req)
		body = strings.NewReader(string(b))
	}
	resp, err := postAuthed(withToken(apiBase(addr)+"/api/desktops/"+rest[0]+"/checkpoint", token), "application/json", body)
	if err != nil {
		fatal("Error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		fatal("checkpoint failed: %s", apiErr(resp))
	}
	var out struct {
		Name         string `json:"name"`
		Size         int64  `json:"size"`
		CheckpointMs int64  `json:"checkpoint_ms"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		fatal("Error decoding response: %v", err)
	}
	fmt.Fprintf(os.Stderr, "checkpointed %s as %q (%d bytes in %dms, still running)\n",
		rest[0], out.Name, out.Size, out.CheckpointMs)
}

func cmdCheckpoints(args []string) {
	addr, token, rest := checkpointFlags(args, "checkpoints")
	if len(rest) < 1 {
		fatal("Usage: warmbox checkpoints <id> | warmbox checkpoints rm <id> <name>")
	}
	if rest[0] == "rm" {
		if len(rest) != 3 {
			fatal("Usage: warmbox checkpoints rm <id> <name>")
		}
		req, _ := http.NewRequest(http.MethodDelete, withToken(apiBase(addr)+"/api/desktops/"+rest[1]+"/checkpoints/"+rest[2], token), nil)
		resp, err := doAuthed(req)
		if err != nil {
			fatal("Error: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			fatal("delete failed: %s", apiErr(resp))
		}
		fmt.Fprintf(os.Stderr, "deleted checkpoint %q of %s\n", rest[2], rest[1])
		return
	}
	if len(rest) != 1 {
		fatal("Usage: warmbox checkpoints <id> | warmbox checkpoints rm <id> <name>")
	}
	resp, err := getAuthed(withToken(apiBase(addr)+"/api/desktops/"+rest[0]+"/checkpoints", token))
	if err != nil {
		fatal("Error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fatal("list failed: %s", apiErr(resp))
	}
	var out struct {
		Checkpoints []struct {
			Name    string `json:"name"`
			Size    int64  `json:"size"`
			Created string `json:"created"`
			Image   string `json:"image"`
		} `json:"checkpoints"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		fatal("Error decoding response: %v", err)
	}
	if len(out.Checkpoints) == 0 {
		fmt.Fprintf(os.Stderr, "%s has no checkpoints\n", rest[0])
		return
	}
	for _, c := range out.Checkpoints {
		fmt.Fprintf(os.Stdout, "%s  %d bytes  %s  %s\n", c.Name, c.Size, c.Created, c.Image)
	}
}

func cmdRestore(args []string) {
	addr, token, rest := checkpointFlags(args, "restore")
	if len(rest) < 1 || len(rest) > 2 {
		fatal("Usage: warmbox restore <id> [name]  (no name: most recent checkpoint)")
	}
	req := map[string]string{}
	if len(rest) > 1 {
		req["name"] = rest[1]
	}
	var body io.Reader
	if len(req) > 0 {
		b, _ := json.Marshal(req)
		body = strings.NewReader(string(b))
	}
	resp, err := postAuthed(withToken(apiBase(addr)+"/api/desktops/"+rest[0]+"/restore", token), "application/json", body)
	if err != nil {
		fatal("Error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fatal("restore failed: %s", apiErr(resp))
	}
	var out struct {
		RestoreMs int64 `json:"restore_ms"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	fmt.Fprintf(os.Stderr, "restored %s in %dms (same desktop, guest uptime continues)\n", rest[0], out.RestoreMs)
}

func cmdHibernate(args []string) {
	addr, token, rest := checkpointFlags(args, "hibernate")
	if len(rest) != 1 {
		fatal("Usage: warmbox hibernate <id>")
	}
	resp, err := postAuthed(withToken(apiBase(addr)+"/api/desktops/"+rest[0]+"/hibernate", token), "", nil)
	if err != nil {
		fatal("Error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fatal("hibernate failed: %s", apiErr(resp))
	}
	fmt.Fprintf(os.Stderr, "hibernated %s (no RAM, no CPU — wake it with: warmbox wake %s)\n", rest[0], rest[0])
}

func cmdWake(args []string) {
	addr, token, rest := checkpointFlags(args, "wake")
	if len(rest) != 1 {
		fatal("Usage: warmbox wake <id>")
	}
	resp, err := postAuthed(withToken(apiBase(addr)+"/api/desktops/"+rest[0]+"/wake", token), "", nil)
	if err != nil {
		fatal("Error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fatal("wake failed: %s", apiErr(resp))
	}
	var out struct {
		RestoreMs int64 `json:"restore_ms"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	fmt.Fprintf(os.Stderr, "woke %s in %dms\n", rest[0], out.RestoreMs)
}

// --- volumes ---

func volumeUsage() {
	fmt.Fprint(os.Stderr, `warmbox volume — portable, cloud-backed disks

Usage:
  warmbox volume create <name> [--size 8G] [--from <name>]
  warmbox volume list
  warmbox volume clone <name> <new>
  warmbox volume rm <name>
`)
}

// parseInterspersed parses flags that may appear after positional arguments.
// The stdlib flag package stops at the first non-flag, so this re-parses the
// remainder and returns positional arguments in order.
func parseInterspersed(fs *flag.FlagSet, args []string) []string {
	var positional []string
	for {
		_ = fs.Parse(args)
		rest := fs.Args()
		if len(rest) == 0 {
			return positional
		}
		if len(rest[0]) > 1 && strings.HasPrefix(rest[0], "-") {
			args = rest
			continue
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// cmdCloud reads and writes the cloud settings (S3/R2) that back volumes.
func cmdCloud(args []string) {
	orDash := func(s string) string {
		if s == "" {
			return "(not set)"
		}
		return s
	}
	mask := func(s string) string {
		if s == "" {
			return "(not set)"
		}
		if len(s) <= 4 {
			return "****"
		}
		return s[:4] + strings.Repeat("*", 8)
	}

	if len(args) < 1 {
		cloudUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "set":
		fs := flag.NewFlagSet("cloud set", flag.ExitOnError)
		bucket := fs.String("bucket", "", "bucket name (required)")
		endpoint := fs.String("endpoint", "", "S3 endpoint URL")
		provider := fs.String("provider", "Cloudflare", "rclone s3 provider")
		region := fs.String("region", "", "region")
		access := fs.String("access-key", "", "access key id")
		secret := fs.String("secret-key", "", "secret access key")
		_ = fs.Parse(args[1:])
		if *bucket == "" {
			cloudUsage()
			os.Exit(1)
		}
		// Keep whatever is already stored (e.g. an encryption key) unless a
		// flag overrides it.
		cfg, _ := config.LoadGlobal()
		if cfg == nil {
			cfg = &config.RemoteConfig{}
		}
		cfg.Provider = *provider
		cfg.Endpoint = *endpoint
		cfg.Region = *region
		cfg.AccessKey = *access
		cfg.SecretKey = *secret
		cfg.DefaultBucket = *bucket
		if err := config.SaveGlobal(cfg); err != nil {
			fatal("Error: %v", err)
		}
		p, _ := config.GlobalPath()
		fmt.Fprintf(os.Stderr, "warmbox: cloud settings saved to %s\n", p)
		fmt.Fprintln(os.Stderr, "  restart the daemon to pick them up:  warmbox service restart")

	case "show":
		fs := flag.NewFlagSet("cloud show", flag.ExitOnError)
		_ = fs.Parse(args[1:])
		cfg, err := config.LoadGlobal()
		if err != nil {
			fatal("Error: %v", err)
		}
		p, _ := config.GlobalPath()
		if !cfg.Configured() {
			fmt.Fprintf(os.Stderr, "no cloud remote configured (%s)\n", p)
			fmt.Fprintln(os.Stderr, "  volumes are stored locally; set one with:  warmbox cloud set --bucket <name>")
			return
		}
		fmt.Printf("config:   %s\n", p)
		fmt.Printf("provider: %s\n", orDash(cfg.Provider))
		fmt.Printf("bucket:   %s\n", cfg.DefaultBucket)
		fmt.Printf("endpoint: %s\n", orDash(cfg.Endpoint))
		fmt.Printf("region:   %s\n", orDash(cfg.Region))
		fmt.Printf("key:      %s\n", mask(cfg.AccessKey))
		fmt.Printf("secret:   %s\n", mask(cfg.SecretKey))

	case "clear":
		fs := flag.NewFlagSet("cloud clear", flag.ExitOnError)
		_ = fs.Parse(args[1:])
		if err := config.Clear(); err != nil {
			fatal("Error: %v", err)
		}
		fmt.Fprintln(os.Stderr, "warmbox: cloud settings cleared; volumes fall back to local storage")
		fmt.Fprintln(os.Stderr, "  restart the daemon:  warmbox service restart")

	default:
		cloudUsage()
		os.Exit(1)
	}
}

func cloudUsage() {
	fmt.Fprint(os.Stderr, `Manage the cloud remote that stores volume bytes (S3 or Cloudflare R2).

Usage:
  warmbox cloud set --bucket <name> [--endpoint <url>] [--access-key <k>] [--secret-key <s>]
  warmbox cloud show      Show where volume bytes are stored
  warmbox cloud clear     Forget the settings and fall back to local storage

Example (Cloudflare R2):
  warmbox cloud set --endpoint https://<account>.r2.cloudflarestorage.com \
      --access-key <id> --secret-key <secret> --bucket warmbox
`)
}

func cmdVolume(args []string) {
	if len(args) < 1 {
		volumeUsage()
		os.Exit(1)
	}
	addr := ":7070"
	token := tokenDefault()

	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("volume create", flag.ExitOnError)
		fs.StringVar(&addr, "addr", addr, "daemon API address")
		fs.StringVar(&token, "token", token, "user API token (or `warmbox login` first)")
		size := fs.String("size", "", "volume size, e.g. 8G (default: base image size)")
		from := fs.String("from", "", "clone from an existing volume")
		fromSnap := fs.String("from-snapshot", "", "create from a snapshot id")
		rest := parseInterspersed(fs, args[1:])
		if len(rest) < 1 {
			fatal("Usage: warmbox volume create <name> [--size 8G] [--from <name>] [--from-snapshot <id>]")
		}
		payload, _ := json.Marshal(map[string]string{"name": rest[0], "size": *size, "from": *from, "from_snapshot": *fromSnap})
		resp, err := postAuthed(withToken(apiBase(addr)+"/api/volumes", token),
			"application/json", strings.NewReader(string(payload)))
		doVolume(resp, err)

	case "list":
		fs := flag.NewFlagSet("volume list", flag.ExitOnError)
		fs.StringVar(&addr, "addr", addr, "daemon API address")
		fs.StringVar(&token, "token", token, "user API token (or `warmbox login` first)")
		_ = fs.Parse(args[1:])
		resp, err := getAuthed(withToken(apiBase(addr)+"/api/volumes", token))
		if err != nil {
			fatal("Error: %v", err)
		}
		defer resp.Body.Close()
		var out struct {
			Volumes []struct {
				Name string `json:"name"`
				Size int64  `json:"size"`
				From string `json:"from"`
			} `json:"volumes"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			fatal("Error decoding response: %v", err)
		}
		if len(out.Volumes) == 0 {
			fmt.Fprintln(os.Stderr, "(no volumes)")
			return
		}
		for _, v := range out.Volumes {
			fmt.Printf("%-20s %6s  %s\n", v.Name, humanSize(v.Size), v.From)
		}

	case "clone":
		fs := flag.NewFlagSet("volume clone", flag.ExitOnError)
		fs.StringVar(&addr, "addr", addr, "daemon API address")
		fs.StringVar(&token, "token", token, "user API token (or `warmbox login` first)")
		rest := parseInterspersed(fs, args[1:])
		if len(rest) < 2 {
			fatal("Usage: warmbox volume clone <name> <new>")
		}
		payload, _ := json.Marshal(map[string]string{"name": rest[1]})
		resp, err := postAuthed(withToken(apiBase(addr)+"/api/volumes/"+rest[0]+"/clone", token),
			"application/json", strings.NewReader(string(payload)))
		doVolume(resp, err)

	case "rm":
		fs := flag.NewFlagSet("volume rm", flag.ExitOnError)
		fs.StringVar(&addr, "addr", addr, "daemon API address")
		fs.StringVar(&token, "token", token, "user API token (or `warmbox login` first)")
		rest := parseInterspersed(fs, args[1:])
		if len(rest) < 1 {
			fatal("Usage: warmbox volume rm <name>")
		}
		req, _ := http.NewRequest(http.MethodDelete, withToken(apiBase(addr)+"/api/volumes/"+rest[0], token), nil)
		resp, err := doAuthed(req)
		doVolume(resp, err)

	default:
		volumeUsage()
		os.Exit(1)
	}
}

func doVolume(resp *http.Response, err error) {
	if err != nil {
		fatal("Error: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fatal("volume request failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	fmt.Fprintf(os.Stderr, "%s\n", resp.Status)
	if s := strings.TrimSpace(string(body)); s != "" {
		fmt.Println(s)
	}
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<40:
		return fmt.Sprintf("%dT", n>>40)
	case n >= 1<<30:
		return fmt.Sprintf("%dG", n>>30)
	case n >= 1<<20:
		return fmt.Sprintf("%dM", n>>20)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func snapshotUsage() {
	fmt.Fprint(os.Stderr, `warmbox snapshot — frozen volume manifests

Usage:
  warmbox snapshot create <volume> [--name <id>]
  warmbox snapshot list
  warmbox snapshot rm <id>
`)
}

func cmdSnapshot(args []string) {
	if len(args) < 1 {
		snapshotUsage()
		os.Exit(1)
	}
	addr := ":7070"
	token := tokenDefault()

	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("snapshot create", flag.ExitOnError)
		fs.StringVar(&addr, "addr", addr, "daemon API address")
		fs.StringVar(&token, "token", token, "user API token (or `warmbox login` first)")
		name := fs.String("name", "", "snapshot id (default: random)")
		rest := parseInterspersed(fs, args[1:])
		if len(rest) < 1 {
			fatal("Usage: warmbox snapshot create <volume> [--name <id>]")
		}
		payload, _ := json.Marshal(map[string]string{"volume": rest[0], "name": *name})
		resp, err := postAuthed(withToken(apiBase(addr)+"/api/snapshots", token),
			"application/json", strings.NewReader(string(payload)))
		doVolume(resp, err)

	case "list":
		fs := flag.NewFlagSet("snapshot list", flag.ExitOnError)
		fs.StringVar(&addr, "addr", addr, "daemon API address")
		fs.StringVar(&token, "token", token, "user API token (or `warmbox login` first)")
		_ = fs.Parse(args[1:])
		resp, err := getAuthed(withToken(apiBase(addr)+"/api/snapshots", token))
		if err != nil {
			fatal("Error: %v", err)
		}
		defer resp.Body.Close()
		var out struct {
			Snapshots []struct {
				ID      string `json:"id"`
				Volume  string `json:"volume"`
				Size    int64  `json:"size"`
				Created string `json:"created_at"`
			} `json:"snapshots"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			fatal("Error decoding response: %v", err)
		}
		if len(out.Snapshots) == 0 {
			fmt.Fprintln(os.Stderr, "(no snapshots)")
			return
		}
		for _, s := range out.Snapshots {
			fmt.Printf("%-14s %-16s %6s  %s\n", s.ID, s.Volume, humanSize(s.Size), s.Created)
		}

	case "rm":
		fs := flag.NewFlagSet("snapshot rm", flag.ExitOnError)
		fs.StringVar(&addr, "addr", addr, "daemon API address")
		fs.StringVar(&token, "token", token, "user API token (or `warmbox login` first)")
		rest := parseInterspersed(fs, args[1:])
		if len(rest) < 1 {
			fatal("Usage: warmbox snapshot rm <id>")
		}
		req, _ := http.NewRequest(http.MethodDelete, withToken(apiBase(addr)+"/api/snapshots/"+rest[0], token), nil)
		resp, err := doAuthed(req)
		doVolume(resp, err)

	default:
		snapshotUsage()
		os.Exit(1)
	}
}
