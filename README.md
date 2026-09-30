# warmbox

Self-hosted GUI desktop microVMs on Apple Silicon, with portable cloud-backed
disks. Boot a Linux desktop in a second, hand a browser a link, and keep the
whole machine — files, installed apps, settings — on a disk that lives in object
storage (S3/R2) and can be cloned or restored on another host.

Two guest images ship out of the box: a tiny **Alpine + XFCE** desktop (~776 MB,
boots in ~1s from a shared read-only rootfs) and a full **Omarchy**
(Arch + Hyprland, ~8.4 GB) built as an EFI disk. You pick the OS at create time.

> **Status: experimental alpha.** macOS / Apple Silicon is the primary target;
> Linux + KVM works for the built-in image. Not hardened: plaintext HTTP with no
> TLS, the guest's VNC server accepts anyone who can reach it, and the guest runs
> as **root**. The API itself *is* gated by accounts and workspaces — see
> [Limitations](#limitations) — but the transport is unencrypted, so don't expose
> it to an untrusted network.

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

To **build from source** you also need **Docker** (for the guest image) and
**Go 1.25+** — but an install from a release needs neither.

## Install

Prebuilt binaries ship with a prebuilt guest image, so there is no Docker step:

```sh
brew install --cask Daviduche03/warmbox/warmbox   # macOS, or Linuxbrew
warmbox setup                                     # fetch noVNC + the guest image
warmbox service install --pool 1                  # run the daemon in the background
```

Then open <http://localhost:7070> (or `warmbox create` for a desktop and its
URL). `warmbox setup` prints what it found and what to do next.

