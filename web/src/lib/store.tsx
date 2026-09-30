import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  api,
  ApiError,
  type DaemonStatus,
  type Desktop,
  type Me,
  type Snapshot,
  type Volume,
} from "./api";

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

interface Store {
  status?: DaemonStatus;
  desktops: Desktop[];
  volumes: Volume[];
  snapshots: Snapshot[];
  images: string[];
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

const Ctx = createContext<Store | null>(null);
const POLL_MS = 5000;
// A short burst on first paint is enough to get a line onto the charts. Holding
// this rate for the whole window (48 ticks, as it did before) meant ~50 refetches
// in the first minute, which reads as the dashboard reloading constantly.
const FAST_POLL_MS = 1500;
const FAST_TICKS = 8;
const MAX_SAMPLES = 48;

export function StoreProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<DaemonStatus>();
  const [desktops, setDesktops] = useState<Desktop[]>([]);
  const [volumes, setVolumes] = useState<Volume[]>([]);
  const [snapshots, setSnapshots] = useState<Snapshot[]>([]);
  const [images, setImages] = useState<string[]>([]);
  const [history, setHistory] = useState<Sample[]>([]);
  const [events, setEvents] = useState<Event[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [unauthorized, setUnauthorized] = useState(false);
  const [me, setMe] = useState<Me | null>(null);
  const [needsSetup, setNeedsSetup] = useState(false);
  const busy = useRef(false);
  const lastDesktops = useRef<Map<string, string>>(new Map());

  const refresh = useCallback(async () => {
    if (busy.current) return;
    busy.current = true;
    try {
      // Identity first: setup status is public, everything else needs it.
      const setup = await api.auth.setupStatus().catch(() => undefined);
      if (setup?.needs_setup) {
        setNeedsSetup(true);
        setMe(null);
        setUnauthorized(false);
        setLoading(false);
        return;
      }
      setNeedsSetup(false);
      const identity = await api.auth.me().catch((e) => {
        if (e instanceof ApiError && e.status === 401) throw e;
        return undefined;
      });
      if (!identity) {
        setError("connection failed");
        return;
      }
      setMe(identity);
      const [st, d, v, s, im] = await Promise.all([
        api.status().catch((e) => {
          if (e instanceof ApiError && e.status === 401) throw e;
          return undefined;
        }),
        api.desktops.list().catch(() => ({ desktops: [] })),
        api.volumes.list().catch(() => ({ volumes: [] })),
        api.snapshots.list().catch(() => ({ snapshots: [] })),
        api.images().catch(() => ({ images: [] })),
      ]);
      const ds = d.desktops ?? [];
      setStatus(st);
      setDesktops(ds);
      setVolumes(v.volumes ?? []);
      setSnapshots(s.snapshots ?? []);
      setImages(im.images ?? []);
      setError(undefined);
      setUnauthorized(false);

      // Derive an event log from desktop state transitions between polls.
      const prev = lastDesktops.current;
      const now = new Map(ds.map((x) => [x.id, x.state]));
      const fresh: Event[] = [];
      const t = Date.now();
      for (const x of ds) {
        const before = prev.get(x.id);
        if (before === undefined) {
          fresh.push({ t, kind: "created", message: `${x.id} appeared (${x.state})` });
        } else if (before !== x.state) {
          fresh.push({ t, kind: "state", message: `${x.id} ${before} → ${x.state}` });
        }
      }
      for (const [id] of prev) {
        if (!now.has(id)) fresh.push({ t, kind: "gone", message: `${id} destroyed` });
      }
      lastDesktops.current = now;
      if (fresh.length) setEvents((e) => [...fresh.reverse(), ...e].slice(0, 200));

      const sample: Sample = {
        t: Date.now(),
        ready: ds.filter((x) => x.state === "ready").length,
        paused: ds.filter((x) => x.state === "paused").length,
        booting: ds.filter((x) => x.state === "booting").length,
        desktops: ds.length,
        idle: st?.pool.idle ?? 0,
        volumes: v.volumes?.length ?? 0,
        snapshots: s.snapshots?.length ?? 0,
        images: im.images?.length ?? 0,
      };
      setHistory((h) => [...h, sample].slice(-MAX_SAMPLES));
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        setMe(null);
        setUnauthorized(true);
      } else {
        setError(e instanceof Error ? e.message : "connection failed");
      }
    } finally {
      busy.current = false;
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    let timer: number | undefined;
    let ticks = 0;
    const tick = async () => {
      if (cancelled) return;
      await refresh();
      ticks += 1;
      if (cancelled) return;
      timer = window.setTimeout(tick, ticks < FAST_TICKS ? FAST_POLL_MS : POLL_MS);
    };
    void tick();
    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [refresh]);

  return (
    <Ctx.Provider
      value={{
        status,
        desktops,
        volumes,
        snapshots,
        images,
        history,
        events,
        loading,
        error,
        unauthorized,
        me,
        needsSetup,
        refresh,
      }}
    >
      {children}
    </Ctx.Provider>
  );
}

export function useStore(): Store {
  const s = useContext(Ctx);
  if (!s) throw new Error("useStore outside StoreProvider");
  return s;
}
