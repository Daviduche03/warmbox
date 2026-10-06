import type {
  ClientOptions, CreatedDesktop, CreatedRun, CreateDesktopOptions, Desktop,
  ExecOptions, ExecResult, FileInfo, RequestOptions, RunOptions, RunStatus,
  SessionOptions, StatusResult, StreamEvent, WriteResult,
} from "./types.js";

export type * from "./types.js";

export class APIError extends Error {
  constructor(public readonly status: number, public readonly body: string) {
    let message = body.trim();
    try {
      const data: unknown = JSON.parse(body);
      if (data && typeof data === "object" && "error" in data && typeof data.error === "string") {
        message = data.error;
      }
    } catch {}
    super(`warmbox HTTP ${status}: ${message}`);
    this.name = "APIError";
  }
}

function segment(value: string): string {
  if (!value || value === "." || value === "..") {
    throw new TypeError("a non-empty resource ID is required");
  }
  return encodeURIComponent(value);
}

export class WarmboxClient {
  private readonly baseUrl: string;
  private readonly headers: Record<string, string>;
  private readonly timeoutMs: number;

  constructor(options: ClientOptions) {
    const url = new URL(options.baseUrl ?? "http://127.0.0.1:7070");
    if (!["http:", "https:"].includes(url.protocol) || url.username || url.password || url.search || url.hash) {
      throw new TypeError("baseUrl must be an HTTP(S) URL without credentials, query or fragment");
    }
    if (!options.token) throw new TypeError("an API token is required");
    this.timeoutMs = options.timeoutMs ?? 120_000;
    if (!Number.isFinite(this.timeoutMs) || this.timeoutMs <= 0) {
      throw new TypeError("timeoutMs must be positive");
    }
    this.baseUrl = url.href.replace(/\/+$/, "");
    this.headers = { Authorization: `Bearer ${options.token}` };
    if (options.workspaceId !== undefined) this.headers["X-Workspace-ID"] = options.workspaceId;
  }

  private desktop(id: string): string {
    return `/api/desktops/${segment(id)}`;
  }

  private async request(
    method: string, path: string, options: RequestOptions = {},
    body?: BodyInit, contentType?: string, stream = false,
  ): Promise<Response> {
    const response = await fetch(this.baseUrl + path, {
      method, body, redirect: "manual",
      signal: options.signal ?? (stream ? undefined : AbortSignal.timeout(this.timeoutMs)),
      headers: {
        ...this.headers,
        Accept: stream ? "text/event-stream" : "application/json",
        ...(contentType ? { "Content-Type": contentType } : {}),
      },
    });
    if (!response.ok) throw new APIError(response.status, await response.text());
    return response;
  }

  private async json<T>(method: string, path: string, payload?: unknown, options?: RequestOptions): Promise<T> {
    const response = await this.request(method, path, options,
      payload === undefined ? undefined : JSON.stringify(payload),
      payload === undefined ? undefined : "application/json");
    const body = await response.text();
    return (body ? JSON.parse(body) : undefined) as T;
  }

  private async raw<T>(method: string, path: string, data: string | Uint8Array, options?: RequestOptions): Promise<T> {
    const body = typeof data === "string" ? data : new Uint8Array(data).buffer;
    const response = await this.request(method, path, options, body, "application/octet-stream");
    return await response.json() as T;
  }

  createDesktop(options: CreateDesktopOptions = {}, request?: RequestOptions): Promise<CreatedDesktop> {
    return this.json("POST", "/api/desktops", options, request);
  }

  async listDesktops(request?: RequestOptions): Promise<Desktop[]> {
    const result = await this.json<{ desktops: Desktop[] | null }>("GET", "/api/desktops", undefined, request);
    return result.desktops ?? [];
  }

  getDesktop(id: string, request?: RequestOptions): Promise<Desktop> {
    return this.json("GET", this.desktop(id), undefined, request);
  }

  destroyDesktop(id: string, request?: RequestOptions): Promise<StatusResult> {
    return this.json("DELETE", this.desktop(id), undefined, request);
  }

  pauseDesktop(id: string, request?: RequestOptions): Promise<StatusResult> {
    return this.json("POST", this.desktop(id) + "/pause", undefined, request);
  }

  resumeDesktop(id: string, request?: RequestOptions): Promise<StatusResult> {
    return this.json("POST", this.desktop(id) + "/resume", undefined, request);
  }

  exec(id: string, options: ExecOptions, request?: RequestOptions): Promise<ExecResult> {
    return this.json("POST", this.desktop(id) + "/exec", options, request);
  }

  listFiles(id: string, path = "/", request?: RequestOptions): Promise<FileInfo[]> {
    return this.json("GET", this.desktop(id) + "/files?" + new URLSearchParams({ path }), undefined, request);
  }

  async readFile(id: string, path: string, request?: RequestOptions): Promise<Uint8Array> {
    const response = await this.request("GET", this.desktop(id) + "/file?" + new URLSearchParams({ path }), request);
    return new Uint8Array(await response.arrayBuffer());
  }

