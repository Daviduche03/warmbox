# Snapshots & fast resume — feasibility and plan

Goal: restore a running sandbox **anywhere in ~100 ms**. Status: **not started;
one hard blocker on macOS.**

## What a snapshot must contain

A running Linux sandbox is (a) its **disk** and (b) its **memory/CPU/device
state**. Fast restore needs both; a disk-only snapshot still pays a full boot.

| Approach | Contains | Restore time | Portable? |
|---|---|---|---|
| Disk snapshot (manifest) | disk | full boot (~2–3 s) | yes |
| In-process pause/resume | disk + memory (live host RAM) | ~ms | **no** (same host/process) |
| True checkpoint (save to disk) | disk + memory | ~100 ms | yes |

## Backend reality check

- **vfkit / Apple Virtualization.framework (macOS):** the public API exposes
  start/stop and `pause()`/`resume()`, but **no serializable VM state
  save/restore**. vfkit v0.6.4 has no snapshot/pause/save options (`--help`
  confirms). So a portable ~100 ms checkpoint is **not possible on macOS today**.
  `pause()` only freezes in the host process — instant resume, same host, RAM held.
- **Cloud Hypervisor / Firecracker / QEMU (Linux):** support real
  snapshot/restore of memory + disk. This is the path to portable ~100 ms restore.
  Firecracker (E2B's substrate) is the proof it works.
- **libkrun:** no snapshot/restore.

So item 4 splits by host: **impossible on Apple Silicon**, very doable on a
Linux backend.

## What we can ship now (no blocker)

1. **Disk snapshots (portable, O(1)).** A snapshot is a frozen manifest. We
   already have the machinery: `volume.Clone` copies a manifest in ~0.1 s (chunks
   are content-addressed and shared). Add a `snapshots` row in the catalog
   (`id, volume, chunk_size, size, created_at`) and a `CreateSnapshot` that copies
   the manifest. Restore = create a volume from the snapshot manifest, then boot.
   Fast to create, portable, but restore still boots (~2–3 s).
2. **Paused warm pool (instant, same host).** On macOS, keep a pool of VMs that
   are booted and then `pause()`d. Acquire = `resume()` (~ms). This is the
   nearest thing to instant on a Mac; costs host RAM per paused VM and is not
   portable.
3. **A `Snapshotter` seam in the Backend interface** so a Linux backend can add
   true save/restore later without touching the orchestrator:
   ```go
   type Snapshotter interface {
       Pause(ctx, m *Machine) error          // macOS: freeze in-process
       Resume(ctx, m *Machine) error
       Save(ctx, m *Machine) (SnapshotID, error)   // Linux: checkpoint to disk
       Restore(ctx, id SnapshotID) (*Machine, error)
   }
   ```

## Plan

- **S1 — disk snapshots + catalog.** `internal/snapshot` metadata, `volume.Clone`
  as the mechanism, `warmbox snapshot create|list|rm`, `create --from-snapshot`.
  Portable, cheap, boot-latency restore. *(no blockers; do first.)*
- **S2 — paused warm pool (macOS).** Add `Pause/Resume` to the vfkit backend and a
  pool of paused sandboxes. Instant same-host acquire.
- **S3 — Linux backend with true checkpoint.** Cloud Hypervisor (or Firecracker)
  behind the `Backend` interface, using its snapshot API for portable ~100 ms
  restore. This is where the headline number comes from.

## Bottom line

The "~100 ms restore anywhere" line requires a hypervisor with memory
checkpointing, which **Apple Virtualization does not have**. On a Mac we can be
instant-but-local (paused pool) or portable-but-seconds (disk snapshots). The
real number arrives with the Linux backend in S3. Don't promise ~100 ms on macOS.
