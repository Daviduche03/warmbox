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

1. **`internal/catalog`** — SQLite store for volumes/desktops/leases; wire the
   daemon and API onto it. **✅ landed.**
2. **Sparse-aware commit** — `commit` reads only allocated extents, so `create`
   on an 8 GiB base went from ~8.9 s to ~0.6 s. **✅ landed.** (Manifest-first
   create, to make it truly O(1), is still open.)
3. **`internal/cloudstore`** — extract the shared chunk/manifest/cache engine;
   put `volume`, `sync`, `cloudfs` on it.
4. **Lazy volume reads** — chunk cache backed by the manifest (cut local disk,
   speed attach).
5. **`internal/snapshot`** — VM checkpoint/restore (the speed lever).
6. **Size as a parameter** — grow (truncate + `resize2fs`); honor `--size`.

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
