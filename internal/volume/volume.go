// Package volume implements portable, cloud-backed disks for warmbox VMs.
//
// A volume is a raw ext4 image whose allocated bytes live as content-addressed
// chunks in a remote store (via internal/cloudstore), with a local sparse
// working copy. Creating a volume is O(1): it clones a manifest, it does not
// scan or upload. See docs/volumes.md and docs/architecture.md.
package volume

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	rfs "github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/operations"

	"warmbox/internal/cloudstore"
)

// DefaultChunkSize is the transfer granularity when syncing a volume image.
const DefaultChunkSize = cloudstore.DefaultChunkSize

// Meta describes a volume. It is stored at <prefix>/<name>/meta.json.
type Meta struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	ChunkSize int64     `json:"chunk_size"`
	Created   time.Time `json:"created"`
	From      string    `json:"from,omitempty"`
}

// Store manages volumes on a remote filesystem plus a local cache of images.
type Store struct {
	cs        *cloudstore.Store
	prefix    string
	localDir  string
	baseImage string
	chunkSize int64

	// localRemote is set when the remote store is a directory on this host:
	// chunk reads go straight to it and the cacheDir copy is skipped.
	localRemote string

	mu       sync.Mutex
	attached map[string]string // volume name -> owner (VM id)

	applyMu   sync.Mutex
	lastApply cloudstore.ApplyStats

	// gcGrace skips chunks newer than this during garbage collection, so an
	// in-flight commit's not-yet-referenced uploads are never deleted.
	// Zero uses DefaultGCGrace.
	gcGrace time.Duration

	cleanedCache sync.Once
}

// DefaultGCGrace is how young a chunk may be and still be safe from the
// garbage collector. A commit uploads chunks before writing the manifest that
// references them; without a grace window GC could delete an upload that is
// seconds away from being referenced.
const DefaultGCGrace = time.Hour

// NewStore builds a Store. prefix defaults to "volumes"; chunkSize <= 0 uses
// DefaultChunkSize.
func NewStore(f rfs.Fs, prefix, localDir, baseImage string, chunkSize int64) *Store {
	if prefix == "" {
		prefix = "volumes"
	}
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	prefix = strings.Trim(prefix, "/")
	return &Store{
		cs:        cloudstore.New(f, prefix+"/chunks", chunkSize),
		prefix:    prefix,
		localDir:  localDir,
		baseImage: baseImage,
		chunkSize: chunkSize,
		attached:  map[string]string{},
	}
}

// SetLocalRemoteDir tells the store its remote lives at dir on this host's
// disk, so chunk reads go straight through to it instead of being copied into
// the local cache first. dir is the remote root (the "volume-remote" dir),
// not the chunks dir itself.
func (s *Store) SetLocalRemoteDir(dir string) {
	s.localRemote = dir
	s.cs.SetLocalChunksDir(filepath.Join(dir, s.prefix, "chunks"))
}

// SetGCGrace overrides how young a chunk may be and still be safe from GC.
// A negative grace disables the grace window (tests).
func (s *Store) SetGCGrace(d time.Duration) { s.gcGrace = d }

func (s *Store) gcGracePeriod() time.Duration {
	if s.gcGrace < 0 {
		return 0 // tests: no grace
	}
	if s.gcGrace == 0 {
		return DefaultGCGrace
	}
	return s.gcGrace
}

// LastApply reports what the most recent EnsureLocal wrote vs skipped.
func (s *Store) LastApply() cloudstore.ApplyStats {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	return s.lastApply
}

// --- paths ---

func (s *Store) metaRemote(name string) string     { return s.prefix + "/" + name + "/meta.json" }
func (s *Store) manifestRemote(name string) string { return s.prefix + "/" + name + "/manifest.json" }
func (s *Store) baseManifestRemote() string        { return s.prefix + "/_base/manifest.json" }
func (s *Store) dirPath(name string) string        { return filepath.Join(s.localDir, name) }
func (s *Store) cacheDir() string                  { return filepath.Join(s.localDir, "chunks") }

