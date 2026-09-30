# warmbox

Self-hosted GUI desktop microVMs on Apple Silicon, with portable cloud-backed
disks. Boot a Linux desktop in a second, hand a browser a link, and keep the
whole machine — files, installed apps, settings — on a disk that lives in object
storage (S3/R2) and can be cloned or restored on another host.

Two guest images ship out of the box: a tiny **Alpine + XFCE** desktop (~776 MB,
boots in ~1s from a shared read-only rootfs) and a full **Omarchy**
(Arch + Hyprland, ~8.4 GB) built as an EFI disk. You pick the OS at create time.

> **Status: experimental alpha.** macOS / Apple Silicon is the primary target;
> Linux + KVM works for the built-in image. Not hardened: no TLS, guest VNC is
> unauthenticated, the guest runs as root, the API token is optional. See
> [Status](#status) and [Limitations](#limitations) before you run it on
> anything shared.

## What it does

- **Disposable computers, portable disks.** The OS is a read-only image shared by
  every VM; the writable layer is a per-VM *volume* stored as content-addressed
  chunks in an S3-compatible bucket (via [rclone](https://rclone.org)). Start a VM
  on any host, attach the volume, and it is the same machine.
- **Fast boot.** Create clones the golden image with an APFS copy-on-write copy
  (instant, shared blocks) and boots it; a warm pool of pre-booted VMs makes a
  desktop ready in ~1s.
- **Choose your OS at create time.** `warmbox create --image omarchy` boots a
  clone of the Omarchy disk; no image argument uses the built-in desktop.
- **Browser access.** Each desktop is streamed over noVNC — no client to install.
- **Agent API.** `exec` + file read/write/list inside any guest over HTTP, no SSH
  keys (`docs/agent-api.md`).
- **Snapshots & clones.** A snapshot is a frozen disk manifest, so it is O(1) and
  shares every chunk; clone it into a fresh box.
- **Apps.** `warmbox-app` turns a directory (e.g. an agent-built HTML dashboard)
  into a menu app, persisted on the volume.
- **Publish.** `GET /p/<vm>/<port>/` reverse-proxies to a port inside a guest, so
  anything served there (a dashboard, an app) gets a shareable URL.

## Requirements

**Host** — one of:
- **macOS on Apple Silicon**, with `vfkit` (`brew install vfkit`); or
- **Linux with KVM** and `qemu-system-x86_64` / `qemu-system-aarch64` — run the
  daemon with `--backend qemu`. Verified on x86_64 KVM. Note: the Linux backend
  cannot boot EFI disk images yet (no OVMF), so image guests are macOS-only.

Plus: **Docker** (to build the built-in guest image) and **Go 1.25+**.

## Quickstart

```sh
# 1. Build the built-in guest image (kernel + rootfs + a 2 GiB base disk).
#    First run is slow (Docker + squashfs); artifacts land in ~/.warmbox.
./deploy/guest/build.sh

# 2. Build the CLI and fetch noVNC.
go build -o warmbox ./cmd/warmbox
./warmbox setup

# 3. Run the daemon (warm pool + REST API + noVNC bridge).
./warmbox daemon

# 4. Provision a desktop; prints a URL to open in your browser.
./warmbox create
```

Each desktop gets a short URL (`http://localhost:7070/d/<id>`); open it and
noVNC fills the page. The token is dropped from the address bar after the first
load. To run the daemon in the background instead of the foreground:

```sh
./warmbox service install --pool 1   # macOS launchd; pool 1 = ~1s creates
./warmbox service status             # start | stop | restart | status
```

On **Linux**, add `--backend qemu` to the daemon (KVM + `qemu-system-*`). QEMU's
user-mode networking isn't reachable host→guest, so the backend forwards a host
port to each guest's VNC; everything else is identical. Build the guest image for
the host arch with `PLATFORM=linux/amd64 ./deploy/guest/build.sh`.

## Images

A named image is a directory under `$WARMBOX_HOME/images/<name>/` holding
`disk.raw` (a bootable disk), an optional `efi-vars.fd` seed, and an optional
`meta.json` (`{"gpu","mem_mib","cpus","input"}`). The daemon discovers them at
startup and you choose one per desktop:

```sh
./warmbox images                     # list what the daemon can boot
./warmbox create --image omarchy     # a full Omarchy (Hyprland) desktop
./warmbox create --image default     # the built-in desktop (aliases: xfce, alpine)
./warmbox create                     # whatever --image the daemon was started with
```

| image | base | size on disk | boot |
|---|---|---|---|
| `default` | Alpine + XFCE | ~776 MB (compressed squashfs base) | ~1s, shared rootfs |
| `lxqt` | Alpine + LXQt | ~800 MB (compressed squashfs base) | ~7s cold |
| `omarchy` | Arch + Hyprland/Quickshell | 24 GB sparse, ~8.4 GB real | ~13s cold, instant if pooled |

The built-in and `lxqt` images are *overlay* images (a shared read-only squashfs
plus a writable upper); `omarchy` is an *EFI disk*. Both kinds are named images
and are selected the same way. Build a variant with:

```sh
DESKTOP=lxqt THEME=ambiance VARIANT=lxqt ./deploy/guest/build.sh   # -> images/lxqt
```

Images travel as a single compressed artifact:

```sh
./warmbox image pack omarchy                 # -> ~/.warmbox/images/omarchy.tar.zst (~3.9 GB)
./warmbox image pull omarchy <file-or-url>   # expand into ~/.warmbox/images/omarchy
./warmbox image list                         # local images (offline)
```

See [`deploy/omarchy/README.md`](deploy/omarchy/README.md) for how the Omarchy
image is built and provisioned (it stands on the community aarch64 port).

## Agent API

Run commands and move files inside any guest over HTTP — the daemon proxies to
`warmbox-agent` in the VM, reachable on the same dial path as VNC:

```sh
curl -X POST localhost:7070/api/desktops/$ID/exec -d '{"cmd":"uname -a"}'
curl "localhost:7070/api/desktops/$ID/files?path=/home"
curl "localhost:7070/api/desktops/$ID/file?path=/etc/hostname"
```

Phase 1 is `exec` + files; streaming/background exec, `tty`, and
screenshot/input are next. Design: [`docs/agent-api.md`](docs/agent-api.md).

## Persistent volumes

```sh
./warmbox volume create dev --size 8G     # grow-only; floor is VOLUME_BASE_SIZE
./warmbox create --volume dev             # boot a VM on that disk
./warmbox snapshot create dev             # freeze it
./warmbox volume clone dev dev-clean      # template a fresh box
```

Volumes attach to image guests too. On the built-in image the volume *is* the
writable overlay (the whole machine persists); on an image guest like Omarchy it
is a data disk mounted at `/volume`, flushed every couple of seconds.

Inside the guest:

```sh
warmbox-app new mydash          # scaffold a web app
warmbox-app install mydash      # appears in the XFCE menu (opens as an app window)
warmbox-app serve mydash 8080   # prints a /p/<vm>/8080/ link you can open
```

Storage is local by default (a directory under the workdir). To keep volumes in
a bucket so the same disk can attach on another machine, point warmbox at it
(also available in the dashboard under Settings):

```sh
warmbox cloud set --endpoint https://<acct>.r2.cloudflarestorage.com \
  --access-key <KEY> --secret-key <SECRET> --bucket <BUCKET>
warmbox cloud show          # where volume bytes live
```

Restart the daemon afterwards (`warmbox service restart`) and it picks the
bucket up.

## Status

| Area | State |
|---|---|
| Guest desktop (XFCE over noVNC), warm pool | ✅ works |
| Named images + create-time selection | ✅ works |
| Omarchy guest (EFI disk image) | ✅ works (macOS) |
| Persistent, cloud-backed volumes (chunked) | ✅ works |
| Volumes on image guests (`/volume`) | ✅ works |
| Grow a volume (`--size`) | ✅ works (grow-only) |
| Disk snapshots + clone | ✅ works |
| Image pack / pull (compressed artifacts) | ✅ works |
| Agent API: exec + files | ✅ works |
| Agent API: background/streaming runs + sessions | ✅ works |
| Linux host (QEMU/KVM) | ✅ works (x86_64 verified; no EFI images) |
| `warmbox-app` (menu apps) | 🟡 experimental |
| Publish a guest port at a URL (`/p/<vm>/<port>/`) | 🟡 experimental |
| Agent API: tty, screenshot/input (computer-use) | ❌ not yet |
| Linux host (Cloud Hypervisor / Firecracker) | ❌ not yet |
| Windows host (WSL2 / native WHPX) | ❌ not yet |
| Memory snapshot / ~100 ms restore | ❌ blocked on macOS |
| TLS / per-guest VNC auth | ❌ not yet |
| GPU / audio | ❌ not supported |

## Limitations

- **Apple Silicon only** (for image guests). `vfkit` wraps Apple's
  Virtualization.framework; ARM64 guests only. The Linux/QEMU backend boots the
  built-in image but not EFI disks.
- **Local-only security posture.** Off by default: no TLS, guest VNC is
  `-SecurityTypes None`, the guest runs as **root**, the API token is optional.
  Don't expose it.
- **No memory snapshots.** Apple's framework exposes no VM state save/restore, so
  fast "restore anywhere" needs a Linux backend (see `docs/snapshots.md`).
- **Guests have no GPU.** AVF exposes virtio-gpu without 3D, so a Wayland desktop
  composites through Mesa `llvmpipe`; the Omarchy image is tuned for it (small
  output, effects off). X11 is lighter for remote display.
- **Images are big.** Arch + Omarchy is multi-GB; the packed artifact is ~3.9 GB,
  but because btrfs fragments its free space and APFS only preserves large holes,
  an expanded copy may not be as sparse as you'd like. The built-in image is the
  small one.
- **Single writer.** A volume attaches to one VM at a time; the lock is
  in-process (fine for one host, not a fleet).
- **Commit cost.** Committing hashes the changed extents; a commit is fast for
  sparse disks but is still O(used) today.

## Repository layout

```
cmd/warmbox         the CLI (daemon, create, image, volume, snapshot, cloud)
internal/desktop    boot/attach microVMs (vfkit, qemu); images, boot modes, pack
internal/volume     persistent volumes (chunked, content-addressed)
internal/cloudstore shared chunk+manifest engine
internal/catalog    SQLite metadata (volumes, desktops, snapshots, leases)
internal/egress     default-deny egress policy + forward proxy
internal/api        REST API, noVNC bridge, guest port proxy, agent proxy
internal/vnc        WebSocket→TCP VNC bridge
deploy/guest        built-in image (Dockerfile, init, overlay-init, apps, agent)
deploy/omarchy      Omarchy image build + guest provisioning
docs/               architecture, volumes, snapshots, agent-api, egress, oss-positioning
```

## Docs

- [`docs/architecture.md`](docs/architecture.md) — the cloud-native design.
- [`docs/volumes.md`](docs/volumes.md) — volumes, sizing, API.
- [`docs/snapshots.md`](docs/snapshots.md) — snapshots & fast-resume plan.
- [`docs/agent-api.md`](docs/agent-api.md) — the guest agent API and roadmap.
- [`deploy/omarchy/README.md`](deploy/omarchy/README.md) — the Omarchy image.
- [`docs/oss-positioning.md`](docs/oss-positioning.md) — what could be a shared primitive.
- [`docs/vision.md`](docs/vision.md) — the original "Runmesh" vision.

## Help wanted

- **Image distribution** — host packed images (OCI/R2) and make `warmbox image
  pull` resolve a bare name, so nobody builds Omarchy locally.
- **EFI on Linux** — OVMF support in the QEMU backend so image guests run there too.
- **Cloud Hypervisor / Firecracker backend** — leaner boot and real memory
  snapshot/restore (the "~100 ms restore anywhere" story).
- **Windows** — a **WSL2 setup guide** (WSL2 is Linux + KVM) or a native **WHPX**
  QEMU backend. Unverified; the seam is `internal/desktop/backend.go`.
- **Agent API** — streaming/background exec, `tty`, and computer-use
  (`docs/agent-api.md`).
- **WebRTC streaming** — replace noVNC/RFB for latency and bandwidth.
- **macOS VNC bridge** — replace the `/usr/bin/nc` fallback with a signed helper.
- **Tests** — API and VM lifecycle integration tests.

See [`CONTRIBUTING.md`](CONTRIBUTING.md).

## License

[Apache-2.0](LICENSE).
