import { json, request } from "../http";
import type { Snapshot } from "../types";

export const snapshots = {
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
};