// ImagePath is the local working copy of a volume image.
func (s *Store) ImagePath(name string) string { return filepath.Join(s.dirPath(name), "disk.img") }

// ValidName reports whether name is a safe volume name.
func ValidName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// --- API ---

// Create makes a new volume in O(1): it clones the manifest of a source volume,
// clones the precomputed base-image manifest, or starts empty. No disk scan.
func (s *Store) Create(ctx context.Context, name string, size int64, from string) (*Meta, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("invalid volume name %q", name)
	}
	if _, err := s.Get(ctx, name); err == nil {
		return nil, fmt.Errorf("volume %q already exists", name)
	} else if !errors.Is(err, rfs.ErrorObjectNotFound) {
		return nil, err
	}

	meta := &Meta{Name: name, Created: time.Now().UTC(), From: from}
	if err := os.MkdirAll(s.dirPath(name), 0o755); err != nil {
		return nil, err
	}

	var man *cloudstore.Manifest
	switch {
	case from != "":
		src, err := s.Get(ctx, from)
		if err != nil {
			return nil, fmt.Errorf("source volume: %w", err)
		}
		m, err := s.Manifest(ctx, from)
		if err != nil {
			return nil, fmt.Errorf("source manifest: %w", err)
		}
		if srcImg := s.ImagePath(from); fileExists(srcImg) {
			if err := cloneFile(srcImg, s.ImagePath(name)); err != nil {
				return nil, err
			}
		}
		meta.Size, meta.ChunkSize, meta.From = src.Size, src.ChunkSize, from
		man = m
	case s.baseImage != "" && fileExists(s.baseImage):
		bm, err := s.baseManifest(ctx)
		if err != nil {
			return nil, err
		}
		if err := cloneFile(s.baseImage, s.ImagePath(name)); err != nil {
			return nil, err
		}
		meta.Size, meta.ChunkSize, meta.From = bm.Size, bm.ChunkSize, "_base"
		man = bm
	default:
		if size <= 0 {
			return nil, fmt.Errorf("volume %q: no base image and no size given", name)
		}
		meta.Size, meta.ChunkSize = size, s.chunkSize
		if err := createSparse(s.ImagePath(name), size); err != nil {
			return nil, err
		}
		man = &cloudstore.Manifest{ChunkSize: s.chunkSize, Size: size, Chunks: map[string]string{}}
	}

	// Honor an explicit size (grow-only). Growing just extends the image file;
	// the guest's boot step runs e2fsck + resize2fs on /dev/vdb before mounting
	// it, so the filesystem catches up on first boot. Shrinking is refused.
	if size > 0 && size < meta.Size {
		return nil, fmt.Errorf("volume %q: cannot shrink %d -> %d bytes (grow-only)", name, meta.Size, size)
	}
	if size > meta.Size {
		img := s.ImagePath(name)
		if !fileExists(img) {
			return nil, fmt.Errorf("volume %q: cannot grow a volume with no local image", name)
		}
		if err := os.Truncate(img, size); err != nil {
			return nil, err
		}
		meta.Size = size
		// Chunks are content-addressed and unchanged; the extra space is holes
		// until the guest grows the fs and writes. The next commit records the
		// new metadata.
	}
	if man.Size != meta.Size {
		man.Size = meta.Size
	}

	if err := s.cs.PutJSON(ctx, s.metaRemote(name), meta); err != nil {
		return nil, err
	}
	if err := s.cs.PutJSON(ctx, s.manifestRemote(name), man); err != nil {
		return nil, err
	}
	// The local image provably matches the manifest in every path above: a
	// clone copies the source bytes, and a fresh sparse image matches the
	// empty manifest. (CreateFromSnapshot is the exception — it leaves a
	// sparse image behind for EnsureLocal — and records no sidecar.)
	// The grow path below only extends with holes, so it stays matched too.
	if fileExists(s.ImagePath(name)) {
		s.recordApplied(name, man)
	}
	return meta, nil
}

