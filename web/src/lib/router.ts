import { useCallback, useEffect, useState } from "react";

// Minimal hash router: no dependency, works when the SPA is served from the
// daemon at any path (the hash never hits the server).

export interface Route {
  path: string; // e.g. "/desktops"
  param: string; // e.g. "abc123" for "/desktops/abc123"
}

function parse(): Route {
  const h = window.location.hash.replace(/^#/, "") || "/";
  const parts = h.split("/").filter(Boolean);
  return {
    path: "/" + (parts[0] ?? ""),
    param: parts[1] ? decodeURIComponent(parts[1]) : "",
  };
}

export function navigate(path: string) {
  if (window.location.hash === `#${path}`) return;
  window.location.hash = path;
}

export function useRoute(): Route {
  const [route, setRoute] = useState<Route>(parse);
  useEffect(() => {
    const onHash = () => setRoute(parse());
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);
  return route;
}

export function useNavigate() {
  return useCallback((path: string) => navigate(path), []);
}
