// The daemon's API, one entry point: per-resource call groups composed into
// the single `api` object callers use. The error class, the role helper and
// the shared shapes are re-exported here too, so a caller needs one import.

import { ApiError } from "../http";
import { auth } from "./auth";
import { cloud, status } from "./daemon";
import { desktops } from "./desktops";
import { images, pullImage } from "./images";
import { settings } from "./settings";
import { snapshots } from "./snapshots";
import { tokens } from "./tokens";
import { users, roleRank } from "./users";
import { volumes } from "./volumes";
import { workspaces } from "./workspaces";

export const api = {
  status,
  cloud,
  desktops,
  settings,
  volumes,
  snapshots,
  images,
  pullImage,
  auth,
  users,
  workspaces,
  tokens,
};

export { ApiError, roleRank };
export type * from "../types";
