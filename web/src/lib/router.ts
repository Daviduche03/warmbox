import { useCallback, useEffect, useState } from "react";

// Path router: history.pushState + popstate, no dependency. The daemon already
// serves index.html for any path that is not pinned to a server route (see
// isShellPath in internal/api → the SPA fallback in web/embed.go), so a deep
// link like /snapshots resolves on a hard refresh and the address bar stays
// clean — no # fragment needed.

export interface Route {
  path: string; // e.g. "/desktops"
}

// Mirrors the daemon's isShellPath exclusions: anything under these prefixes is
// handled by the server (API, console, proxy) and must not be routed in-page.
const SERVER_PREFIXES = [
  "/api/",
  "/internal/",
  "/websockify/",
  "/p/",
  "/d/",
  "/vnc/",
];

/** True when the path is a client-side route (same rule the daemon applies). */
export function isAppPath(pathname: string): boolean {
  if (SERVER_PREFIXES.some((prefix) => pathname.startsWith(prefix))) {
    return false;
  }
  if (pathname === "/" || pathname === "/index.html") return true;
  const base = pathname.slice(pathname.lastIndexOf("/") + 1);
  return !base.includes(".");
}

function parse(): Route {
  const parts = window.location.pathname.split("/").filter(Boolean);
  return { path: "/" + (parts[0] ?? "") };
}

export function navigate(path: string) {
  const url = new URL(path, window.location.href);
  if (
    url.pathname === window.location.pathname &&
    url.search === window.location.search
  ) {
    return;
  }
  window.history.pushState({}, "", url.pathname + url.search);
  window.dispatchEvent(new PopStateEvent("popstate"));
}

// Plain left-clicks on our own links route in-page instead of reloading; hrefs
// stay real URLs, so open-in-new-tab, middle-click and the console links (which
// live under /d/) keep behaving like ordinary browser navigation.
function onLinkClick(event: MouseEvent) {
  if (
    event.defaultPrevented ||
    event.button !== 0 ||
    event.metaKey ||
    event.ctrlKey ||
    event.shiftKey ||
    event.altKey
  ) {
    return;
  }
  const anchor = (event.target as Element | null)?.closest?.(
    "a[href]"
  ) as HTMLAnchorElement | null;
  if (
    !anchor ||
    anchor.hasAttribute("download") ||
    anchor.target === "_blank" ||
    !isAppPath(new URL(anchor.href, window.location.href).pathname)
  ) {
    return;
  }
  const url = new URL(anchor.href, window.location.href);
  if (url.origin !== window.location.origin) return;
  event.preventDefault();
  navigate(url.pathname + url.search);
}

// Rewrite `#/settings` to `/settings` in place, then announce the new path.
function onLegacyHash() {
  if (!window.location.hash.startsWith("#/")) return;
  window.history.replaceState({}, "", window.location.hash.slice(1));
  window.dispatchEvent(new PopStateEvent("popstate"));
}

/**
 * Wires the router once per app mount. Returns the teardown.
 */
export function installRouter(): () => void {
  // Legacy `#/settings` links keep working — both a fresh load (bookmark) and
  // a fragment change on an already-open tab, which never reloads the page.
  onLegacyHash();
  window.addEventListener("hashchange", onLegacyHash);
  window.addEventListener("click", onLinkClick);
  return () => {
    window.removeEventListener("hashchange", onLegacyHash);
    window.removeEventListener("click", onLinkClick);
  };
}

export function useRoute(): Route {
  const [route, setRoute] = useState<Route>(parse);
  useEffect(() => {
    const onPop = () => setRoute(parse());
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);
  return route;
}

export function useNavigate() {
  return useCallback((path: string) => navigate(path), []);
}
