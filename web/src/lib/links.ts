import { getToken } from "./api";

// desktopUrl is the short noVNC URL for a guest. When token auth is on we
// append it once: the daemon sets the auth cookie and redirects to the clean
// path, so the iframe/websocket afterwards inherit auth without the token.
export function desktopUrl(id: string): string {
  const t = getToken();
  return `/d/${id}${t ? `?token=${encodeURIComponent(t)}` : ""}`;
}

export function publishUrl(id: string, port: number | string): string {
  return `${window.location.origin}/p/${id}/${port}/`;
}

export function openDesktop(id: string) {
  window.open(desktopUrl(id), "_blank", "noopener");
}
