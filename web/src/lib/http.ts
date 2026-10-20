// Transport for the warmbox daemon API: one fetch wrapper that speaks the
// daemon's error shape, plus the helpers every resource file builds on.
//
// Auth is session-cookie based (see internal/api/auth.go). The browser sends
// the cookie automatically — credentials: "include" below — so there is no
// token to store. CLI and scripts use per-user API tokens as Bearer
// credentials, managed from Settings.

export class ApiError extends Error {
  status: number;
  code?: string;
  constructor(message: string, status: number, code?: string) {
    super(message);
    this.status = status;
    this.code = code;
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
    let code: string | undefined;
    try {
      const body = await res.json();
      // The API answers a failure with a structured envelope: the message is
      // a sentence, the code is the machine-readable half.
      msg = body?.error?.message ?? body?.message ?? msg;
      code = body?.error?.code ?? body?.code;
    } catch {
      /* not json */
    }
    throw new ApiError(msg || `request failed (${res.status})`, res.status, code);
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
