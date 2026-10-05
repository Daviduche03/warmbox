import { json, request } from "../http";
import type { Me } from "../types";

export const auth = {
  setupStatus: () => request<{ needs_setup: boolean }>("/api/setup/status"),
  setup: (body: { name: string; email: string; password: string; workspace: string }) =>
    request<Me>("/api/setup", { method: "POST", ...json(body) }),
  login: (body: { email: string; password: string }) =>
    request<Me>("/api/login", { method: "POST", ...json(body) }),
  logout: () => request<{ status: string }>("/api/logout", { method: "POST" }),
  me: () => request<Me>("/api/me"),
  changePassword: (body: { current: string; next: string }) =>
    request<{ status: string }>("/api/me/password", { method: "PATCH", ...json(body) }),
};
