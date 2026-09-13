# Architecture — unify runmesh + warmbox, cloud-native

Status: **decided direction; W1 (catalog) + W2 (fast create/commit) landed.**
Supersedes the "local file + cloud backup" behavior in `docs/volumes.md`.

## North star

One storage/execution core where **the object store is the live data**, the local
disk is a cache, every VM can be restored anywhere in ~100 ms, and the whole
fleet's metadata lives in a database.

## The decision

For "clone my dev box anywhere, apps and all, fast," the foundation is:

- **Cloud-native volumes** — the authoritative volume is an object-store
  manifest + chunks; the local image is a **cache**, not the source of truth.
- **Snapshot/restore** — the real speed lever: checkpoint a running VM (disk +
  memory) and restore it on any host. This is what makes *attach* fast, not the
  storage layer.
- A **block device remains the guest interface** (virtio-blk), but it is backed
  by the cache/stream layer. We keep the block-device semantics because the guest
  uses it as the overlay's writable root (so installed apps persist).

Rejected as the foundation (kept as fallbacks): a pure FUSE/network filesystem
inside the guest (can't be the overlay root, no nice "apps persist"), and a
full custom streaming block layer from day one (too much to get right up front).

## Why this fixes today's problems

| Today | With this design |
|---|---|
| `create` clones an 8 GiB image and full-scans it (~9 s) | `create` writes a **manifest** — instant |
| `commit` scans the whole image (~11 s) | push **dirty chunks** only |
| Fixed 8 GiB size baked into the base image | size is a **parameter**; grow when needed |
| Volume pinned to one host's disk | volume streams from the object store; any host |
| VM must cold-boot (~seconds) | **restore a snapshot** (~100 ms) |
| Volume/VM state in JSON files + memory | **catalog in SQLite** |

## Components

```
cmd/warmbox, cmd/runmesh        two CLIs, one core
├─ internal/cloudstore          rclone/S3 + content-addressed chunks + manifest + cache
│     used by: volume, sync (runmesh), fuse/cloudfs
├─ internal/volume              a volume == a cloudstore namespace (create=manifest)
├─ internal/snapshot            save/restore VM state (disk + memory)          [next]
├─ internal/catalog             SQLite: volumes, desktops, snapshots, leases
├─ internal/desktop             boot/attach (unchanged interface; volume path points at cache)
└─ internal/api                 REST + noVNC bridge
```

**Bytes vs metadata split (important):** the object store holds *bytes* (chunks)
and a small per-volume *manifest*; SQLite holds the *catalog* (what exists, who
owns it, leases). Never put bytes in SQLite.

## Data model

- `cloudstore://<remote>/volumes/<name>/manifest.json` — chunk index (authoritative bytes).
- SQLite `volumes(name, size, chunk_size, remote, from, created_at, updated_at)`
- SQLite `snapshots(id, volume, kind, size, created_at)`
- SQLite `desktops(id, volume, state, guest_ip, pid, created_at, updated_at)`
- SQLite `leases(volume PK, owner, acquired_at)` — single-writer

SQLite is **per node**. A fleet adds a shared control plane later (Postgres /
etcd) or object-store locks; the `catalog` package hides that behind an interface.

## Lifecycle

- **create volume** — write manifest + catalog row. O(1).
- **attach** — take a lease; start a cache; boot the VM with the cache as
  `/dev/vdb`. Reads fill the cache lazily; writes mark dirty chunks.
- **stop** — flush dirty chunks; optionally snapshot memory. Release the lease.
- **restore anywhere** — on another host, read the catalog + manifest, pull the
  snapshot/chunks lazily, resume.
- **clone/fork** — copy the manifest (and snapshot ref); chunks are shared until
  written (content addressing gives cheap forks).

## Scale model

- **Node-local:** SQLite catalog + a bounded chunk cache on local NVMe.
- **Fleet:** shared catalog (Postgres/etcd) for placement + leases; a scheduler
  places a volume where it is cached or can stream; snapshots make cold placement
  fast.
- Eviction: LRU chunk cache with a size budget; snapshots and hot volumes pinned.

## Build order

1. **`internal/catalog`** — SQLite store for volumes/desktops/leases; wired into
   the daemon and API. **✅ landed.**
2. **Sparse-aware commit + manifest-first create.** Commit reads only allocated
   extents; create clones a manifest instead of scanning, so creating a volume
   from an 8 GiB base went ~8.9 s → **~0.1 s** (first create pays ~0.6 s to build
   the base manifest once). **✅ landed.**
3. **`internal/cloudstore`** — shared content-addressed chunk engine + JSON
   docs. **Volume migrated ✅.** Note: `internal/sync` (runmesh projects) is
   *file-level* — it copies arbitrary files, not disk chunks — and `cloudfs` is a
   general object filesystem, so neither needs the chunk engine; the shared piece
   they do use (building an rclone `fs.Fs` from config) already lives in
   `internal/config`. So "one engine" means cloudstore for chunked disks, config
   for remote construction.
4. **Lazy volume reads** — a shared local chunk cache shipped (identical chunks
   downloaded once per host, and re-attach reuses it). True on-demand reads for a
   multi-GB used set would need a FUSE-served image on the host (**macFUSE
   dependency**) or a streaming block proxy; deferred. `EnsureLocal` currently
   pulls every referenced chunk before boot.
5. **`internal/snapshot` — disk snapshots ✅ landed.** `warmbox snapshot
   create|list|rm` and `volume create --from-snapshot`; a snapshot is a frozen
   manifest, so create/restore-from is O(1) and shares all chunks. Memory
   checkpointing ("~100 ms anywhere") is blocked on macOS — see `docs/snapshots.md`.
6. **Size as a parameter — grow-only ✅ landed.** `--size` is honored; a grow
   resizes the image on the host (native `e2fsprogs`, or a Docker helper on
   macOS) and rebuilds the manifest. Shrinking is refused; the floor is
   `VOLUME_BASE_SIZE` (default 2G).

## Non-goals (for now)

- A multi-region scheduler.
- Putting bytes in SQLite.
- Making SQLite itself distributed.
- GPU.

## Open questions

- Snapshot granularity: full VM memory vs. disk-only + fast boot.
- Whether the live feed to `/dev/vdb` is a **streaming block proxy** or a
  **user-space filesystem** feeding a loop/NBD device; the manifest layer is the
  same either way.
- How `runmesh`'s project sync and a volume's chunk space share content
  addressing (dedup across both would be a big win).