  writeFile(id: string, path: string, data: string | Uint8Array, request?: RequestOptions): Promise<WriteResult> {
    return this.raw("PUT", this.desktop(id) + "/file?" + new URLSearchParams({ path }), data, request);
  }

  deleteFile(id: string, path: string, request?: RequestOptions): Promise<StatusResult> {
    return this.json("DELETE", this.desktop(id) + "/file?" + new URLSearchParams({ path }), undefined, request);
  }

  moveFile(id: string, source: string, destination: string, request?: RequestOptions): Promise<StatusResult> {
    return this.json("POST", this.desktop(id) + "/file/move", { from: source, to: destination }, request);
  }

  startRun(id: string, options: RunOptions, request?: RequestOptions): Promise<CreatedRun> {
    return this.json("POST", this.desktop(id) + "/runs", options, request);
  }

  async listRuns(id: string, request?: RequestOptions): Promise<RunStatus[]> {
    const result = await this.json<{ runs: RunStatus[] | null }>("GET", this.desktop(id) + "/runs", undefined, request);
    return result.runs ?? [];
  }

  getRun(id: string, runId: string, options: RequestOptions & { tail?: number } = {}): Promise<RunStatus> {
    return this.json("GET", this.desktop(id) + `/runs/${segment(runId)}?` + new URLSearchParams({ tail: String(options.tail ?? 0) }), undefined, options);
  }

  writeStdin(id: string, runId: string, data: string | Uint8Array, request?: RequestOptions): Promise<StatusResult> {
    return this.raw("POST", this.desktop(id) + `/runs/${segment(runId)}/stdin`, data, request);
  }

  killRun(id: string, runId: string, request?: RequestOptions): Promise<StatusResult> {
    return this.json("DELETE", this.desktop(id) + `/runs/${segment(runId)}`, undefined, request);
  }

  streamRun(id: string, runId: string, options: RequestOptions & { from?: number } = {}): AsyncGenerator<StreamEvent> {
    return this.stream(this.desktop(id) + `/runs/${segment(runId)}/stream`, options);
  }

  createSession(id: string, options: SessionOptions = {}, request?: RequestOptions): Promise<CreatedRun> {
    return this.json("POST", this.desktop(id) + "/sessions", options, request);
  }

  sessionInput(id: string, sessionId: string, data: string | Uint8Array, request?: RequestOptions): Promise<StatusResult> {
    return this.raw("POST", this.desktop(id) + `/sessions/${segment(sessionId)}/input`, data, request);
  }

  streamSession(id: string, sessionId: string, options: RequestOptions & { from?: number } = {}): AsyncGenerator<StreamEvent> {
    return this.stream(this.desktop(id) + `/sessions/${segment(sessionId)}/output`, options);
  }

  destroySession(id: string, sessionId: string, request?: RequestOptions): Promise<StatusResult> {
    return this.json("DELETE", this.desktop(id) + `/sessions/${segment(sessionId)}`, undefined, request);
  }

  private async *stream(path: string, options: RequestOptions & { from?: number }): AsyncGenerator<StreamEvent> {
    const offset = options.from ?? 0;
    if (!Number.isSafeInteger(offset) || offset < 0) throw new TypeError("from must be a non-negative integer");
    const response = await this.request("GET", path + "?" + new URLSearchParams({ from: String(offset) }), options, undefined, undefined, true);
    if (response.headers.get("Content-Type")?.split(";", 1)[0]?.trim() !== "text/event-stream" || !response.body) {
      await response.body?.cancel();
      throw new TypeError("expected a text/event-stream response");
    }
    const reader = response.body.getReader();
    const decoder = new TextDecoder("utf-8", { fatal: true });
    let buffer = "";
    let data: string[] = [];
    try {
      for (;;) {
        const { value, done } = await reader.read();
        buffer += decoder.decode(value, { stream: !done });
        for (;;) {
          const newline = /\r\n|\r|\n/.exec(buffer);
          if (!newline || (!done && newline[0] === "\r" && newline.index === buffer.length - 1)) break;
          const line = buffer.slice(0, newline.index);
          buffer = buffer.slice(newline.index + newline[0].length);
          if (line.startsWith("data:")) {
            const part = line.slice(5);
            data.push(part.startsWith(" ") ? part.slice(1) : part);
          } else if (line === "" && data.length) {
            const event: unknown = JSON.parse(data.join("\n"));
            data = [];
            if (!event || typeof event !== "object" || !("out" in event || "exit" in event)) {
              throw new TypeError("invalid warmbox stream event");
            }
            yield event as StreamEvent;
            if ("exit" in event) return;
          }
        }
        if (done) throw new Error("warmbox stream ended before an exit event");
      }
    } finally {
      try { await reader.cancel(); } catch {}
      reader.releaseLock();
    }
  }
}
