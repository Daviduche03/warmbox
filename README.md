# warmbox

**A Linux desktop you open in a browser tab — on a machine whose disk you can
move to another computer.**

warmbox runs small virtual machines on a host you control. Each one is an
ordinary Linux desktop: a window manager, a browser, a terminal, whatever you
install. It is streamed to your browser over VNC, so there is nothing to install
on the machine you are sitting at.

The unusual part is where the desktop lives. The operating system is a read-only
image that every VM boots from, and everything you change — files, settings,
installed apps — goes to that machine's **volume**, stored as chunks in an
S3-compatible bucket. The volume *is* the computer. Start it on your laptop
today, on a server next week, and it is the same machine.

```
  your browser ─────────────┐
                            │  noVNC over a websocket
                            ▼
                 ┌────────────────────────┐
                 │  warmbox daemon        │  REST API · warm pool · VNC bridge
                 │  (one host)            │
                 └───────────┬────────────┘
                             │  boots microVMs (vfkit on macOS, QEMU on Linux)
                 ┌───────────▼────────────┐
                 │  guest VM              │
                 │    ┌───────────────┐   │
     read-only   │    │  OS image     │   │  shared by every VM
     ────────────┼───►│  Alpine, Arch │   │
                 │    └───────────────┘   │
                 │    ┌───────────────┐   │
     your disk   │    │  volume       │   │  files and apps, in a bucket
     ────────────┼───►│               │   │  or a local directory
                 │    └───────────────┘   │
                 └────────────────────────┘
```