`setup` downloads the guest image for your host architecture — the guest has to
match the hypervisor, so there is one image per arch — verifies it against the
published sha256, and installs it. It is resumable: an interrupted download
picks up where it left off, and re-running it is safe. Point it somewhere else
with `--image-url` (plus `--image-sha256` if you don't publish a `.sha256`).

<details>
<summary>Without Homebrew</summary>

Download the tarball for your platform from the
[releases page](https://github.com/Daviduche03/warmbox/releases), unpack it, and
run the same three commands (`./warmbox setup`, `./warmbox service install`).

</details>

## Quickstart (from source)

```sh
# 1. Build the built-in guest image (kernel + rootfs + a 2 GiB base disk).
#    First run is slow (Docker + squashfs); artifacts land in ~/.warmbox.
./deploy/guest/build.sh

# 2. Build the CLI and fetch noVNC.
go build -o warmbox ./cmd/warmbox
./warmbox setup --no-image      # the image is already built above

# 3. Run the daemon (warm pool + REST API + noVNC bridge).
./warmbox daemon

# 4. Provision a desktop; prints a URL to open in your browser.
./warmbox create
```

Maintainers publish an image with `warmbox image pack --builtin -o <file>`,
which also writes the `.sha256` that `setup` verifies.

### Releasing

Push a tag (`git tag v0.2.0 && git push origin v0.2.0`). The release workflow
builds the binaries for darwin/arm64 and linux/{amd64,arm64}, publishes them with
`checksums.txt`, and updates the Homebrew cask.

The guest image is published separately by the **guest image** workflow, which is
manual on purpose — it takes ~15 minutes per architecture and a failure there
should not block a binary release. Run it with the tag as input and it attaches
`warmbox-image-<tag>-<arch>.tar.zst` plus its `.sha256` to that release. Those
names are exactly what `warmbox setup` looks for, so the tag and the asset must
agree.

Two things must exist before the first release:

- a `homebrew-warmbox` tap repository, and
- a `HOMEBREW_TAP_GITHUB_TOKEN` secret — a PAT with write access to that repo,
  because the default `GITHUB_TOKEN` cannot push to a different repository.

Each desktop gets a short URL (`http://localhost:7070/d/<id>`); open it and
noVNC fills the page. The token is dropped from the address bar after the first
load. To run the daemon in the background instead of the foreground:

```sh
./warmbox service install --pool 1   # launchd on macOS, systemd on Linux
./warmbox service status             # start | stop | restart | status
```

On Linux it installs a **systemd** unit instead: a system unit at
`/etc/systemd/system/warmbox.service` when you run it as root, or a per-user
unit (`~/.config/systemd/user/`, with lingering enabled so it starts at boot)
otherwise. Logs live in the journal — `journalctl -u warmbox.service -f`.

Warm VMs hold RAM even when nobody is creating anything, so the pool drains
itself after 15 minutes without a lease (`--pool-idle-timeout 30m`, `0` keeps it
warm forever); the next create pays a cold boot instead. Desktops can also be
frozen from the dashboard's row menu: **Pause** stops the guest's CPUs and keeps
its memory, **Resume** picks the session back up, and **Destroy** is what hands
the RAM back. Pause works on both backends (vfkit's REST API, QEMU's monitor).

On **Linux**, add `--backend qemu` to the daemon (KVM + `qemu-system-*`).
`warmbox service install` passes that flag for you and manages the daemon as a
systemd unit, so the same command works on both hosts. QEMU's user-mode
networking isn't reachable host→guest, so the backend forwards a host port to
each guest's VNC; everything else is identical. Build the guest image for the
host arch with `PLATFORM=linux/amd64 ./deploy/guest/build.sh`.

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
| Accounts, workspaces, per-workspace scoping | ✅ works (bcrypt + session/API-token auth) |
| Pause / resume a desktop | ✅ works (CPU only — the guest keeps its memory) |
| Warm-pool idle drain | ✅ works (`--pool-idle-timeout`, default 15m) |
| Named images + create-time selection | ✅ works |
| Omarchy guest (EFI disk image) | ✅ works (macOS) |
| Persistent, cloud-backed volumes (chunked) | ✅ works |
| Volumes on image guests (`/volume`) | ✅ works |
| Grow a volume (`--size`) | ✅ works (grow-only) |
| Disk snapshots + clone | ✅ works |
| Image pack / pull (compressed artifacts) | ✅ works |
| Install without Docker (`warmbox setup` fetches the image) | ✅ works (no release published yet) |
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

- **EFI disk images are macOS-only.** Omarchy is an EFI disk, and the QEMU
  backend ships no OVMF firmware, so it cannot boot it. (vfkit wraps Apple's
  Virtualization.framework, which is ARM64-only and handles the firmware itself.)
  Overlay images — the built-in one and `lxqt` — need no firmware and take the
  same path on both backends: the built-in image is verified on Linux/QEMU, a
  *named* overlay image there is not.
- **Authorised, but not encrypted.** The API is not open: accounts and workspaces
  gate every stateful route, and everything that reaches a desktop — console
  streams (`/d/`, `/websockify/`, `/vnc/`), the agent API, published guest ports
  (`/p/`) — is scoped to the caller's workspace; warm-pool VMs are daemon
  capacity and are never listed or controllable as desktops. What is missing is
  confidentiality and guest hardening: there is still no TLS, the guest's VNC
  server runs with `-SecurityTypes None`, and the guest is **root**. The guest
  only sits behind the hypervisor's NAT, so its VNC port is reachable from the
  host — and therefore through the authenticated daemon — rather than from the
  LAN. All of it assumes a machine you trust; don't expose it to an untrusted
  network.
- **No memory snapshots.** Apple's framework exposes no VM state save/restore, so
  fast "restore anywhere" needs a Linux backend (see `docs/snapshots.md`). Pause
  is not a substitute: it freezes the guest's CPUs in place but its memory stays
  allocated. To actually reclaim RAM, destroy the desktop (its volume keeps your
  files) or let the warm pool drain.
- **Guests have no GPU.** The hypervisor exposes virtio-gpu without 3D (AVF on
  macOS, virtio on QEMU), so a Wayland desktop composites through Mesa
  `llvmpipe`; the Omarchy image is tuned for it (small output, effects off). X11
  is far lighter for remote display.
- **Images are big.** The built-in image is ~800 MB compressed and `warmbox
  setup` downloads it in one piece. Omarchy is the heavy one: ~8.4 GB on disk,
  ~3.9 GB packed, and because btrfs fragments its free space and APFS only
  preserves large holes, an expanded copy may not be as sparse as you'd like.
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
- [`docs/egress.md`](docs/egress.md) — the egress policy and what it does not do yet.
- [`deploy/omarchy/README.md`](deploy/omarchy/README.md) — the Omarchy image.
- [`docs/oss-positioning.md`](docs/oss-positioning.md) — what could be a shared primitive.

## Help wanted

- **Named-image distribution** — the built-in image is a release artifact now
  (`warmbox setup` downloads and verifies it), but *named* images are not:
  `warmbox image pull` still wants a file or a URL, so Omarchy has to be built
  locally. Publishing them, and letting `pull omarchy` resolve a bare name, is
  the remaining half.
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
