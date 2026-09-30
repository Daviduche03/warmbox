import type { Sample } from "./store";

export type CountKey = Exclude<keyof Sample, "t">;

/**
 * Absolute change between the first and last sample of the live window.
 * Counts stay in their own units — a fleet of 1 becoming 2 is "+1", not "+100%".
 * Returns null until the window holds at least two samples.
 */
export function windowDelta(history: Sample[], key: CountKey): number | null {
  if (history.length < 2) return null;
  const first = history[0][key];
  const last = history[history.length - 1][key];
  if (typeof first !== "number" || typeof last !== "number") return null;
  return last - first;
}

/** Wall-clock length of the sample window, or null while there is only one sample. */
export function windowSpanMs(history: ReadonlyArray<{ t: number }>): number | null {
  if (history.length < 2) return null;
  const span = history[history.length - 1].t - history[0].t;
  return span > 0 ? span : null;
}

/** "42s" / "3m" — used for the "in last …" footnote under a delta. */
export function shortDuration(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  const rem = s % 60;
  return rem >= 30 ? `${m + 1}m` : m >= 1 ? `${m}m` : `${rem}s`;
}

/** Footnote shared by the stat tiles: how far back the delta reaches. */
export function deltaFootnote(history: Sample[], empty: string): string {
  const span = windowSpanMs(history);
  if (span === null) return empty;
  return `in last ${shortDuration(span)}`;
}

/**
 * Integer Y-axis bounds for a count series: a domain that reaches `max` and no
 * further, with a whole number of ticks. Left to recharts, a max of 1 becomes a
 * 0–2 axis and half the plot sits empty.
 */
export function integerAxis(
  max: number,
  maxTicks = 6,
): { domainMax: number; tickCount: number } {
  const top = Math.max(1, Math.ceil(Number.isFinite(max) ? max : 1));
  const step = Math.max(1, Math.ceil(top / Math.max(2, maxTicks - 1)));
  const domainMax = Math.ceil(top / step) * step;
  return { domainMax, tickCount: domainMax / step + 1 };
}

/** Percentile-free share of `value` across the pool, 0 when the pool is empty. */
export function pct(value: number, total: number): number {
  if (total <= 0) return 0;
  return (value / total) * 100;
}
