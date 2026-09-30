// Small formatting helpers shared across pages.

export function bytes(n: number | undefined | null): string {
  if (!n || n < 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  const digits = v >= 100 || i === 0 ? 0 : v >= 10 ? 1 : 2;
  return `${v.toFixed(digits)} ${units[i]}`;
}

// parseSize turns "8G" / "512M" into bytes for display; 0 when unset/invalid.
export function parseSize(s: string): number {
  const m = /^\s*(\d+(?:\.\d+)?)\s*([TGM])?\s*$/i.exec(s);
  if (!m) return 0;
  const n = parseFloat(m[1]);
  const mult: Record<string, number> = {
    T: 1024 ** 4,
    G: 1024 ** 3,
    M: 1024 ** 2,
    "": 1,
  };
  return Math.round(n * (mult[(m[2] ?? "").toUpperCase()] ?? 1));
}

export function since(iso: string | undefined): string {
  if (!iso) return "—";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "—";
  return ago(Date.now() - t);
}

export function ago(ms: number): string {
  if (ms < 0) ms = 0;
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

export function uptime(iso: string | undefined): string {
  if (!iso) return "—";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "—";
  let s = Math.floor((Date.now() - t) / 1000);
  if (s < 0) s = 0;
  const h = Math.floor(s / 3600);
  s %= 3600;
  const m = Math.floor(s / 60);
  s %= 60;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

export function clock(iso: string | undefined): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

export function title(s: string): string {
  return s.length ? s[0].toUpperCase() + s.slice(1) : s;
}
