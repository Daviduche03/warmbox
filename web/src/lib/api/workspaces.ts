import { request } from "../http";
import type { WorkspaceRef } from "../types";

export const workspaces = {
  list: () =>
    request<{ workspaces: Array<WorkspaceRef & { role: string }> }>("/api/workspaces"),
};
