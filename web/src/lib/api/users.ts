import { json, request } from "../http";
import type { Member, Role } from "../types";

export const users = {
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
};

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
