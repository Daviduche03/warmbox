import { json, request } from "../http";
import type { CloudStorage, DaemonStatus } from "../types";

/** GET /api/status — version, backend, pool, and what the daemon holds. */
export const status = () => request<DaemonStatus>("/api/status");

/** Object-storage backup config. Secrets are masked on read; blank keeps the stored value. */
export const cloud = {
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
};
