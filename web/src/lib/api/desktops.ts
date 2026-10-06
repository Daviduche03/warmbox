import { json, request } from "../http";
import type { Desktop, ExecResult } from "../types";

export const desktops = {
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
  checkpoint: (id: string, name?: string) =>
    request<{ name: string; size: number; checkpoint_ms: number }>(
      `/api/desktops/${id}/checkpoint`,
      { method: "POST", ...json(name ? { name } : {}) },
    ),
  checkpoints: (id: string) =>
    request<{ checkpoints: { name: string; size: number; created: string; image: string }[] }>(
      `/api/desktops/${id}/checkpoints`,
    ),
  checkpointDelete: (id: string, name: string) =>
    request<{ status: string; id: string }>(
      `/api/desktops/${id}/checkpoints/${encodeURIComponent(name)}`,
      { method: "DELETE" },
    ),
  restore: (id: string, name?: string) =>
    request<{ status: string; id: string; restore_ms: number }>(
      `/api/desktops/${id}/restore`,
      { method: "POST", ...json(name ? { name } : {}) },
    ),
  hibernate: (id: string) =>
    request<{ status: string; id: string }>(`/api/desktops/${id}/hibernate`, {
      method: "POST",
    }),
  wake: (id: string) =>
    request<{ status: string; id: string; restore_ms: number }>(
      `/api/desktops/${id}/wake`,
      { method: "POST" },
    ),
  exec: (id: string, cmd: string, cwd?: string, timeoutMs = 30000) =>
    request<ExecResult>(`/api/desktops/${id}/exec`, {
      method: "POST",
      ...json({ cmd, cwd, timeout_ms: timeoutMs }),
    }),
};
