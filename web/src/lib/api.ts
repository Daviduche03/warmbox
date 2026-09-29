// Typed client for the warmbox daemon. Mirrors the JSON shapes in
// internal/api/server.go, internal/catalog and internal/desktop.

const TOKEN_KEY = "warmbox.token";

export function getToken(): string {
  try {
    return localStorage.getItem(TOKEN_KEY) ?? "";
  } catch {
    return "";
  }
}

export function setToken(token: string) {
  try {
    if (token) localStorage.setItem(TOKEN_KEY, token);
    else localStorage.removeItem(TOKEN_KEY);
  } catch {
    /* ignore */
  }
}

export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
    this.name = "ApiError";
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const token = getToken();
  if (token) headers.set("Authorization", `Bearer ${token}`);

  let res: Response;
  try {
    res = await fetch(path, { ...init, headers, credentials: "include" });
  } catch (e) {
    throw new ApiError(
      e instanceof Error ? e.message : "network error",
      0,
    );
  }

  if (res.status === 401) {
    throw new ApiError("unauthorized", 401);
  }
  if (!res.ok) {
    let msg = res.statusText;
    try {
      const body = await res.json();
      msg = body?.error ?? msg;
    } catch {
      /* not json */
    }
    throw new ApiError(msg || `request failed (${res.status})`, res.status);
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  if (!text) return undefined as T;
  try {
    return JSON.parse(text) as T;
  } catch {
    return text as unknown as T;
  }
}

const json = (v: unknown): RequestInit => ({ body: JSON.stringify(v) });

// ── types ────────────────────────────────────────────────────────────────

export type DesktopState = "booting" | "ready" | "busy" | "dead";

export interface Desktop {
  id: string;
  state: DesktopState;
  guest_ip?: string;
  started: string;
  volume?: string;
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
  token_required: boolean;
  up: string;
  pool: { size: number; idle: number; booting: number };
  desktops: number;
  volumes: number;
  snapshots: number;
  images: string[];
  volumes_backed: string;
  default_image: string;
}

export interface ExecResult {
  exit: number;
  stdout: string;
  stderr: string;
  duration_ms: number;
  timed_out?: boolean;
}

// ── calls ────────────────────────────────────────────────────────────────

export const api = {
  status: () => request<DaemonStatus>("/api/status"),

  desktops: {
    list: () => request<{ desktops: Desktop[] | null }>("/api/desktops"),
    get: (id: string) => request<Desktop>(`/api/desktops/${id}`),
    create: (opts: { volume?: string; image?: string } = {}) =>
      request<{ id: string; vnc: string; ws: string; volume?: string }>(
        "/api/desktops",
        { method: "POST", ...json(opts) },
      ),
    destroy: (id: string) =>
      request<{ status: string }>(`/api/desktops/${id}`, { method: "DELETE" }),
    exec: (id: string, cmd: string, cwd?: string, timeoutMs = 30000) =>
      request<ExecResult>(`/api/desktops/${id}/exec`, {
        method: "POST",
        ...json({ cmd, cwd, timeout_ms: timeoutMs }),
      }),
  },

  volumes: {
    list: () => request<{ volumes: Volume[] | null }>("/api/volumes"),
    get: (name: string) => request<Volume>(`/api/volumes/${name}`),
    create: (body: {
      name: string;
      size?: string;
      from?: string;
      from_snapshot?: string;
    }) => request<Volume>("/api/volumes", { method: "POST", ...json(body) }),
    clone: (name: string, next: string) =>
      request<Volume>(`/api/volumes/${name}/clone`, {
        method: "POST",
        ...json({ name: next }),
      }),
    remove: (name: string) =>
      request<{ status: string }>(`/api/volumes/${name}`, { method: "DELETE" }),
  },

  snapshots: {
    list: (volume?: string) =>
      request<{ snapshots: Snapshot[] | null }>(
        "/api/snapshots" + (volume ? `?volume=${encodeURIComponent(volume)}` : ""),
      ),
    create: (volume: string, name?: string) =>
      request<Snapshot>("/api/snapshots", {
        method: "POST",
        ...json({ volume, name: name ?? "" }),
      }),
    remove: (id: string) =>
      request<{ status: string }>(`/api/snapshots/${id}`, { method: "DELETE" }),
  },

  images: () => request<{ images: string[] | null }>("/api/images"),
};
