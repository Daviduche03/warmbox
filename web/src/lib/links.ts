// desktopUrl is the short noVNC URL for a guest. The browser's session cookie
// authenticates the page, the iframe and the websocket alike.
export function desktopUrl(id: string): string {
  return `/d/${id}`;
}

export function publishUrl(id: string, port: number | string): string {
  return `${window.location.origin}/p/${id}/${port}/`;
}

export function openDesktop(id: string) {
  window.open(desktopUrl(id), "_blank", "noopener");
}
