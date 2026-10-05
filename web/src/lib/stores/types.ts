import type {
  DaemonStatus,
  Desktop,
  ImageMeta,
  ImageStatus,
  Me,
  Snapshot,
  Volume,
} from "../types";

/** One poll's worth of the numbers the trend cards draw. */
export interface Sample {
  t: number;
  ready: number;
  paused: number;
  booting: number;
  /** Counts carried alongside the state split so trend cards can compute their own deltas. */
  desktops: number;
  idle: number;
  volumes: number;
  snapshots: number;
  images: number;
}

export interface Event {
  t: number;
  kind: "created" | "state" | "gone" | "error" | "volume" | "snapshot";
  message: string;
}

/**
 * Everything the dashboard reads, refreshed by one polling loop. Identity comes
 * first in that loop because everything else needs it — and because a 401 there
 * is the signal to show the login screen rather than a broken page.
 */
export interface Store {
  status?: DaemonStatus;
  desktops: Desktop[];
  volumes: Volume[];
  snapshots: Snapshot[];
  images: string[];
  /** Per-image meta.json overrides, keyed by image name ({} on older daemons). */
  imageMeta: Record<string, ImageMeta>;
  /** Every image this build knows about, installed or not. */
  catalogue: ImageStatus[];
  history: Sample[];
  events: Event[];
  loading: boolean;
  error?: string;
  unauthorized: boolean;
  /** Null until logged in. Set alongside unauthorized. */
  me: Me | null;
  /** True when no account exists yet — the app shows setup instead of login. */
  needsSetup: boolean;
  refresh: () => Promise<void>;
}
