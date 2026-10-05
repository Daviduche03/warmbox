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
  exec: (id: string, cmd: string, cwd?: string, timeoutMs = 30000) =>
    request<ExecResult>(`/api/desktops/${id}/exec`, {
      method: "POST",
      ...json({ cmd, cwd, timeout_ms: timeoutMs }),
    }),
};
