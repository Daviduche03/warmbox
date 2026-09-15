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

	_ "github.com/rclone/rclone/backend/all"
	rfs "github.com/rclone/rclone/fs"

	"runmesh/workspace/internal/api"
	"runmesh/workspace/internal/catalog"
	"runmesh/workspace/internal/config"
	"runmesh/workspace/internal/desktop"
	"runmesh/workspace/internal/pool"
	"runmesh/workspace/internal/volume"
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
	case "volume":
		cmdVolume(os.Args[2:])
	case "snapshot":
		cmdSnapshot(os.Args[2:])
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

  warmbox volume create <name> [--size 8G] [--from <name>] [--from-snapshot <id>]
  warmbox volume list
  warmbox volume clone <name> <new>
  warmbox volume rm <name>

  warmbox snapshot create <volume> [--name <id>]
  warmbox snapshot list
  warmbox snapshot rm <id>

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
	fs.StringVar(&cfg.Image, "image", cfg.Image, "guest boot profile: overlay|disk|initramfs|efi (empty = auto)")
	fs.StringVar(&cfg.EFIDisk, "efi-disk", cfg.EFIDisk, "EFI-bootable disk image (e.g. an installed Omarchy disk) for --image efi")
	fs.StringVar(&cfg.EFIVars, "efi-vars", cfg.EFIVars, "seed EFI variable store copied per VM (from the installing machine)")
	fs.StringVar(&cfg.GPU, "gpu", cfg.GPU, "virtio-gpu size, e.g. 1440x900 (empty = headless)")
	fs.BoolVar(&cfg.Input, "input", cfg.Input, "attach virtio keyboard/pointing devices")
	fs.BoolVar(&cfg.GUI, "gui", cfg.GUI, "open the hypervisor window (vfkit only; bring-up aid)")
	fs.StringVar(&cfg.NoVNCDir, "novnc", cfg.NoVNCDir, "noVNC asset directory")
	fs.StringVar(&cfg.VfkitPath, "vfkit", cfg.VfkitPath, "vfkit binary")
	fs.StringVar(&cfg.Backend, "backend", cfg.Backend, "hypervisor backend (vfkit)")
	fs.StringVar(&cfg.APIAddr, "addr", cfg.APIAddr, "HTTP listen address")
	fs.StringVar(&cfg.HostAddr, "host", cfg.HostAddr, "address guests use to reach this host")
	fs.UintVar(&cfg.MemMiB, "mem", cfg.MemMiB, "memory per VM (MiB)")
	fs.UintVar(&cfg.CPUs, "cpus", cfg.CPUs, "vCPUs per VM")
	fs.IntVar(&cfg.PoolSize, "pool", cfg.PoolSize, "warm pool size")
	fs.StringVar(&cfg.ShareDir, "share", cfg.ShareDir, "host directory shared with guests via virtiofs (mounted at /workspace)")
	fs.StringVar(&cfg.ShareTag, "share-tag", cfg.ShareTag, "virtiofs mount tag")
	fs.StringVar(&cfg.Token, "token", cfg.Token, "require this token on API/UI routes (empty = no auth)")
	fs.StringVar(&cfg.VolumeDir, "volume-dir", cfg.VolumeDir, "local cache dir for volume images")
	fs.StringVar(&cfg.VolumeBase, "volume-base", cfg.VolumeBase, "base ext4 image cloned for new volumes")
	fs.IntVar(&cfg.VolumeChunkMiB, "volume-chunk", cfg.VolumeChunkMiB, "volume transfer chunk size (MiB)")
	fs.StringVar(&cfg.VolumePrefix, "volume-prefix", cfg.VolumePrefix, "remote prefix for volumes")
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
		// EFI-booting images carry their own kernel in the disk.
		if cfg.Image != "efi" {
			fatal("missing guest kernel %s — build it with ./deploy/guest/build.sh", cfg.KernelPath)
		}
	}
	if cfg.Image == "efi" {
		if !stat(cfg.EFIDisk) {
			fatal("image efi needs --efi-disk pointing at an installed EFI disk")
		}
	} else {
		// Boot needs an overlay base, an ext4 disk, or the all-RAM initramfs.
		haveOverlay := stat(cfg.SquashPath) && stat(cfg.OverlayInitrdPath)
		haveDisk := stat(cfg.DiskPath) && stat(cfg.BootInitrdPath)
		if !haveOverlay && !haveDisk && !stat(cfg.InitrdPath) {
			fatal("missing guest rootfs — run ./deploy/guest/build.sh (need %s + %s, %s + %s, or %s)",
				cfg.SquashPath, cfg.OverlayInitrdPath, cfg.DiskPath, cfg.BootInitrdPath, cfg.InitrdPath)
		}
	}

	mgr := desktop.NewManager(cfg, os.Stderr)
	p := pool.New(mgr, cfg.PoolSize, os.Stderr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store := newVolumeStore(ctx, cfg)
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

	srv := &http.Server{Addr: cfg.APIAddr, Handler: api.New(mgr, p, cfg, store, cat, os.Stderr).Handler()}
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

// newVolumeStore builds a volume store backed by runmesh/R2 when credentials
// are configured, otherwise by a local directory so volumes still work for
// local development. Returns nil if no store can be created.
func newVolumeStore(ctx context.Context, cfg *desktop.Config) *volume.Store {
	chunk := int64(cfg.VolumeChunkMiB) << 20

	if rc, err := config.LoadGlobal(); err == nil && rc != nil && rc.DefaultBucket != "" {
		if f, err := rc.NewFs(ctx, rc.DefaultBucket, ""); err == nil {
			fmt.Fprintf(os.Stderr, "warmbox: volumes backed by remote bucket %q\n", rc.DefaultBucket)
			return volume.NewStore(f, cfg.VolumePrefix, cfg.VolumeDir, cfg.VolumeBase, chunk)
		} else {
			fmt.Fprintf(os.Stderr, "warmbox: volume remote unavailable (%v); using local storage\n", err)
		}
	}

	local := filepath.Join(cfg.WorkDir, "volume-remote")
	if err := os.MkdirAll(local, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "warmbox: volumes disabled: %v\n", err)
		return nil
	}
	f, err := rfs.NewFs(ctx, local)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warmbox: volumes disabled: %v\n", err)
		return nil
	}
	fmt.Fprintf(os.Stderr, "warmbox: volumes backed by local dir %s (no runmesh remote configured)\n", local)
	return volume.NewStore(f, cfg.VolumePrefix, cfg.VolumeDir, cfg.VolumeBase, chunk)
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
	vol := ""
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	fs.StringVar(&addr, "addr", addr, "daemon API address")
	fs.StringVar(&token, "token", token, "daemon token (default $WARMBOX_TOKEN)")
	fs.StringVar(&vol, "volume", "", "attach a persistent volume by name")
	_ = fs.Parse(args)

	var body io.Reader
	if vol != "" {
		b, _ := json.Marshal(map[string]string{"volume": vol})
		body = strings.NewReader(string(b))
	}
	resp, err := http.Post(withToken(apiBase(addr)+"/api/desktops", token), "application/json", body)
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
	rest := parseInterspersed(fs, args)
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
		fs.StringVar(&token, "token", token, "daemon token (default $WARMBOX_TOKEN)")
		size := fs.String("size", "", "volume size, e.g. 8G (default: base image size)")
		from := fs.String("from", "", "clone from an existing volume")
		fromSnap := fs.String("from-snapshot", "", "create from a snapshot id")
		rest := parseInterspersed(fs, args[1:])
		if len(rest) < 1 {
			fatal("Usage: warmbox volume create <name> [--size 8G] [--from <name>] [--from-snapshot <id>]")
		}
		payload, _ := json.Marshal(map[string]string{"name": rest[0], "size": *size, "from": *from, "from_snapshot": *fromSnap})
		resp, err := http.Post(withToken(apiBase(addr)+"/api/volumes", token),
			"application/json", strings.NewReader(string(payload)))
		doVolume(resp, err)

	case "list":
		fs := flag.NewFlagSet("volume list", flag.ExitOnError)
		fs.StringVar(&addr, "addr", addr, "daemon API address")
		fs.StringVar(&token, "token", token, "daemon token (default $WARMBOX_TOKEN)")
		_ = fs.Parse(args[1:])
		resp, err := http.Get(withToken(apiBase(addr)+"/api/volumes", token))
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
		fs.StringVar(&token, "token", token, "daemon token (default $WARMBOX_TOKEN)")
		rest := parseInterspersed(fs, args[1:])
		if len(rest) < 2 {
			fatal("Usage: warmbox volume clone <name> <new>")
		}
		payload, _ := json.Marshal(map[string]string{"name": rest[1]})
		resp, err := http.Post(withToken(apiBase(addr)+"/api/volumes/"+rest[0]+"/clone", token),
			"application/json", strings.NewReader(string(payload)))
		doVolume(resp, err)

	case "rm":
		fs := flag.NewFlagSet("volume rm", flag.ExitOnError)
		fs.StringVar(&addr, "addr", addr, "daemon API address")
		fs.StringVar(&token, "token", token, "daemon token (default $WARMBOX_TOKEN)")
		rest := parseInterspersed(fs, args[1:])
		if len(rest) < 1 {
			fatal("Usage: warmbox volume rm <name>")
		}
		req, _ := http.NewRequest(http.MethodDelete, withToken(apiBase(addr)+"/api/volumes/"+rest[0], token), nil)
		resp, err := http.DefaultClient.Do(req)
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
		fs.StringVar(&token, "token", token, "daemon token (default $WARMBOX_TOKEN)")
		name := fs.String("name", "", "snapshot id (default: random)")
		rest := parseInterspersed(fs, args[1:])
		if len(rest) < 1 {
			fatal("Usage: warmbox snapshot create <volume> [--name <id>]")
		}
		payload, _ := json.Marshal(map[string]string{"volume": rest[0], "name": *name})
		resp, err := http.Post(withToken(apiBase(addr)+"/api/snapshots", token),
			"application/json", strings.NewReader(string(payload)))
		doVolume(resp, err)

	case "list":
		fs := flag.NewFlagSet("snapshot list", flag.ExitOnError)
		fs.StringVar(&addr, "addr", addr, "daemon API address")
		fs.StringVar(&token, "token", token, "daemon token (default $WARMBOX_TOKEN)")
		_ = fs.Parse(args[1:])
		resp, err := http.Get(withToken(apiBase(addr)+"/api/snapshots", token))
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
		fs.StringVar(&token, "token", token, "daemon token (default $WARMBOX_TOKEN)")
		rest := parseInterspersed(fs, args[1:])
		if len(rest) < 1 {
			fatal("Usage: warmbox snapshot rm <id>")
		}
		req, _ := http.NewRequest(http.MethodDelete, withToken(apiBase(addr)+"/api/snapshots/"+rest[0], token), nil)
		resp, err := http.DefaultClient.Do(req)
		doVolume(resp, err)

	default:
		snapshotUsage()
		os.Exit(1)
	}
}
