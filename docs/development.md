# Development

## Building from source

You need Go 1.25+ and Docker (for the guest image). An install from a release
needs neither.

```sh
git clone https://github.com/Daviduche03/warmbox && cd warmbox
go build -o warmbox ./cmd/warmbox

./warmbox image build xfce      # the default image, from deploy/images/xfce.yaml
./warmbox setup --no-image      # hypervisor + noVNC; the image is already built
./warmbox daemon                # foreground, so you can watch it
```

| Command | What it does |
|---|---|
| `make build-all` | Rebuilds the dashboard (`web/`) and the binary that embeds it |
| `make test` | Runs the tests |
| `make vet` | Runs `go vet` |
| `make smoke IMAGE_NAME=xfce` | Boots an image with the real daemon and waits for it to report ready |

The smoke test is the check the release workflow runs on every image before
publishing it. It catches a guest that never comes up, a hypervisor flag that is
refused, or a readiness gate that never fires. On a machine without KVM it uses
software emulation, so it works on a runner too.

## How it works

There are two layers, and almost everything follows from them.

The image is the operating system. It is read-only and shared by every VM. Boots
are cheap because nothing is copied. On macOS `create` clones the golden image
with an APFS copy-on-write copy, which is instant and shares blocks, and the
guest boots from that. Writes go to a tmpfs or an attached volume, never to the
image.

The volume is the machine. It is chunked and content-addressed, so identical
chunks are stored once, snapshots are manifests and not copies, and a commit
only pushes the chunks that changed.

The daemon owns the host side: a REST API, the warm pool, the noVNC bridge, a
per-guest-port proxy, and a default-deny egress policy for guests that want to
reach the network through the host. It boots microVMs with vfkit on macOS and
QEMU on Linux.

## Code layout

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

## Making a release

Push a tag:

```sh
git tag v0.3.0 && git push origin v0.3.0
```

That builds the binaries for darwin/arm64 and linux/{amd64,arm64}, publishes the
tarballs, `.deb`/`.rpm` and `checksums.txt`, and updates the Homebrew cask.

Releases need a `homebrew-warmbox` tap repository and a
`HOMEBREW_TAP_GITHUB_TOKEN` secret. The secret is a PAT with write access to the
tap, because the default token cannot push to another repository. Without it the
workflow warns and publishes without the cask, so a release can never fail on
it.

## Guest image releases

Guest images are separate. They live on a release tagged `images`, published as
`warmbox-image-<name>-<arch>.tar.zst` plus its `.sha256`.

They are deliberately not tied to a version. The guest changes far less often,
and a CLI release should not need hundreds of MB re-uploaded.

Adding an image to the registry does not publish it. The **guest image**
workflow asks the registry what to build (`warmbox image build --publishable`)
and is run by hand, because it takes minutes per image per architecture and must
never be able to block a binary release.

Each image is booted to readiness before it is uploaded, so a guest that never
comes up fails the workflow.

## Help wanted

- **EFI on Linux**: OVMF support in the QEMU backend, so Omarchy-style disk
  images run on a server too.
- **One pooled image per image**: the warm pool only pre-boots the daemon's
  default image, so any other `--image` cold-boots. The pool needs to know which
  image each warm VM holds.
- **Cloud Hypervisor / Firecracker backend**: leaner boot and memory
  snapshot/restore.
- **The agent API**: TTY, screenshot and input, so an agent can drive the screen
  as well as the shell ([agent-api.md](agent-api.md)).
- **WebRTC streaming**: replace noVNC/RFB for latency and bandwidth.
- **Windows**: a WSL2 guide, or a native WHPX QEMU backend. Unverified. The seam
  is `internal/desktop/backend.go`.
- **Tests**: API and VM-lifecycle integration.
