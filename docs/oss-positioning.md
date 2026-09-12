# warmbox — OSS positioning charter

Status: draft for discussion. Goal: decide what warmbox open-sources *as a
primitive*, so other people can build on it without adopting our whole app.

## The problem

Every team building agents, computer-use, or remote dev environments is
hand-rolling the same sandbox plumbing: boot an isolated machine, get a
filesystem in and out, stream a screen, drive input, snapshot it, throw it away.
There is no vendor-neutral, self-hostable primitive for this. The result is
lock-in to one cloud, or a bespoke pile of QEMU/Firecracker/Docker glue per team.

## What warmbox is today (an app)

A single binary with one opinionated stack baked in:

- `internal/desktop/manager.go` shells out to **vfkit** directly.
- `internal/vnc/websockify.go` *is* the streaming transport (noVNC/RFB).
- `internal/config` + `internal/sync` are welded to **rclone/S3**.
- `cmd/warmbox/main.go` wires all of it together.

You can use it end-to-end, or fork it. You cannot swap the hypervisor, the
transport, the storage, or embed just the scheduler. That is the difference
between an **app** and a **primitive**.

## What OSS actually adopts

Primitives, interfaces, and specs — the things you drop into your own project:

- **OCI / containerd** — a spec plus a runtime others embed.
- **Firecracker / libkrun** — a small hypervisor as a library.
- **Tart / Lima** — a single-VM primitive, reused by others.
- **gVisor / Kata** — an isolation primitive.
- **WebRTC** — a transport standard.

Communities form around the seam everyone keeps rebuilding, not around a
finished product.

## The wedge

**An open agent-sandbox runtime + protocol**: one daemon and one spec for
`create / snapshot / fork / exec / files / screen / input`, with **pluggable
backends** (hypervisors) and **pluggable transports** (screen/input).

- Reference implementation: this repo.
- First backend: vfkit (macOS/Apple Silicon). Second: Cloud Hypervisor or QEMU (Linux/x86_64).
- First transport: VNC/noVNC. Then WebRTC.
- Storage: the portable volume store (see `docs/volumes.md`).

One sentence: *"containerd for agent sandboxes."*

## The interfaces to expose (the contribution surface)

These are the extension points that let people help without forking.

```go
// Backend boots and controls isolated machines. One per hypervisor.
type Backend interface {
    Start(ctx context.Context, spec SandboxSpec) (*Machine, error)
    Stop(ctx context.Context, m *Machine) error
    Snapshot(ctx context.Context, m *Machine) (SnapshotID, error) // M2+
    Fork(ctx context.Context, snap SnapshotID) (*Machine, error)  // M3+
    Capabilities() Caps
}

// Transport streams a screen and carries input. VNC today, WebRTC later.
type Transport interface {
    Serve(ctx context.Context, m *Machine) (endpoint string, err error)
    Input(ctx context.Context, ev Event) error
}

// VolumeStore persists a machine's writable layer. Local FS today, S3/R2 via
// rclone; content-addressed chunks with cheap fork.
type VolumeStore interface {
    Create(ctx context.Context, name string, size int64, from string) (*Volume, error)
    Attach(ctx context.Context, name string) (imagePath string, release func(), err error)
    Commit(ctx context.Context, name string) error
    Fork(ctx context.Context, src, dst string) (*Volume, error)
}
```

Plus a **wire protocol** (gRPC + a small HTTP/JSON surface) and a **client SDK**
(Go first, then Python/TypeScript) so non-Go users can drive sandboxes.

## Scope (v1)

- Lifecycle: create/start/stop/destroy sandboxes.
- Guest images as **OCI artifacts** (pull-only to start; push later).
- Filesystem: mount a host dir (virtiofs) and/or attach a portable volume.
- Screen + input: VNC for humans; a screenshot/input API for agents.
- Exec + file API for tool-use agents.
- Snapshots/forks (the flagship hard problem).

## Non-goals

- A multi-region control plane for millions of sandboxes (that is a product).
- A GPU scheduler.
- A full desktop environment — that is one *image*, not the runtime.
- A cloud. warmbox should work against your own bucket and your own hardware.

## Milestones

- **M0** — extract the `Backend` and `Transport` interfaces from today's app;
  no behavior change; vfkit + VNC as the only impls. *(Refactor, low risk.)*
- **M1** — publish the protocol + a Go/Python SDK; sandbox API (`create`,
  `exec`, `files`, `screenshot`, `input`).
- **M2** — snapshot/fork for the volume store; fast fork of a running sandbox.
- **M3** — WebRTC transport (the biggest quality gap vs. the field).
- **M4** — second backend (Cloud Hypervisor/QEMU) so it runs on Linux/x86_64.

## Why people would contribute

- **Day-one value on their own machine**: `warmbox sandbox create` gives them a
  GUI agent sandbox on their Mac in one command.
- **Clear plugin seams**: a backend or transport is a small, self-contained PR.
- **A real hard problem**: fast snapshot/fork of microVMs — unsolved in the open
  on Apple Silicon — is the kind of thing that attracts serious contributors.
- **Vendor-neutral**: works with their bucket, their hardware, no signup.

## Landscape (and how this differs)

| Project | Open? | Shape | Difference |
|---|---|---|---|
| E2B | open core | cloud sandboxes, snapshots | cloud-tied, x86 |
| Daytona | OSS | dev sandboxes | x86, not GUI/streaming |
| Tart + Orchard | partly | Apple-Silicon VMs | single-VM / acquired path; no agent protocol |
| Lima / UTM | OSS | dev VMs | not an orchestrator/API |
| Kasm / Selkies / Neko | OSS | streamed desktops | containers, not microVM-native |
| gVisor / Kata | OSS | isolation | no sandbox lifecycle/streaming |
| Firecracker / libkrun | OSS | hypervisor | a component, not the seam |

The empty seam: **an open, Apple-Silicon-capable, agent-native sandbox runtime
with a portable, forkable filesystem.**

## Licensing & governance (decide early)

- **Code**: Apache-2.0 (patent grant, business-friendly) or MIT.
- **Protocol/spec**: a permissive/spec license (e.g. CC0 or Apache) so anyone can
  implement it; keep the spec in a neutral repo separate from the reference impl.
- **Governance**: open issues/RFCs; keep the reference impl the first, not the
  only, implementation.

## Day-one onboarding (needs to be a single command)

```sh
# on an Apple Silicon Mac
warmbox sandbox create --image warmbox/desktop:latest --expose screen,input
# prints a URL and a sandbox id; agents use the SDK, humans use the URL
```

## Open questions

- Is the primitive the **sandbox runtime** (this doc) or the **forkable volume**
  (a separate, storage-only project)? Likely both, with the volume as a dep.
- Pixels (computer-use, human-in-loop) vs. headless browser + CDP (tool-use
  agents): pick one as the primary story or the project fragments.
- How much to standardize vs. ship: a spec nobody implements is worthless; ship
  the reference impl first, spec the parts others actually replace.
