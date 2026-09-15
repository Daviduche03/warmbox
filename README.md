# warmbox

Self-hosted GUI desktop microVMs on Apple Silicon, with portable cloud-backed
disks. Boot a Linux desktop in a second, hand a browser a link, and keep the
whole machine — files, installed apps, settings — on a disk that lives in object
storage (S3/R2) and can be cloned or restored on another host.

> **Status: experimental alpha.** macOS / Apple Silicon only today. Not hardened:
> no TLS, no VNC auth, single-user. See [Status](#status) and
> [Limitations](#limitations) before you run it on anything shared.

## What it does

- **Disposable computers, portable disks.** The OS is a read-only image shared by
  every VM; the writable layer is a per-VM *volume* stored as content-addressed
  chunks in an S3-compatible bucket (via [rclone](https://rclone.org)). Start a VM
  on any host, attach the volume, and it is the same machine.
- **Fast boot.** A warm pool of pre-booted VMs; a desktop is ready in ~1–3s.
- **Browser access.** Each desktop is streamed over noVNC — no client to install.
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
  daemon with `--backend qemu`. Verified on x86_64 KVM.

Plus: **Docker** (to build the guest image) and **Go 1.25+**.

## Quickstart

```sh
# 1. Build the guest image (kernel + rootfs + a 2 GiB base disk). First run is
#    slow (Docker + squashfs); artifacts land in ~/.warmbox by default.
./deploy/guest/build.sh

# 2. Build the CLI and fetch noVNC.
go build -o warmbox ./cmd/warmbox
./warmbox setup

# 3. Run the daemon (warm pool + REST API + noVNC bridge).
./warmbox daemon

# 4. Provision a desktop; prints a URL to open in your browser.
./warmbox create
```

On **Linux**, add `--backend qemu` to the daemon (KVM + `qemu-system-*`). QEMU's
user-mode networking isn't reachable host→guest, so the backend forwards a host
port to each guest's VNC; everything else is identical. Build the guest image for
the host arch with `PLATFORM=linux/amd64 ./deploy/guest/build.sh`.

Persistent volumes:

```sh
./warmbox volume create dev --size 8G     # grow-only; floor is VOLUME_BASE_SIZE
./warmbox create --volume dev             # boot a VM on that disk
./warmbox snapshot create dev             # freeze it
./warmbox volume clone dev dev-clean      # template a fresh box
```

Inside the guest:

```sh
warmbox-app new mydash          # scaffold a web app
warmbox-app install mydash      # appears in the XFCE menu (opens as an app window)
warmbox-app serve mydash 8080   # prints a /p/<vm>/8080/ link you can open
```

Storage is local by default (a directory under the workdir). To use a bucket,
point `runmesh` at it and the daemon picks it up:

```sh
./runmesh config set --endpoint https://<acct>.r2.cloudflarestorage.com \
  --access-key <KEY> --secret-key <SECRET> --bucket <BUCKET>
```

## Status

| Area | State |
|---|---|
| Guest desktop (XFCE over noVNC), warm pool | ✅ works |
| Persistent, cloud-backed volumes (chunked) | ✅ works |
| Grow a volume (`--size`) | ✅ works (grow-only) |
| Disk snapshots + clone | ✅ works |
| Linux host (QEMU/KVM) | ✅ works (x86_64 verified) |
| `warmbox-app` (menu apps) | 🟡 experimental |
| Publish a guest port at a URL (`/p/<vm>/<port>/`) | 🟡 experimental |
| Linux host (Cloud Hypervisor / Firecracker) | ❌ not yet |
| Windows host (WSL2) | ❌ not yet |
| Agent API: exec + files | ✅ works |
| Agent API: streaming + screenshot/input | ❌ not yet |
| Memory snapshot / ~100 ms restore | ❌ blocked on macOS |
| TLS / per-guest VNC auth | ❌ not yet |
| GPU / audio | ❌ not supported |

## Limitations

- **Apple Silicon only.** `vfkit` wraps Apple's Virtualization.framework; there is
  no Linux/Windows port yet. ARM64 guests only.
- **Local-only security posture.** Off by default: no TLS, guest VNC is
  `-SecurityTypes None`, guest runs as **root**, the API token is optional. Don't
  expose it.
- **No programmatic agent channel.** Agents drive the GUI; there is no exec/SSH
  API. `warmbox-app`/`serve` are run from a guest shell.
- **No memory snapshots.** Apple's framework exposes no VM state save/restore, so
  fast "restore anywhere" needs a Linux backend (see `docs/snapshots.md`).
- **Single writer.** A volume attaches to one VM at a time; the lock is
  in-process (fine for one host, not a fleet).
- **Commit cost.** Committing hashes the changed extents; a commit is fast for
  sparse disks but is still O(used) today.

## Repository layout

```
cmd/warmbox         the orchestrator CLI (daemon, create, volume, snapshot)
cmd/runmesh         the storage/sync CLI (up/down/watch/mount)
internal/desktop    boot/attach microVMs (vfkit)
internal/volume     persistent volumes (chunked, content-addressed)
internal/cloudstore shared chunk+manifest engine
internal/catalog    SQLite metadata (volumes, desktops, snapshots, leases)
internal/api        REST API, noVNC bridge, guest port proxy
internal/vnc        WebSocket→TCP VNC bridge
deploy/guest        guest image (Dockerfile, init, overlay-init, apps)
docs/               architecture, volumes, snapshots, oss-positioning, vision
```

## Docs

- [`docs/architecture.md`](docs/architecture.md) — the cloud-native design.
- [`docs/volumes.md`](docs/volumes.md) — volumes, sizing, API.
- [`docs/snapshots.md`](docs/snapshots.md) — snapshots & fast-resume plan.
- [`docs/oss-positioning.md`](docs/oss-positioning.md) — what could be a shared primitive.
- [`docs/vision.md`](docs/vision.md) — the original "Runmesh" vision.

## Help wanted

- **Cloud Hypervisor / Firecracker backend** — leaner boot and real memory
  snapshot/restore (the "~100 ms restore anywhere" story).
- **Windows** — either a **WSL2 setup guide** (WSL2 is Linux + KVM, so it should
  work as-is) or a native **WHPX** QEMU backend (`-accel whpx`). Unverified; the
  seam is `internal/desktop/backend.go`.
- **Agent API** — phase 1 (`exec` + `files`) works; sessions, streaming/background
  exec, and the computer-use endpoints are next (`docs/agent-api.md`).
- **WebRTC streaming** — replace noVNC/RFB for latency and bandwidth.
- **macOS VNC bridge** — replace the `/usr/bin/nc` fallback with a signed helper.
- **Tests** — API and VM lifecycle integration tests.

See [`CONTRIBUTING.md`](CONTRIBUTING.md).

## License

[Apache-2.0](LICENSE).