> **Status: beta.** Tested and packaged, but single-host and short of
> real-world mileage; the defaults assume a host you trust. Don't put this on
> the open internet: the dashboard serves plaintext HTTP unless you pass
> `--tls-cert`/`--tls-key`, and guests run as **root** inside the VM. The API
> itself *is* behind accounts and workspaces — see [Limitations](#limitations).

## Get started

You need a host: **macOS on Apple Silicon**, or **Linux with KVM**. Then:

```sh
# 1. install the binary
brew install --cask Daviduche03/warmbox/warmbox    # macOS (or Linuxbrew)

# 2. let it install the rest: hypervisor, noVNC, and the guest image
warmbox setup

# 3. run the daemon in the background, one warm VM ready to hand out
warmbox service install --pool 1
```

Open <http://localhost:7070>. The first time, it asks you to create an account —
that account **owns** the instance. From the dashboard press **New desktop** and
you have a Linux desktop in a browser tab, usually in about a second.

Prefer the command line? Sign in once, then create:

```sh
warmbox login
warmbox create            # prints the desktop's URL
```

`warmbox setup` installs what is missing rather than handing you a list of
chores, and it is safe to re-run. It installs the hypervisor for your host
(`brew install vfkit`, or QEMU from your package manager), then gets the guest
image: it downloads the published one for your architecture, verified against its
sha256 and resumable — or, when nothing is published for your platform, fetches
the source and builds it locally, installing Docker first if it has to.
`--no-install` makes it report instead of installing; `--image-url` and
`--image-sha256` point it at an archive of your own.

<details>
<summary>Other ways to install the binary</summary>

One line on a server, which verifies the release's checksum before installing
anything:

```sh
curl -fsSL https://raw.githubusercontent.com/Daviduche03/warmbox/master/install.sh | sh
```

It installs to `/usr/local/bin` as root, otherwise `~/.local/bin`. That URL picks
which copy of the script runs, not which version you get — with `WARMBOX_VERSION`
unset it installs the newest release. To pin both, take the script from a tag and
say so:

```sh
curl -fsSL https://raw.githubusercontent.com/Daviduche03/warmbox/v0.3.0/install.sh \
  | WARMBOX_VERSION=v0.3.0 sh
```

Debian/Ubuntu and Fedora/RHEL packages are tidier on a server (they deliberately
do not fetch the guest image — that stays `warmbox setup`):

```sh
sudo apt install ./warmbox_0.3.0_linux_amd64.deb
sudo rpm -i warmbox_0.3.0_linux_amd64.rpm
```

Or unpack the tarball for your platform from the
[releases page](https://github.com/Daviduche03/warmbox/releases) and run the same
commands: `./warmbox setup`, then `./warmbox service install --pool 1`.

</details>

## What you can do with it

**Get a desktop in about a second.** The daemon keeps a *warm pool* of VMs that
are already booted. `warmbox create` (or New desktop) hands you one instead of
waiting for a boot. Anything else — a different image, a custom size, a volume —
cold-boots in a few seconds.

**Choose the operating system at create time.** `warmbox create --image lxqt`.
See [Choosing an operating system](#choosing-an-operating-system).

**Keep your machine on a portable disk.** Attach a volume and the whole system
persists to it; detach and carry the volume elsewhere. See [Your disk, and where
it lives](#your-disk-and-where-it-lives).

**Drive it from code.** Every guest runs a small agent, so you can run commands
and move files over HTTP without SSH keys or a screen at all:
`curl -X POST localhost:7070/api/desktops/$ID/exec -d '{"cmd":"uname -a"}'`. See
[Driving it from code](#driving-it-from-code).

**Reach a service inside a guest.** `GET /p/<vm>/<port>/` reverse-proxies to a
port in the guest, so a dashboard or dev server running there has a URL you can
open or share.

**Freeze a desktop instead of losing it.** Pause stops the guest's CPUs and keeps
its memory; Resume picks the session up; Destroy is what actually hands the RAM
back. A volume survives all three.

**Snapshot and clone a disk.** A snapshot is a frozen manifest, so it is
effectively instant and shares every chunk with the volume it came from; cloning
it gives you a fresh machine from that point.

## Choosing an operating system

Desktops boot from **guest images**. Every image warmbox can build is one file in
[`deploy/images/`](deploy/images) — that file is the source of truth. It picks
the engine that builds the image, and it generates the image's `meta.json`, which
is all the daemon reads at runtime.

| image | based on | screen | download | cold boot |
|---|---|---|---|---|
| `xfce` | Alpine + XFCE | yes — the default | ~0.9 GB | ~1s pooled, ~7s cold |
| `lxqt` | Alpine + LXQt | yes | ~0.9 GB | ~7s |
| `headless` | Alpine, no desktop | **none** | ~0.15 GB | ~6s |
| `omarchy` | Arch + Hyprland | yes — macOS hosts only | ~3.9 GB packed | ~13s |

Sizes and boot times are approximate, and the times come from an Apple Silicon
host — they will differ on QEMU, and they move with the guest.

```sh
warmbox images                        # what this daemon can boot right now
warmbox create --image lxqt           # pick one
warmbox create --image headless       # no screen: the agent API is the only way in
warmbox create                        # whatever --image the daemon was started with
```

**Headless images** are built without an X server, a desktop session or a
browser at all — not merely one that never starts. The guest still runs the
agent, so `exec`, files, runs and sessions all work — there is just nothing to
look at, and no VNC port to connect to. It is a property of the image rather
than a flag on `create`, so the guest and the daemon cannot disagree about
whether a screen exists.

### Building, downloading, and moving images

The dashboard's **Images** page lists what is installed next to what this build
knows about but has not fetched, and an admin can download one from there. The
same thing from a shell:

```sh
warmbox image build                   # what can be built, and what is installed
warmbox image build lxqt              # build one from a checkout
warmbox image build --all             # every image the checkout can build
warmbox image pull headless           # download the published one for this platform
warmbox image pack lxqt -o lxqt.tar.zst   # pack one to move it, + .sha256
warmbox image list                    # local images, offline
```

An image lands in `~/.warmbox/images/<name>/` — either an *overlay* image
(`vmlinux` + `rootfs.squashfs` + `initramfs-overlay`, one read-only rootfs shared
by every VM) or an *EFI disk* (`disk.raw`), plus `meta.json`. There is no
"built-in" image: the daemon boots the image you name, and the daemon's `--image`
decides what an unnamed create gets.

### Adding your own image

Write a file and build it — nothing else has to know:

```yaml
# deploy/images/epiphany.yaml
name: epiphany
engine: overlay          # overlay | efi
desktop: xfce
theme: win11
browser: epiphany
headless: false
publish: false           # true ships it on the images release
```

The `overlay` engine builds a rootfs from `deploy/guest/Dockerfile`; adding a
desktop, a theme or a browser is a Dockerfile change, and the config is how you
say *which combination* is an image. The `efi` engine (used by `omarchy`) stages
a disk that was already installed on a machine, so it needs input from the host —
`warmbox image build --all` skips those and builds the rest.

## Your disk, and where it lives

```sh
warmbox volume create dev --size 8G     # grow-only; the floor is VOLUME_BASE_SIZE
warmbox create --volume dev             # boot a machine whose disk is that volume
warmbox snapshot create dev             # freeze it
warmbox volume clone dev dev-clean      # a fresh machine from that point
```

On an overlay image the volume **is** the writable layer: the whole machine
persists, and destroying the desktop keeps every file. On an EFI image like
Omarchy it is a data disk mounted at `/volume`, flushed every couple of seconds.

Storage is local by default, under the workdir. To keep volumes in a bucket so
the same disk can attach on another host, point warmbox at it (also under
**Settings** in the dashboard):

```sh
warmbox cloud set --endpoint https://<account>.r2.cloudflarestorage.com \
  --access-key <KEY> --secret-key <SECRET> --bucket <BUCKET>
warmbox cloud show          # where volume bytes live
```

Restart the daemon afterwards (`warmbox service restart`) and it picks the bucket
up. This is how "the same machine, elsewhere" actually works: it is not a clone
or an export, it is the same chunks in the same bucket.

### Apps

`warmbox-app` turns a directory into a menu entry, so a thing you built — an
agent-generated dashboard, a tool you compiled — behaves like an installed app
and lives on the volume with the rest of your machine:

```sh
warmbox-app new mydash          # scaffold a web app
warmbox-app install mydash      # appears in the menu, opens as an app window
warmbox-app serve mydash 8080   # prints a /p/<vm>/8080/ link you can open
```

## Driving it from code

Every guest runs `warmbox-agent`, and the daemon proxies to it on the same dial
path it uses for VNC. No SSH, no keys, no screen:

```sh
curl -X POST localhost:7070/api/desktops/$ID/exec -d '{"cmd":"uname -a"}'
curl "localhost:7070/api/desktops/$ID/files?path=/home"
curl "localhost:7070/api/desktops/$ID/file?path=/etc/hostname"
```

There are also background/streaming runs (`/runs`) and interactive sessions
(`/sessions`). This is the only way in to a **headless** image, and it is how you
would put an agent inside a machine. Design and roadmap:
[`docs/agent-api.md`](docs/agent-api.md).

## Running it day to day

**The daemon** serves the dashboard and the API on `127.0.0.1:7070` by default.
Guests get a second listener on the vmnet gateway that answers nothing but the
readiness callback, so the API is never reachable from the network while guests
can still report that they booted.

```sh
warmbox service install --pool 1   # launchd on macOS, systemd on Linux
warmbox service status             # start | stop | restart | status
```

On Linux it installs a systemd unit — `/etc/systemd/system/warmbox.service` as
root, or a per-user unit under `~/.config/systemd/user/` with lingering enabled
so it starts at boot. Logs are in the journal:
`journalctl -u warmbox.service -f`.

The daemon needs `--backend qemu` on Linux; `warmbox service install` passes it
for you. QEMU normally asks for KVM; on a machine without it (a CI runner, or a
container that cannot pass `/dev/kvm` through) `--accel tcg` falls back to
software emulation, which is roughly twenty times slower but does boot. QEMU's
user-mode networking is not reachable host→guest, so the backend forwards a host
port to each guest's VNC and agent; everything else behaves the same.

**Exposing it.** Everything the daemon serves is plaintext — the dashboard, its
session cookie, the console, the guest API — so it binds `127.0.0.1` and refuses
a network address without a certificate:

```sh
# a tunnel needs neither TLS nor an open port
ssh -L 7070:127.0.0.1:7070 <user>@<host>

# or run it on the network, with a certificate (the service remembers it)
warmbox service install --pool 1 \
  --tls-cert /etc/letsencrypt/live/<host>/fullchain.pem \
  --tls-key  /etc/letsencrypt/live/<host>/privkey.pem

# or accept the risk knowingly
warmbox service install --pool 1 --addr 0.0.0.0:7070 --insecure
```

**Warm VMs hold RAM** even when nobody is creating anything, so the pool drains
itself after 15 minutes without a lease (`--pool-idle-timeout 30m`; `0` keeps it
warm forever) and the next create pays a cold boot instead.

**Where things live** (under `~/.warmbox/` unless you pass `--workdir`):

```
images/<name>/      installed guest images, each with its meta.json
volumes/            local volume disks, when not backed by a bucket
vms/<id>/            per-VM state: console log, pid, control socket
novnc/              the dashboard's VNC client assets
daemon.log          the service's output
```

**Updating.** The binary and the guest images move independently — the guest
changes far less often, and upgrading the CLI does not touch an image.

```sh
# the binary
curl -fsSL https://raw.githubusercontent.com/Daviduche03/warmbox/master/install.sh | sh

# the guest image for this platform (or the Images page in the dashboard)
warmbox setup                      # installs the default image
warmbox image pull <name>          # or a specific one

warmbox service restart            # picks up the new binary
```

The **Settings → Daemon** tab tells you when either half is behind: it compares
the running version against this repository's releases, and the installed image
against the published one.

## What you need, and what runs where

**macOS 13+ on Apple Silicon** — `brew install vfkit`; or **Linux with KVM** —
`qemu-system-x86_64` (or `-aarch64`) plus `/dev/kvm`. `warmbox setup` installs
the hypervisor for whichever host it finds, so the table below is about what can
run, not what you must install by hand.

| host | overlay images (`xfce`, `lxqt`, `headless`) | EFI disk images (`omarchy`) |
|---|---|---|
| macOS 13+, Apple Silicon (vfkit) | ✅ | ✅ |
| Linux + KVM, x86_64 (QEMU) | ✅ verified | ❌ no OVMF |
| Linux + KVM, arm64 (QEMU) | ✅ expected, untested | ❌ no OVMF |
| Intel Mac | ❌ no nested virtualisation, and vfkit is ARM64-only | ❌ |
| Windows (WSL2 or native) | ❌ not yet | ❌ |

To **build from source** you also need **Go 1.25+** and **Docker** (for the guest
image). An install from a release needs neither.

## How it works

Two layers, and almost everything follows from them:

- **The image is the operating system, read-only, shared by every VM.** Boots are
  cheap because nothing is copied: on macOS `create` clones the golden image with
  an APFS copy-on-write copy (instant, shared blocks) and the guest boots from
  that. Writes go to a tmpfs or an attached volume, never to the image.
- **The volume is the machine.** It is chunked and content-addressed, so
  identical chunks are stored once, snapshots are manifests rather than copies,
  and a commit only pushes the chunks that changed.

The daemon owns the host side: a REST API, the warm pool, the noVNC bridge, a
per-guest-port proxy, and a default-deny egress policy for guests that want to
reach the network through the host.

```
cmd/warmbox         the CLI (daemon, create, image, volume, snapshot, cloud)
internal/api        REST API, noVNC bridge, guest port proxy, agent proxy
internal/desktop    boots and supervises microVMs (vfkit, QEMU); images, pack
internal/volume     persistent volumes (chunked, content-addressed)
internal/cloudstore the chunk + manifest engine shared by volumes
internal/catalog    SQLite metadata: volumes, desktops, snapshots, leases
internal/egress     default-deny egress policy and forward proxy
internal/vnc        websocket → TCP VNC bridge
internal/imagecfg   the guest-image registry (deploy/images/*.yaml)
internal/release    where releases and guest images are published
deploy/images       what images exist, and what each one is
deploy/guest        the overlay engine: Dockerfile, init, apps, guest agent
deploy/omarchy      the EFI engine: Omarchy image build + provisioning
```

## How finished is it

Solid and used daily: the XFCE/LXQt desktops over noVNC, the warm pool,
accounts and workspaces, volumes with snapshots and clones, image build/pack/
pull, and the agent's `exec` and file APIs. Experimental: `warmbox-app` and the
`/p/` port proxy. Not built yet: TTY and screenshot/input on the agent API, EFI
images anywhere but macOS, memory snapshots, and GPU or audio.

## Limitations

- **Plaintext unless you say otherwise, and the guest is root.** The API is not
  open — accounts and workspaces gate every stateful route, and everything that
  reaches a desktop (console streams, the agent API, published guest ports) is
  scoped to the caller's workspace, with warm-pool VMs kept out of every listing.
  But the daemon speaks plain HTTP unless given `--tls-cert`/`--tls-key`, so it
  binds loopback by default and **refuses** a network address without TLS (or an
  explicit `--insecure`). Each guest's VNC server requires a per-VM password,
  generated inside the guest and handed to noVNC with the console URL — the guest
  still sits behind the hypervisor's NAT, so that port was never on the LAN, but
  anything else on the host could previously reach it with no credentials at all.
  What remains is the guest itself: it runs as **root**, and none of this is
  hardened against someone who already has the desktop.
- **EFI disk images are macOS-only.** Omarchy is an EFI disk and the QEMU backend
  ships no OVMF firmware, so it cannot boot there. Overlay images need no firmware
  and take the same path on both backends. Omarchy is also arm64, so it would not
  run on an x86_64 server even with firmware.
- **No memory snapshots.** Neither hypervisor exposes VM state save/restore, so
  "restore anywhere in ~100 ms" needs a different backend
  ([`docs/snapshots.md`](docs/snapshots.md)). Pause is not a substitute: it
  freezes the CPUs in place and the memory stays allocated.
- **Guests have no GPU.** virtio-gpu without 3D, so a Wayland desktop composites
  through Mesa `llvmpipe`; the Omarchy image is tuned for it (small output,
  effects off). X11 is much lighter for remote display.
- **Images are big.** A few hundred MB compressed, downloaded in one piece.
  Omarchy is the heavy one: ~8.4 GB on disk, ~3.9 GB packed, and because btrfs
  fragments free space and APFS only preserves large holes, an expanded copy may
  not be as sparse as you would like.
- **Single writer.** A volume attaches to one VM at a time, and the lock is
  in-process — fine for one host, not for a fleet.
- **Commit cost.** Committing hashes the changed extents: fast for a sparse disk,
  but still O(used).

## Building from source

```sh
git clone https://github.com/Daviduche03/warmbox && cd warmbox
go build -o warmbox ./cmd/warmbox

./warmbox image build xfce      # the default image, from deploy/images/xfce.yaml
./warmbox setup --no-image      # hypervisor + noVNC; the image is already built
./warmbox daemon                # foreground, so you can watch it
```

`make build-all` rebuilds the dashboard (`web/`) and the binary that embeds it.
`make test`, `make vet`.

`make smoke IMAGE_NAME=xfce` boots an image with the real daemon and waits for it
to report ready. That is the check the release workflow runs on every image
before publishing it — a guest that never comes up, a hypervisor flag that is
refused, or a readiness gate that never fires is caught there rather than by
whoever installs it next. On a machine without KVM it uses software emulation, so
it works on a runner too.

### Making a release

Push a tag: `git tag v0.3.0 && git push origin v0.3.0`. That builds the binaries
for darwin/arm64 and linux/{amd64,arm64}, publishes the tarballs, `.deb`/`.rpm`
and `checksums.txt`, and updates the Homebrew cask. Releases need a
`homebrew-warmbox` tap repository and a `HOMEBREW_TAP_GITHUB_TOKEN` secret (a PAT
with write access to it, because the default token cannot push to another
repository). Without that secret the workflow warns and publishes without the
cask, so a release can never fail on it.

**Guest images are separate**, on a release tagged `images`, published as
`warmbox-image-<name>-<arch>.tar.zst` plus its `.sha256`. They are deliberately
not tied to a version: the guest changes far less often, and a CLI release should
not need hundreds of MB re-uploaded. Adding an image to the registry does not
publish it — the **guest image** workflow asks the registry what to build
(`warmbox image build --publishable`) and is run by hand, because it is minutes
per image per architecture and must never be able to block a binary release.
Each image is **booted to readiness before it is uploaded**, so a guest that
never comes up fails the workflow rather than reaching whoever installs next.

## Docs

- [`docs/architecture.md`](docs/architecture.md) — the cloud-native design.
- [`docs/volumes.md`](docs/volumes.md) — volumes, sizing, API.
- [`docs/snapshots.md`](docs/snapshots.md) — snapshots and the fast-resume plan.
- [`docs/agent-api.md`](docs/agent-api.md) — the guest agent API and roadmap.
- [`docs/egress.md`](docs/egress.md) — the egress policy, and what it does not do.
- [`docs/oss-positioning.md`](docs/oss-positioning.md) — what could be a shared primitive.
- [`deploy/omarchy/README.md`](deploy/omarchy/README.md) — the Omarchy image.

## Help wanted

- **EFI on Linux** — OVMF support in the QEMU backend, so Omarchy-style disk
  images run on a server too.
- **One pooled image per image** — the warm pool only pre-boots the daemon's
  default image, so any other `--image` cold-boots. The pool needs to know which
  image each warm VM holds.
- **Cloud Hypervisor / Firecracker backend** — leaner boot and real memory
  snapshot/restore.
- **The agent API** — TTY, screenshot and input, so an agent can drive the screen
  as well as the shell ([`docs/agent-api.md`](docs/agent-api.md)).
- **WebRTC streaming** — replace noVNC/RFB for latency and bandwidth.
- **Windows** — a WSL2 guide, or a native WHPX QEMU backend. Unverified; the seam
  is `internal/desktop/backend.go`.
- **Tests** — API and VM-lifecycle integration tests.

See [`CONTRIBUTING.md`](CONTRIBUTING.md).

## License

[Apache-2.0](LICENSE).
