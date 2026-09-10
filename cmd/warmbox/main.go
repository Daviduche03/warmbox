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
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"runmesh/workspace/internal/api"
	"runmesh/workspace/internal/desktop"
	"runmesh/workspace/internal/pool"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "daemon":
		cmdDaemon(os.Args[2:])
	case "setup":
		cmdSetup(os.Args[2:])
	case "create":
		cmdCreate(os.Args[2:])
	case "list":
		cmdList(os.Args[2:])
	case "destroy":
		cmdDestroy(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `warmbox — self-hosted GUI desktop orchestrator (vfkit + noVNC)

Usage:
  warmbox daemon            Run the orchestrator (warm pool + REST API)
  warmbox setup             Check host prerequisites, fetch noVNC
  warmbox create            Provision a desktop, print its noVNC URL
  warmbox list              List desktops
  warmbox destroy <id>      Destroy a desktop

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
	fs.StringVar(&cfg.KernelPath, "kernel", cfg.KernelPath, "path to uncompressed vmlinux")
	fs.StringVar(&cfg.InitrdPath, "initrd", cfg.InitrdPath, "path to all-RAM initramfs.zst")
	fs.StringVar(&cfg.DiskPath, "disk", cfg.DiskPath, "base ext4 rootfs image for low-RAM disk boot (empty = all-RAM)")
	fs.StringVar(&cfg.BootInitrdPath, "boot-initrd", cfg.BootInitrdPath, "Alpine boot initramfs for disk boot")
	fs.StringVar(&cfg.SquashPath, "squash", cfg.SquashPath, "shared read-only squashfs base for overlay boot")
	fs.StringVar(&cfg.OverlayInitrdPath, "overlay-initrd", cfg.OverlayInitrdPath, "boot initramfs for overlay boot")
	fs.StringVar(&cfg.NoVNCDir, "novnc", cfg.NoVNCDir, "noVNC asset directory")
	fs.StringVar(&cfg.VfkitPath, "vfkit", cfg.VfkitPath, "vfkit binary")
	fs.StringVar(&cfg.APIAddr, "addr", cfg.APIAddr, "HTTP listen address")
	fs.StringVar(&cfg.HostAddr, "host", cfg.HostAddr, "address guests use to reach this host")
	fs.UintVar(&cfg.MemMiB, "mem", cfg.MemMiB, "memory per VM (MiB)")
	fs.UintVar(&cfg.CPUs, "cpus", cfg.CPUs, "vCPUs per VM")
	fs.IntVar(&cfg.PoolSize, "pool", cfg.PoolSize, "warm pool size")
	fs.StringVar(&cfg.ShareDir, "share", cfg.ShareDir, "host directory shared with guests via virtiofs (mounted at /workspace)")
	fs.StringVar(&cfg.ShareTag, "share-tag", cfg.ShareTag, "virtiofs mount tag")
	fs.StringVar(&cfg.Token, "token", cfg.Token, "require this token on API/UI routes (empty = no auth)")
}

func cmdDaemon(args []string) {
	cfg := desktop.DefaultConfig()
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	addCommonFlags(fs, cfg)
	_ = fs.Parse(args)

	if err := cfg.EnsureDirs(); err != nil {
		fatal("Error: %v", err)
	}
	if _, err := os.Stat(cfg.KernelPath); err != nil {
		fatal("missing guest kernel %s — build it with ./deploy/guest/build.sh", cfg.KernelPath)
	}
	// Boot needs an overlay base, an ext4 disk, or the all-RAM initramfs.
	haveOverlay := stat(cfg.SquashPath) && stat(cfg.OverlayInitrdPath)
	haveDisk := stat(cfg.DiskPath) && stat(cfg.BootInitrdPath)
	if !haveOverlay && !haveDisk && !stat(cfg.InitrdPath) {
		fatal("missing guest rootfs — run ./deploy/guest/build.sh (need %s + %s, %s + %s, or %s)",
			cfg.SquashPath, cfg.OverlayInitrdPath, cfg.DiskPath, cfg.BootInitrdPath, cfg.InitrdPath)
	}

	mgr := desktop.NewManager(cfg, os.Stderr)
	p := pool.New(mgr, cfg.PoolSize, os.Stderr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go p.Run(ctx)

	srv := &http.Server{Addr: cfg.APIAddr, Handler: api.New(mgr, p, cfg, os.Stderr).Handler()}
	go func() {
		fmt.Fprintf(os.Stderr, "warmbox: listening on %s (pool=%d)\n", cfg.APIAddr, cfg.PoolSize)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "warmbox: server error: %v\n", err)
			stop()
		}
	}()

	<-ctx.Done()
	fmt.Fprintln(os.Stderr, "warmbox: shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	mgr.Shutdown()
}

func cmdSetup(args []string) {
	cfg := desktop.DefaultConfig()
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	fs.StringVar(&cfg.WorkDir, "workdir", cfg.WorkDir, "state directory")
	fs.StringVar(&cfg.KernelPath, "kernel", cfg.KernelPath, "path to uncompressed vmlinux")
	fs.StringVar(&cfg.InitrdPath, "initrd", cfg.InitrdPath, "path to initramfs.zst")
	fs.StringVar(&cfg.NoVNCDir, "novnc", cfg.NoVNCDir, "noVNC asset directory")
	_ = fs.Parse(args)

	if err := cfg.EnsureDirs(); err != nil {
		fatal("Error: %v", err)
	}

	if _, err := exec.LookPath(cfg.VfkitPath); err != nil {
		fmt.Fprintf(os.Stderr, "✗ vfkit not found — install it: brew install vfkit\n")
	} else {
		fmt.Fprintf(os.Stderr, "✓ vfkit found\n")
	}

	for _, p := range []string{cfg.KernelPath, cfg.InitrdPath} {
		if _, err := os.Stat(p); err == nil {
			fmt.Fprintf(os.Stderr, "✓ %s\n", p)
		} else {
			fmt.Fprintf(os.Stderr, "✗ missing %s\n", p)
		}
	}
	fmt.Fprintf(os.Stderr, "  build the guest image with:\n")
	fmt.Fprintf(os.Stderr, "    ./deploy/guest/build.sh        # builds + extracts to %s\n", cfg.WorkDir)
	fmt.Fprintf(os.Stderr, "  (it produces vmlinux and initramfs.zst in the workdir)\n")

	if _, err := os.Stat(filepath.Join(cfg.NoVNCDir, "vnc.html")); err == nil {
		fmt.Fprintf(os.Stderr, "✓ noVNC at %s\n", cfg.NoVNCDir)
	} else {
		fmt.Fprintf(os.Stderr, "… fetching noVNC to %s\n", cfg.NoVNCDir)
		if err := fetchNoVNC(cfg.NoVNCDir); err != nil {
			fmt.Fprintf(os.Stderr, "✗ noVNC download failed: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "✓ noVNC installed\n")
		}
	}
}

const noVNCTarball = "https://github.com/novnc/noVNC/archive/refs/tags/v1.6.0.tar.gz"

func fetchNoVNC(dest string) error {
	resp, err := http.Get(noVNCTarball)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: %s", resp.Status)
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
		// Strip the leading "noVNC-<version>/" path component.
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

func tokenDefault() string { return os.Getenv("WARMBOX_TOKEN") }

func cmdCreate(args []string) {
	addr := ":7070"
	token := tokenDefault()
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	fs.StringVar(&addr, "addr", addr, "daemon API address")
	fs.StringVar(&token, "token", token, "daemon token (default $WARMBOX_TOKEN)")
	_ = fs.Parse(args)

	resp, err := http.Post(withToken(apiBase(addr)+"/api/desktops", token), "application/json", nil)
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
	fmt.Fprintf(os.Stderr, "desktop %s ready\n", out.ID)
	fmt.Printf("%s%s\n", apiBase(addr), out.VNC)
}

func cmdList(args []string) {
	addr := ":7070"
	token := tokenDefault()
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	fs.StringVar(&addr, "addr", addr, "daemon API address")
	fs.StringVar(&token, "token", token, "daemon token (default $WARMBOX_TOKEN)")
	_ = fs.Parse(args)

	resp, err := http.Get(withToken(apiBase(addr)+"/api/desktops", token))
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
	fs.StringVar(&token, "token", token, "daemon token (default $WARMBOX_TOKEN)")
	_ = fs.Parse(args)
	rest := fs.Args()
	if len(rest) < 1 {
		fatal("Usage: warmbox destroy <id>")
	}
	req, _ := http.NewRequest(http.MethodDelete, withToken(apiBase(addr)+"/api/desktops/"+rest[0], token), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatal("Error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fatal("destroy failed: %s", resp.Status)
	}
	fmt.Fprintf(os.Stderr, "destroyed %s\n", rest[0])
}
