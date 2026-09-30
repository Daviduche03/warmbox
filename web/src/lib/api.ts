// Typed client for the warmbox daemon. Mirrors the JSON shapes in
// internal/api/server.go, internal/catalog and internal/desktop.

// Auth is session-cookie based (see internal/api/auth.go). The browser sends
// the cookie automatically — credentials: "include" below — so there is no
// token to store. CLI and scripts use per-user API tokens as Bearer credentials,
// managed from Settings.

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

export type DesktopState = "booting" | "ready" | "busy" | "paused" | "dead";

export interface Desktop {
  id: string;
  state: DesktopState;
  guest_ip?: string;
  started: string;
  volume?: string;
  allow?: string[];
  deny?: string[];
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

export function roleRank(role?: string): number {
  switch (role) {
    case "owner":
      return 4;
    case "admin":
      return 3;
    case "member":
      return 2;
    case "viewer":
      return 1;
    default:
      return 0;
  }
}

// ── calls ────────────────────────────────────────────────────────────────

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

export const api = {
  status: () => request<DaemonStatus>("/api/status"),

  cloud: {
    get: () => request<CloudStorage>("/api/cloud"),
    set: (body: {
      provider?: string;
      bucket: string;
      endpoint?: string;
      region?: string;
      access_key?: string;
      secret_key?: string;
    }) =>
      request<{ ok: boolean; restart_required?: boolean }>("/api/cloud", {
        method: "PUT",
        ...json(body),
      }),
    clear: () =>
      request<{ ok: boolean; restart_required?: boolean }>("/api/cloud", {
        method: "DELETE",
      }),
  },

  desktops: {
    list: () => request<{ desktops: Desktop[] | null }>("/api/desktops"),
    get: (id: string) => request<Desktop>(`/api/desktops/${id}`),
    create: (opts: {
      volume?: string;
      image?: string;
      cpus?: number;
      mem_mib?: number;
      allow?: string[];
      deny?: string[];
    } = {}) =>
      request<{ id: string; vnc: string; ws: string; volume?: string }>(
        "/api/desktops",
        { method: "POST", ...json(opts) },
      ),
    setPolicy: (id: string, allow: string[], deny: string[]) =>
      request<{ id: string; allow: string[]; deny: string[] }>(
        `/api/desktops/${id}/policy`,
        { method: "POST", ...json({ allow, deny }) },
      ),
    destroy: (id: string) =>
      request<{ status: string }>(`/api/desktops/${id}`, { method: "DELETE" }),
    pause: (id: string) =>
      request<{ status: string; id: string }>(`/api/desktops/${id}/pause`, {
        method: "POST",
      }),
    resume: (id: string) =>
      request<{ status: string; id: string }>(`/api/desktops/${id}/resume`, {
        method: "POST",
      }),
    exec: (id: string, cmd: string, cwd?: string, timeoutMs = 30000) =>
      request<ExecResult>(`/api/desktops/${id}/exec`, {
        method: "POST",
        ...json({ cmd, cwd, timeout_ms: timeoutMs }),
      }),
  },

  settings: {
    get: () =>
      request<{ max_desktops_per_workspace: number; desktops_in_workspace: number }>(
        "/api/settings",
      ),
    update: (body: { max_desktops_per_workspace: number }) =>
      request<{ max_desktops_per_workspace: number }>("/api/settings", {
        method: "PATCH",
        ...json(body),
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

  auth: {
    setupStatus: () => request<{ needs_setup: boolean }>("/api/setup/status"),
    setup: (body: { name: string; email: string; password: string; workspace: string }) =>
      request<Me>("/api/setup", { method: "POST", ...json(body) }),
    login: (body: { email: string; password: string }) =>
      request<Me>("/api/login", { method: "POST", ...json(body) }),
    logout: () => request<{ status: string }>("/api/logout", { method: "POST" }),
    me: () => request<Me>("/api/me"),
    changePassword: (body: { current: string; next: string }) =>
      request<{ status: string }>("/api/me/password", { method: "PATCH", ...json(body) }),
  },

  users: {
    list: () => request<{ users: Member[] }>("/api/users"),
    create: (body: { name: string; email: string; password: string; role: Role }) =>
      request<{ user: Member }>("/api/users", { method: "POST", ...json(body) }),
    setRole: (id: string, role: Role) =>
      request<{ status: string; role: string }>(`/api/users/${id}`, {
        method: "PATCH",
        ...json({ role }),
      }),
    remove: (id: string) =>
      request<{ status: string }>(`/api/users/${id}`, { method: "DELETE" }),
  },

  workspaces: {
    list: () => request<{ workspaces: Array<WorkspaceRef & { role: string }> }>("/api/workspaces"),
  },

  tokens: {
    list: () => request<{ tokens: ApiToken[] }>("/api/tokens"),
    create: (name: string) =>
      request<ApiToken & { token: string }>("/api/tokens", {
        method: "POST",
        ...json({ name }),
      }),
    revoke: (id: string) =>
      request<{ status: string }>(`/api/tokens/${id}`, { method: "DELETE" }),
  },
};
