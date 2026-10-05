import { useEffect } from "react";

const POLL_MS = 5000;
// A short burst on first paint is enough to get a line onto the charts. Holding
// this rate for the whole window (48 ticks, as it did before) meant ~50 refetches
// in the first minute, which reads as the dashboard reloading constantly.
const FAST_POLL_MS = 1500;
const FAST_TICKS = 8;

/**
 * How often the store refetches: a fast burst while the page settles, then a
 * steady 5s. Kept apart from the fetch itself so the cadence can be reasoned
 * about (and changed) on its own.
 */
export function usePolling(refresh: () => Promise<void>) {
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
}
