# Contributing to warmbox

Thanks for taking a look. warmbox is in **beta**; expect rough
edges, and please open an issue before large changes so we can agree on the
shape.

## Ground rules

- **Be kind.** This project follows the spirit of the
  [Contributor Covenant](https://www.contributor-covenant.org/version/2/1/code_of_conduct/).
- **Small, focused PRs.** One change per PR; describe the "why".
- **Keep `go build ./...`, `go vet ./...`, and `go test ./...` green.**

## Dev setup

```sh
# host deps: Apple Silicon Mac, Docker, Go 1.25+, vfkit (brew install vfkit)
./deploy/guest/build.sh        # build the guest image (slow first time)
go build -o warmbox ./cmd/warmbox
./warmbox setup                # fetch noVNC
go test ./...                  # unit tests (no VM needed)
```

For a full end-to-end check: `./warmbox daemon`, then `./warmbox create --volume dev`.

## Where things live

- `internal/desktop` — boots/attaches microVMs via `vfkit`.
- `internal/volume` + `internal/cloudstore` — chunked, content-addressed disks.
- `internal/catalog` — SQLite metadata (volumes, desktops, snapshots, leases).
- `internal/api` — REST API, noVNC bridge, guest port proxy.
- `deploy/guest` — the guest image: `Dockerfile`, `init`, `overlay-init`, `apps`.

`docs/architecture.md` is the map; read that first.

## Testing

Unit tests cover the storage/catalog layers. VM/API integration tests are
**missing** and very welcome. If you add a test that needs a real VM, gate it
behind an env var so CI (which has no hypervisor) can skip it.

## High-leverage areas (help wanted)

1. **A Cloud Hypervisor / Firecracker Linux backend** (QEMU/KVM already works) —
   for leaner boot and real memory snapshot/restore. The `Backend` interface in
   `internal/desktop/backend.go` is the seam; see `docs/architecture.md`.
2. **Exec / agent API** — phase 1 (`exec` + `files`) works. Next: **persistent
   sessions** (state that survives between calls), **background + streaming
   exec**, `tty`/shell/base64 options, then an SDK. Roadmap in
   `docs/agent-api.md`.
3. **WebRTC streaming** — replace noVNC/RFB for latency and bandwidth.
4. **macOS VNC bridge** — replace the `/usr/bin/nc` fallback with a signed helper
   or a configurable bridge.
5. **Windows** — a WSL2 setup guide, and/or a native QEMU **WHPX** backend
   (`-accel whpx`; the current QEMU backend hardcodes KVM). Untested; see
   `internal/desktop/backend.go`.
6. **Tests** — API and lifecycle integration tests.

## Platform notes

The guest is always Linux; portability work is on the **host** side
(hypervisor + networking). Keep host-specific code isolated so a new backend is
additive.

## License

By contributing you agree your contributions are licensed under
[Apache-2.0](LICENSE).
