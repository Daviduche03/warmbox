import { json, request } from "../http";

/** The daemon-wide desktop ceiling: how much memory one workspace may hold. */
export const settings = {
  get: () =>
    request<{ max_desktops_per_workspace: number; desktops_in_workspace: number }>(
      "/api/settings",
    ),
  update: (body: { max_desktops_per_workspace: number }) =>
    request<{ max_desktops_per_workspace: number }>("/api/settings", {
      method: "PATCH",
      ...json(body),
    }),
};
