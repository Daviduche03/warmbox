# Volumes — portable, cloud-backed disks for warmbox VMs

Status: **design + first milestone in progress.**

## The idea in one line

The computer is disposable; the hard drive lives in the cloud. Each VM gets a
"volume" stored in R2 (through runmesh/rclone). Start a VM on any host, plug in
the volume, and it is the same machine — same files, same installed apps, same
settings. Stop it and the volume goes back to the cloud.

## Why

Today a warmbox VM boots from a read-only squashfs base plus a **tmpfs overlay**:
writes live in RAM and vanish when the VM stops. That is great for boot speed and
RAM, but it means nothing persists and every VM starts from zero.

A volume replaces the tmpfs upper layer with a real, persistent disk. Because the
overlay's writable layer lives on the volume, *everything* survives: project
files, `apk add`, browser profiles, config. A new VM anywhere can attach the same
volume, so a dev box can be cloned/recreated on any host.

## Concepts

- **Volume** — a raw ext4 disk image, addressed by name. Stored in R2 as
  fixed-size chunks (so only changed chunks move, not the whole disk).
- **Volume store** — host-side agent (built on runmesh/rclone) that pulls chunks
  down before a VM boots and pushes changed chunks back after.
- **Attach** — a volume may be attached to at most one running VM at a time
  (single-writer, like a cloud block device).
- **Commit** — push changed chunks to R2. First milestone: on stop. Later:
  periodically while running.
- **Clone / snapshot** — copy a volume (cheap server-side chunk copy) to seed a
  new one without touching the original.

## Storage layout (remote)

```
<prefix>/<name>/meta.json        # name, size, chunk size, created, from
<prefix>/<name>/manifest.json    # chunk index -> sha256 (which chunks exist)
<prefix>/<name>/chunks/<index>   # raw chunk bytes (only non-zero chunks)
```

Local working copy: `<volume-dir>/<name>/disk.img` (sparse).

## Boot flow

```text
        R2 bucket (source of truth)
              ^    |
   pull chunks|    | push changed chunks (commit)
              |    v
   host:  <volume-dir>/<name>/disk.img   (sparse ext4)
              |
              | virtio-blk (rw)  -> guest /dev/vdb
              v
   guest: read-only squashfs base (/dev/vda)
          + overlay upper/work on /dev/vdb
          = the whole machine persists
```

`deploy/guest/overlay-init` gets one new branch: if `/dev/vdb` is present and
mountable, use it for the overlay upper/work; otherwise keep today's tmpfs
behaviour. The OS base stays read-only and shared, so boots stay fast and cheap.

## CLI

```text
warmbox daemon                     Run the orchestrator (warm pool + REST API)
warmbox setup                      Check host prerequisites, fetch noVNC
warmbox create [--volume <name>]   Provision a desktop, print its noVNC URL
warmbox list                       List desktops
warmbox destroy <id>               Destroy a desktop

warmbox volume create <name> [--size 8G] [--from <name>]
warmbox volume list
warmbox volume clone <name> <new>
warmbox volume snapshot <name> [--as <snap>]
warmbox volume rm <name>
```

Daemon flags for volumes:

```
--volume-dir     local chunk cache             (default <workdir>/volumes)
--volume-base    base ext4 image to seed new   (default <workdir>/volume-base.img)
--volume-chunk   chunk size in MiB             (default 16)
--volume-prefix  remote prefix for volumes     (default "volumes")
--volume-flush   periodic commit interval      (M2: default 0 = only on stop)
```

Example:

```sh
runmesh config set ...                 # point storage at your bucket (once)
warmbox volume create dev --size 8G
warmbox create --volume dev            # prints the noVNC URL

# inside the VM: install tools, edit files — it all lands in the volume

warmbox create --volume dev            # later / on another host: same machine
warmbox volume clone dev dev-clean     # template a fresh box
```

## Sizing

Volumes are **grow-only**. A volume is an ext4 disk with a fixed size, so
changing size means growing the disk file and then telling ext4 to stretch
(`e2fsck -f` + `resize2fs`).

- The image is created at the requested size: `warmbox volume create dev --size 64G`.
- The base disk built by `build.sh` sets the **floor**: `VOLUME_BASE_SIZE`
  (default `2G`). Requesting smaller than the floor is refused (shrinking ext4
  is unsafe).
- Growth happens on the **host at create time**: natively where `resize2fs`
  exists (Linux), or in a throwaway Docker container on macOS (Docker is already
  required to build the guest image). The guest just mounts an already-correct
  filesystem.
- The manifest's chunks are unaffected by growing — the extra space is holes
  until written, and the resize's new metadata is captured in the manifest at
  create time.
- Same-size creates stay O(1) (manifest clone); only a *grow* does the resize.

## REST API

```
POST   /api/volumes                 {name, size?, from?}
GET    /api/volumes
GET    /api/volumes/{name}
DELETE /api/volumes/{name}
POST   /api/volumes/{name}/clone    {name: "dev-2"}
POST   /api/volumes/{name}/snapshot {as: "snap-1"}
POST   /api/desktops                {volume: "dev"}   # existing endpoint, new field
```

## Lifecycle

1. `create` — clone the base image (or an existing volume) locally, write meta.
2. `attach` (VM create with `--volume`) — ensure the local image matches the
   remote (pull missing chunks), take the single-attach lock.
3. `run` — the guest writes to `/dev/vdb`; optionally commit every N seconds.
4. `stop` — final commit (push changed chunks), release the lock.
5. `clone` — server-side copy of chunks + manifest under a new name.
6. `snapshot` — freeze the current manifest as an immutable version.

## Consistency and failure

- ext4 journaling keeps the disk consistent across unclean shutdowns; the next
  mount replays the journal.
- A volume-backed guest runs `sync` every 2s, so a host-side commit after an
  unclean stop loses at most a couple of seconds. (Verified: a file written in
  one VM survives destroy + re-create.)
- Single-attach is enforced in-process for milestone 1; a distributed lock in R2
  is a later step so multiple daemons can share volumes safely.
- Commit currently hashes the whole image, so a commit takes a few seconds for a
  multi-GB volume. M4 replaces this with write tracking.

## Milestones

- **M1 (this change)** — volume store (create/list/get/delete/clone +
  pull/commit of changed chunks), attach as `/dev/vdb`, overlay uses it,
  `warmbox volume ...`, `create --volume`, commit on stop, single attach.
- **M2** — periodic flush; sparse-aware change detection; golden seed volume
  baked at image-build time.
- **M3** — snapshot/clone UX, R2 lock, warm-pool awareness, multi-host.
- **M4** — cheaper change tracking (write-tracking block layer) instead of
  hashing the whole image on commit.

## Open decisions

- Default volume size and whether it can grow in place.
- Fresh volume contents: empty vs. a golden dev image (tools preinstalled).
- Warm pool: volume-backed VMs can't be pre-booted with a specific volume, so
  they boot on demand (accept the attach cost) or we pre-warm per known volume.
- Whether `.git` and caches live on the volume or stay ephemeral.
