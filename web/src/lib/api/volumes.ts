import { json, request } from "../http";
import type { Volume } from "../types";

export const volumes = {
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
};