// baseManifest returns the base image's manifest, computing and caching it on
// first use (the only place a scan happens, and only once).
func (s *Store) baseManifest(ctx context.Context) (*cloudstore.Manifest, error) {
	var m cloudstore.Manifest
	if err := s.cs.GetJSON(ctx, s.baseManifestRemote(), &m); err == nil && m.Chunks != nil {
		return &m, nil
	}
	f, err := os.Open(s.baseImage)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	m2, err := s.cs.BuildManifest(ctx, f, fi.Size(), s.chunkSize)
	if err != nil {
		return nil, err
	}
	if err := s.cs.PutJSON(ctx, s.baseManifestRemote(), m2); err != nil {
		return nil, err
	}
	return m2, nil
}

// List returns all volumes.
func (s *Store) List(ctx context.Context) ([]*Meta, error) {
	names, err := s.cs.ListDirs(ctx, s.prefix)
	if err != nil {
		return nil, err
	}
	var out []*Meta
	for _, name := range names {
		if name == "_base" {
			continue
		}
		m, err := s.Get(ctx, name)
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get reads a volume's metadata.
func (s *Store) Get(ctx context.Context, name string) (*Meta, error) {
	var m Meta
	if err := s.cs.GetJSON(ctx, s.metaRemote(name), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Manifest returns the current manifest for a volume.
func (s *Store) Manifest(ctx context.Context, name string) (*cloudstore.Manifest, error) {
	m := &cloudstore.Manifest{Chunks: map[string]string{}}
	if err := s.cs.GetJSON(ctx, s.manifestRemote(name), m); err != nil {
		return nil, err
	}
	if m.Chunks == nil {
		m.Chunks = map[string]string{}
	}
	return m, nil
}

// Delete removes a volume's metadata/manifest and local image. Content-addressed
// chunks are shared, so they are left for a garbage collector.
func (s *Store) Delete(ctx context.Context, name string) error {
	_ = s.cs.Delete(ctx, s.manifestRemote(name))
	_ = s.cs.Delete(ctx, s.metaRemote(name))
	if err := operations.Rmdir(ctx, s.cs.Fs(), s.prefix+"/"+name); err != nil {
		// best effort: some backends remove dirs implicitly
		_ = err
	}
	_ = os.RemoveAll(s.dirPath(name))
	return nil
}

// Clone copies src to dst (manifest clone, O(1)).
func (s *Store) Clone(ctx context.Context, src, dst string) (*Meta, error) {
	if !ValidName(dst) {
		return nil, fmt.Errorf("invalid volume name %q", dst)
	}
	if _, err := s.Get(ctx, dst); err == nil {
		return nil, fmt.Errorf("volume %q already exists", dst)
	}
	return s.Create(ctx, dst, 0, src)
}

// --- snapshots (frozen manifests) ---

// Snapshot is a frozen copy of a volume's manifest.
type Snapshot struct {
	ID        string    `json:"id"`
	Volume    string    `json:"volume"`
	Size      int64     `json:"size"`
	ChunkSize int64     `json:"chunk_size"`
	Created   time.Time `json:"created"`
}

func (s *Store) snapshotMetaRemote(id string) string {
	return s.prefix + "/snapshots/" + id + "/meta.json"
}
func (s *Store) snapshotManifestRemote(id string) string {
	return s.prefix + "/snapshots/" + id + "/manifest.json"
}

// NewSnapshotID returns a short random id.
func NewSnapshotID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Snapshot freezes a volume's current manifest under a new id. The chunks are
// already content-addressed, so this is O(1) and shares all data.
func (s *Store) Snapshot(ctx context.Context, volumeName, id string) (*Snapshot, error) {
	if _, err := s.Get(ctx, volumeName); err != nil {
		return nil, err
	}
	man, err := s.Manifest(ctx, volumeName)
	if err != nil {
		return nil, err
	}
	if id == "" {
		id = NewSnapshotID()
	}
	if !ValidName(id) {
		return nil, fmt.Errorf("invalid snapshot id %q", id)
	}
	snap := &Snapshot{ID: id, Volume: volumeName, Size: man.Size, ChunkSize: man.ChunkSize, Created: time.Now().UTC()}
	if err := s.cs.PutJSON(ctx, s.snapshotManifestRemote(id), man); err != nil {
		return nil, err
	}
	if err := s.cs.PutJSON(ctx, s.snapshotMetaRemote(id), snap); err != nil {
		return nil, err
	}
	return snap, nil
}

// GetSnapshot reads a snapshot's metadata.
func (s *Store) GetSnapshot(ctx context.Context, id string) (*Snapshot, error) {
	var sn Snapshot
	if err := s.cs.GetJSON(ctx, s.snapshotMetaRemote(id), &sn); err != nil {
		return nil, err
	}
	return &sn, nil
}

// ListSnapshots returns all snapshots, newest first.
func (s *Store) ListSnapshots(ctx context.Context) ([]*Snapshot, error) {
	ids, err := s.cs.ListDirs(ctx, s.prefix+"/snapshots")
	if err != nil {
		return nil, err
	}
	var out []*Snapshot
	for _, id := range ids {
		sn, err := s.GetSnapshot(ctx, id)
		if err != nil {
			continue
		}
		out = append(out, sn)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// DeleteSnapshot removes a snapshot's metadata/manifest.
func (s *Store) DeleteSnapshot(ctx context.Context, id string) error {
	_ = s.cs.Delete(ctx, s.snapshotManifestRemote(id))
	_ = s.cs.Delete(ctx, s.snapshotMetaRemote(id))
	_ = operations.Rmdir(ctx, s.cs.Fs(), s.prefix+"/snapshots/"+id)
	return nil
}

// CreateFromSnapshot makes a new volume from a snapshot's manifest (O(1)).
func (s *Store) CreateFromSnapshot(ctx context.Context, name, snapID string) (*Meta, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("invalid volume name %q", name)
	}
	if _, err := s.Get(ctx, name); err == nil {
		return nil, fmt.Errorf("volume %q already exists", name)
	}
	snap, err := s.GetSnapshot(ctx, snapID)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	man := &cloudstore.Manifest{Chunks: map[string]string{}}
	if err := s.cs.GetJSON(ctx, s.snapshotManifestRemote(snapID), man); err != nil {
		return nil, err
	}
	if man.Chunks == nil {
		man.Chunks = map[string]string{}
	}
	meta := &Meta{Name: name, Size: snap.Size, ChunkSize: snap.ChunkSize, Created: time.Now().UTC(), From: "snapshot:" + snapID}
	if err := os.MkdirAll(s.dirPath(name), 0o755); err != nil {
		return nil, err
	}
	if err := createSparse(s.ImagePath(name), snap.Size); err != nil {
		return nil, err
	}
	if err := s.cs.PutJSON(ctx, s.metaRemote(name), meta); err != nil {
		return nil, err
	}
	if err := s.cs.PutJSON(ctx, s.manifestRemote(name), man); err != nil {
		return nil, err
	}
	return meta, nil
}

// EnsureLocal materializes the volume's image locally from the manifest using
// the shared chunk cache, and returns its path.
//
// Ranges already identical to the manifest are left untouched (per-chunk
// hash compare), so a create on an up-to-date image performs zero chunk
// writes. A sidecar records the last materialized manifest plus the image's
// size/mtime, so the fully-up-to-date case skips even the comparison reads.
// When the remote is a local dir, chunk reads go straight through to it and
// the legacy cache copy is removed.
func (s *Store) EnsureLocal(ctx context.Context, name string) (string, error) {
	meta, err := s.Get(ctx, name)
	if err != nil {
		return "", err
	}
	// Local backend: the cache is pure waste (every byte would live twice on
	// one disk), so drop it once instead of feeding it. Concurrent applies
	// already hold full chunk bytes in memory, so deleting files out from
	// under them only turns into a re-fetch, never corruption.
	if s.localRemote != "" {
		s.cleanedCache.Do(func() { _ = os.RemoveAll(s.cacheDir()) })
	}
	man, err := s.Manifest(ctx, name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(s.dirPath(name), 0o755); err != nil {
		return "", err
	}
	if s.appliedCurrent(name, man) {
		s.setLastApply(cloudstore.ApplyStats{})
		return s.ImagePath(name), nil
	}
	f, err := os.OpenFile(s.ImagePath(name), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := f.Truncate(meta.Size); err != nil {
		return "", err
	}
	st, err := s.cs.ApplyManifest(ctx, man, f, s.cacheDir())
	if err != nil {
		return "", err
	}
	s.setLastApply(st)
	if err := f.Sync(); err != nil {
		return "", err
	}
	s.recordApplied(name, man)
	return s.ImagePath(name), nil
}

func (s *Store) setLastApply(st cloudstore.ApplyStats) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	s.lastApply = st
}

// appliedSidecar is the sidecar recording what disk.img currently holds.
type appliedSidecar struct {
	Manifest string `json:"manifest"` // digest of the materialized manifest
	Size     int64  `json:"size"`
	Mtime    int64  `json:"mtime_ns"`
}

func (s *Store) sidecarPath(name string) string {
	return filepath.Join(s.dirPath(name), ".applied.json")
}

// manifestDigest is the content hash of a manifest's canonical form.
func manifestDigest(m *cloudstore.Manifest) string {
	keys := make([]string, 0, len(m.Chunks))
	for k := range m.Chunks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString(strconv.FormatInt(m.ChunkSize, 10))
	sb.WriteByte('/')
	sb.WriteString(strconv.FormatInt(m.Size, 10))
	for _, k := range keys {
		sb.WriteByte('/')
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(m.Chunks[k])
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// appliedCurrent reports whether disk.img provably matches man: the sidecar
// names the same manifest digest and the image is byte-identical in size and
// mtime to when the sidecar was written. Any guest run or external write
// changes mtime and forces the full per-chunk comparison.
func (s *Store) appliedCurrent(name string, man *cloudstore.Manifest) bool {
	raw, err := os.ReadFile(s.sidecarPath(name))
	if err != nil {
		return false
	}
	var sc appliedSidecar
	if err := json.Unmarshal(raw, &sc); err != nil {
		return false
	}
	if sc.Manifest == "" || sc.Manifest != manifestDigest(man) {
		return false
	}
	fi, err := os.Stat(s.ImagePath(name))
	if err != nil || fi.Size() != sc.Size {
		return false
	}
	return fi.ModTime().UnixNano() == sc.Mtime
}

func (s *Store) recordApplied(name string, man *cloudstore.Manifest) {
	fi, err := os.Stat(s.ImagePath(name))
	if err != nil {
		return
	}
	sc := appliedSidecar{Manifest: manifestDigest(man), Size: fi.Size(), Mtime: fi.ModTime().UnixNano()}
	raw, err := json.Marshal(sc)
	if err != nil {
		return
	}
	_ = os.WriteFile(s.sidecarPath(name), raw, 0o644)
}

// Commit uploads the local image's allocated chunks and refreshes the manifest.
func (s *Store) Commit(ctx context.Context, name string) error {
	meta, err := s.Get(ctx, name)
	if err != nil {
		return err
	}
	img, err := os.Open(s.ImagePath(name))
	if err != nil {
		return err
	}
	defer img.Close()
	m, err := s.cs.BuildManifest(ctx, img, meta.Size, meta.ChunkSize)
	if err != nil {
		return err
	}
	if err := s.cs.PutJSON(ctx, s.manifestRemote(name), m); err != nil {
		return err
	}
	// The manifest was just built from this exact disk, so they match.
	s.recordApplied(name, m)
	return nil
}

// --- garbage collection ---

// GCReport describes what a garbage-collection pass found and did.
type GCReport struct {
	Volumes   int      `json:"volumes"`
	Chunks    int      `json:"chunks"`
	Orphans   []string `json:"orphans"`
	Reclaimed int64    `json:"reclaimed_bytes"`
	Deleted   int      `json:"deleted"`
	DryRun    bool     `json:"dry_run"`
}

// ReferencedHashes collects the chunk hashes named by every volume manifest,
// every snapshot manifest, and the base-image manifest. Anything stored but
// not in this set is unreachable and safe to delete — including while a
// desktop is attached, because the attached volume's own manifest keeps its
// chunks referenced.
func (s *Store) ReferencedHashes(ctx context.Context) (map[string]bool, error) {
	ref := map[string]bool{}
	vols, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range vols {
		man, err := s.Manifest(ctx, v.Name)
		if err != nil {
			continue
		}
		for _, h := range man.Chunks {
			ref[h] = true
		}
	}
	snaps, err := s.ListSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	for _, sn := range snaps {
		man := &cloudstore.Manifest{Chunks: map[string]string{}}
		if err := s.cs.GetJSON(ctx, s.snapshotManifestRemote(sn.ID), man); err != nil {
			continue
		}
		for _, h := range man.Chunks {
			ref[h] = true
		}
	}
	base := &cloudstore.Manifest{Chunks: map[string]string{}}
	if err := s.cs.GetJSON(ctx, s.baseManifestRemote(), base); err == nil {
		for _, h := range base.Chunks {
			ref[h] = true
		}
	}
	return ref, nil
}

// GC deletes remote chunks no manifest or snapshot references. Chunks newer
// than the grace period are spared, so an in-flight commit's not-yet-linked
// uploads survive. Dry-run lists without deleting.
func (s *Store) GC(ctx context.Context, dryRun bool) (GCReport, error) {
	var rep GCReport
	rep.DryRun = dryRun
	ref, err := s.ReferencedHashes(ctx)
	if err != nil {
		return rep, err
	}
	chunks, err := s.cs.ListChunks(ctx)
	if err != nil {
		return rep, err
	}
	rep.Chunks = len(chunks)
	grace := s.gcGracePeriod()
	cutoff := time.Now().Add(-grace)
	for _, c := range chunks {
		if ref[c.Hash] {
			continue
		}
		if c.ModTime.After(cutoff) {
			continue
		}
		rep.Orphans = append(rep.Orphans, c.Hash)
		rep.Reclaimed += c.Size
	}
	sort.Strings(rep.Orphans)
	vols, _ := s.List(ctx)
	rep.Volumes = len(vols)
	if dryRun {
		return rep, nil
	}
	for _, h := range rep.Orphans {
		if err := s.cs.DeleteChunk(ctx, h); err == nil {
			rep.Deleted++
		}
	}
	return rep, nil
}

// --- single-attach lock (in-process) ---

// Attach claims a volume for owner. It errors if another owner holds it.
func (s *Store) Attach(name, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.attached[name]; ok && cur != owner {
		return fmt.Errorf("volume %q is already attached to %s", name, cur)
	}
	s.attached[name] = owner
	return nil
}

// Release frees the attach lock held by owner.
func (s *Store) Release(name, owner string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attached[name] == owner {
		delete(s.attached, name)
	}
}

// AttachedTo reports the current owner of a volume, if any.
func (s *Store) AttachedTo(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attached[name]
}

// --- helpers ---

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func createSparse(path string, size int64) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Truncate(size)
}

// cloneFile makes a copy-on-write clone when the filesystem supports it
// (APFS "cp -c"), otherwise a full copy.
func cloneFile(src, dst string) error {
	if err := exec.Command("cp", "-c", src, dst).Run(); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
