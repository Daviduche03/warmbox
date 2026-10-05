import { json, request } from "../http";
import type { ApiToken } from "../types";

export const tokens = {
  list: () => request<{ tokens: ApiToken[] }>("/api/tokens"),
  create: (name: string) =>
    request<ApiToken & { token: string }>("/api/tokens", {
      method: "POST",
      ...json({ name }),
    }),
  revoke: (id: string) =>
    request<{ status: string }>(`/api/tokens/${id}`, { method: "DELETE" }),
};
