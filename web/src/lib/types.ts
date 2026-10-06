// The JSON shapes the daemon serves. Mirrors internal/api/server.go,
// internal/catalog and internal/desktop — keep these in step with the Go.

export type DesktopState = "booting" | "ready" | "busy" | "paused" | "hibernated" | "dead";

export interface Desktop {
  id: string;
  state: DesktopState;
  guest_ip?: string;
  started: string;
  volume?: string;
  allow?: string[];
  deny?: string[];
  /** The image behind this desktop declares no screen — nothing at /d/. */
  headless?: boolean;
}

/**
 * Per-image boot overrides from an image's meta.json. Only `headless` changes
 * what the dashboard can offer: a headless image has no console to open.
 */
export interface ImageMeta {
  gpu?: string;
  mem_mib?: number;
  cpus?: number;
  input?: boolean;
  headless?: boolean;
}

/** GET /api/images — the name list plus each image's meta.json overrides. */
export interface ImagesResponse {
  images: string[] | null;
  /** Absent on older daemons; nothing is headless then. */
  image_meta?: Record<string, ImageMeta>;
  /** Every image this build knows about, installed or not. */
  catalogue?: ImageStatus[];
}

/**
 * One row of the Images page: an image the daemon can boot, or one this build
 * knows about that has not been fetched yet.
 */
export interface ImageStatus {
  name: string;
  headless?: boolean;
  /** The daemon can boot it right now. */
  installed?: boolean;
  /** There is something to download. False for unpublished or locally built. */
  pullable?: boolean;
  state: "installed" | "available" | "pulling" | "failed";
  /** Why a pull failed. */
  detail?: string;
  /** Bytes downloaded and expected, while state is "pulling". */
  done?: number;
  total?: number;
}

export interface Volume {
  name: string;
  size: number;
  chunk_size: number;
  remote?: string;
  from?: string;
  created_at: string;
  updated_at: string;
}

export interface Snapshot {
  id: string;
  volume: string;
  size: number;
  chunk_size: number;
  created_at: string;
}

export interface DaemonStatus {
  version: string;
  backend: string;
  addr: string;
  up: string;
  pool: { size: number; idle: number; booting: number };
  desktops: number;
  volumes: number;
  snapshots: number;
  images: string[];
  volumes_backed: string;
  default_image: string;
  /** Per-VM sizes a desktop gets unless the create request names its own. */
  defaults: { cpus: number; mem_mib: number };
  /**
   * The daemon's cached answer to "is there something newer?" — gathered by a
   * background check, so it can be absent (no checker), still checking, or
   * carrying an error instead of pretending everything is current.
   */
  updates?: UpdateCheck;
}

/** What the background release check found. Both halves are independent. */
export interface UpdateCheck {
  /** RFC3339; absent until the first check finishes. */
  checked_at?: string;
  /** Why the last check failed — shown as "couldn't check", never as "up to date". */
  error?: string;
  binary?: {
    current: string;
    latest: string;
    /** true only when both parse as versions and latest is ahead. */
    available: boolean;
    url: string;
  };
  image?: {
    arch: string;
    /** Recorded sha256 of the installed image, "local" if built here, else absent. */
    current?: string;
    /** Published sha256; absent when that check failed. */
    latest?: string;
    /** false when nothing was recorded at install time. */
    known: boolean;
    available: boolean;
    url: string;
  };
}

export interface ExecResult {
  exit: number;
  stdout: string;
  stderr: string;
  duration_ms: number;
  timed_out?: boolean;
}

export interface AuthUser {
  id: string;
  name: string;
  email: string;
}

export interface WorkspaceRef {
  id: string;
  name: string;
}

export interface Me {
  user: AuthUser;
  workspace: WorkspaceRef;
  role: string;
  workspaces: WorkspaceRef[];
}

export interface Member extends AuthUser {
  role: string;
}

export interface ApiToken {
  id: string;
  name: string;
  prefix: string;
  created_at: string;
  last_used_at: string | null;
}

export type Role = "viewer" | "member" | "admin" | "owner";

export type CloudStorage = {
  configured: boolean;
  provider: string;
  bucket: string;
  endpoint: string;
  region: string;
  /** Masked on read; leave blank when saving to keep the stored value. */
  access_key: string;
  secret_key: string;
  path: string;
};
