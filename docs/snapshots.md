# Snapshots & fast resume — feasibility and plan

Goal: restore a running sandbox **anywhere in ~100 ms**. Status: **S1 (disk
snapshots) landed; pause/resume (in-place freeze) landed; S3 (memory
checkpoints on QEMU/KVM) landed; macOS checkpointing still impossible.**

## What a snapshot must contain

A running Linux sandbox is (a) its **disk** and (b) its **memory/CPU/device
state**. Fast restore needs both; a disk-only snapshot still pays a full boot.

| Approach | Contains | Restore time | Portable? |
|---|---|---|---|
| Disk snapshot (manifest) | disk | full boot (~2–3 s) | yes |
| In-process pause/resume | disk + memory (live host RAM) | ~ms | **no** (same host/process) |
| True checkpoint (save to disk) | disk + memory | **~1 s measured** | yes |

## Backend reality check

- **vfkit / Apple Virtualization.framework (macOS):** the public API exposes
  start/stop and `pause()`/`resume()`, but **no serializable VM state
  save/restore**. There is no `--save`/`--restore` on the command line (v0.6.4);
  pause and resume are reachable through vfkit's REST API (`--restful-uri`,
  `POST /vm/state`), which is what warmbox drives. So a portable ~100 ms
  checkpoint is **not possible on macOS today**. `pause()` only freezes in the
  host process — instant resume, same host, RAM held.
- **QEMU (Linux, the backend warmbox already ships):** does real memory
  checkpointing with no new backend needed — QMP `migrate` streams live
  RAM+device state to a file, `-incoming` restores it. Measured on KVM
  (2026-10-06, headless 768 MB guest): save 4.0 s → 180 MB file (zero pages
  skipped), kill → relaunch → QEMU running in 1.0 s, guest agent answering on
  first check, guest uptime continuing through death and resurrection. The
  qemu backend advertises `Snapshot: true`; vfkit stays `false`.
- **Cloud Hypervisor / Firecracker (Linux):** also support real
  snapshot/restore of memory + disk. Still the leaner-boot option from Help
  wanted, but no longer the blocker for checkpoints — QEMU got there first.
- **libkrun:** no snapshot/restore.

So item 4 splits by host: **impossible on Apple Silicon**, shipped on QEMU/KVM.

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

- **S1 — disk snapshots + catalog.** **✅ landed:** `warmbox snapshot
  create|list|rm`, `volume create --from-snapshot`, catalog `snapshots` table.
  A snapshot is a frozen, content-addressed manifest, so create/restore is O(1)
  and shares every chunk. Portable, but restore still boots (~2–3 s).
- **S2 — paused warm pool (macOS).** **Pause/Resume landed:** both backends
  freeze in place (vfkit via its REST API, QEMU via its monitor), wired through
  `POST /api/desktops/{id}/pause|resume` and the dashboard's row menu. Still
  open: a pool of *paused* sandboxes for instant same-host acquire, and the
  capacity changes that go with it.
- **S3 — memory checkpoints on QEMU/KVM.** **Landed:** no new backend needed.
  `POST /api/desktops/{id}/checkpoint` live-snapshots RAM+device state to
  `snapshots/<id>/<name>.mem` (0600 — it holds live guest secrets) via QMP
  migrate-to-file; `restore` relaunches the identical machine with `-incoming`
  (same id, workspace, volume, VNC password; guest uptime continues);
  `hibernate` checkpoints and stops the process (no RAM, no CPU);
  `wake` restores it. CLI mirrors all four plus `checkpoints` list/rm.
  Crash-consistent, not application-consistent: attached volumes keep
  mutating under the guest, so restore is power-loss recovery for disk state.
  A restart is a clean slate (running VMs are reaped, checkpoints cleared) —
  hibernated desktops do not survive it, same rule as running ones.

## Bottom line

Memory checkpointing is **shipped on QEMU/KVM and impossible on Apple
Virtualization**, so the "~100 ms restore anywhere" line stays host-split: on
Linux a restore runs in about a second with the session intact; on a Mac the
fast paths remain the warm pool (booted, ~1 s to hand out) and in-process
pause. Don't promise checkpoints on macOS.
