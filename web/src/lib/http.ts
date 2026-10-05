// Transport for the warmbox daemon API: one fetch wrapper that speaks the
// daemon's error shape, plus the helpers every resource file builds on.
//
// Auth is session-cookie based (see internal/api/auth.go). The browser sends
// the cookie automatically — credentials: "include" below — so there is no
// token to store. CLI and scripts use per-user API tokens as Bearer
// credentials, managed from Settings.

export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
    this.name = "ApiError";
  }
}

export async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  let res: Response;
  try {
    res = await fetch(path, { ...init, headers, credentials: "include" });
  } catch (e) {
    throw new ApiError(
      e instanceof Error ? e.message : "network error",
      0,
    );
  }

  if (res.status === 401) {
    throw new ApiError("unauthorized", 401);
  }
  if (!res.ok) {
    let msg = res.statusText;
    try {
      const body = await res.json();
      msg = body?.error ?? msg;
    } catch {
      /* not json */
    }
    throw new ApiError(msg || `request failed (${res.status})`, res.status);
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  if (!text) return undefined as T;
  try {
    return JSON.parse(text) as T;
  } catch {
    return text as unknown as T;
  }
}

export const json = (v: unknown): RequestInit => ({ body: JSON.stringify(v) });
