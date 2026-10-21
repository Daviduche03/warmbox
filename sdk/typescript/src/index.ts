import type {
  ClientOptions, CreatedDesktop, CreatedRun, CreateDesktopOptions, Desktop,
  DesktopList, ErrorDetails, ExecOptions, ExecResult, FileInfo, HistoryEntry,
  ListOptions, ProcessInfo, RequestOptions, RunOptions, RunStatus,
  SessionOptions, Snapshot, SnapshotList, StatusResult, StreamEvent, Volume,
  VolumeList, WriteResult,
} from "./types.js";

export type * from "./types.js";

export class APIError extends Error {
  /** The machine-readable error slug, e.g. `desktop_not_ready`. */
  readonly code?: string;
  /** Extra context for the failure; `retryable` marks transient errors. */
  readonly details?: ErrorDetails;
  /** The request id echoed by the daemon, for support and log correlation. */
  readonly requestId?: string;
  /** The human-readable message the daemon sent, without the HTTP prefix. */
  readonly serverMessage: string;

  constructor(public readonly status: number, public readonly body: string, requestId?: string) {
    let message = body.trim();
    let code: string | undefined;
    let details: ErrorDetails | undefined;
    let serverMessage = message;
    try {
      const data: unknown = JSON.parse(body);
      if (data && typeof data === "object") {
        const envelope = data as Record<string, unknown>;
        const error = envelope.error;
        const nested = error && typeof error === "object" ? error as Record<string, unknown> : undefined;
        if (typeof envelope.code === "string") code = envelope.code;
        else if (typeof nested?.code === "string") code = nested.code;
        if (typeof envelope.message === "string") serverMessage = envelope.message;
        else if (typeof nested?.message === "string") serverMessage = nested.message;
        else if (typeof error === "string" && error) serverMessage = error;
        const raw = nested?.details ?? envelope.details;
        if (raw && typeof raw === "object") details = raw as ErrorDetails;
        if (typeof envelope.requestId === "string" && envelope.requestId) requestId = envelope.requestId;
      }
    } catch {}
    super(`warmbox HTTP ${status}: ${serverMessage}`);
    this.name = "APIError";
    this.code = code;
    this.details = details;
    this.requestId = requestId ?? undefined;
    this.serverMessage = serverMessage;
  }

  /** True when the daemon marked the failure as transient. */
  get retryable(): boolean {
    return this.details?.retryable === true;
  }
}

function pageQuery(options: ListOptions): string {
  const params = new URLSearchParams();
  if (options.limit !== undefined) {
    if (!Number.isInteger(options.limit) || options.limit < 1 || options.limit > 200) {
      throw new TypeError("limit must be an integer between 1 and 200");
    }
    params.set("limit", String(options.limit));
  }
  if (options.cursor !== undefined) params.set("cursor", options.cursor);
  const query = params.toString();
  return query ? "?" + query : "";
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
    body?: BodyInit, contentType?: string, stream = false, extraHeaders?: Record<string, string>,
  ): Promise<Response> {
    const response = await fetch(this.baseUrl + path, {
      method, body, redirect: "manual",
      signal: options.signal ?? (stream ? undefined : AbortSignal.timeout(this.timeoutMs)),
      headers: {
        ...this.headers,
        Accept: stream ? "text/event-stream" : "application/json",
        ...(contentType ? { "Content-Type": contentType } : {}),
        ...extraHeaders,
      },
    });
    if (!response.ok) {
      throw new APIError(response.status, await response.text(), response.headers.get("X-Request-Id") ?? undefined);
    }
    return response;
  }

  private async json<T>(method: string, path: string, payload?: unknown, options?: RequestOptions, extraHeaders?: Record<string, string>): Promise<T> {
    const response = await this.request(method, path, options,
      payload === undefined ? undefined : JSON.stringify(payload),
      payload === undefined ? undefined : "application/json", false, extraHeaders);
    const body = await response.text();
    return (body ? JSON.parse(body) : undefined) as T;
  }

  private async raw<T>(method: string, path: string, data: string | Uint8Array, options?: RequestOptions): Promise<T> {
    const body = typeof data === "string" ? data : new Uint8Array(data).buffer;
    const response = await this.request(method, path, options, body, "application/octet-stream");
    return await response.json() as T;
  }

  createDesktop(options: CreateDesktopOptions = {}, request?: RequestOptions): Promise<CreatedDesktop> {
    const { idempotencyKey, ttlSeconds, ttl_seconds, ...rest } = options;
    const payload: Record<string, unknown> = { ...rest };
    const ttl = ttlSeconds ?? ttl_seconds;
    if (ttl !== undefined) payload.ttl_seconds = ttl;
    const headers = idempotencyKey === undefined ? undefined : { "Idempotency-Key": idempotencyKey };
    return this.json("POST", "/api/desktops", payload, request, headers);
  }

  /**
   * Lists the desktops visible to the caller. Accepts `limit` and `cursor`;
   * use {@link WarmboxClient.listDesktopsPage} to read the pagination metadata.
   */
  async listDesktops(options: ListOptions = {}): Promise<Desktop[]> {
    const result = await this.json<{ desktops: Desktop[] | null }>("GET", "/api/desktops" + pageQuery(options), undefined, options);
    return result.desktops ?? [];
  }

  /** Lists desktops with pagination; `pageInfo.nextCursor` feeds the next call. */
  listDesktopsPage(options: ListOptions = {}): Promise<DesktopList> {
    return this.json("GET", "/api/desktops" + pageQuery(options), undefined, options);
  }

  /** Lists the volumes visible to the caller. */
  async listVolumes(options: ListOptions = {}): Promise<Volume[]> {
    const result = await this.json<{ volumes: Volume[] | null }>("GET", "/api/volumes" + pageQuery(options), undefined, options);
    return result.volumes ?? [];
  }

  /** Lists volumes with pagination; `pageInfo.nextCursor` feeds the next call. */
  listVolumesPage(options: ListOptions = {}): Promise<VolumeList> {
    return this.json("GET", "/api/volumes" + pageQuery(options), undefined, options);
  }

  /** Lists the snapshots visible to the caller. */
  async listSnapshots(options: ListOptions = {}): Promise<Snapshot[]> {
    const result = await this.json<{ snapshots: Snapshot[] | null }>("GET", "/api/snapshots" + pageQuery(options), undefined, options);
    return result.snapshots ?? [];
  }

  /** Lists snapshots with pagination; `pageInfo.nextCursor` feeds the next call. */
  listSnapshotsPage(options: ListOptions = {}): Promise<SnapshotList> {
    return this.json("GET", "/api/snapshots" + pageQuery(options), undefined, options);
  }

  /** Extends the desktop's auto-stop deadline; `ttlSeconds` of 0 never expires. */
  extendDesktop(id: string, ttlSeconds: number, request?: RequestOptions): Promise<Desktop> {
    return this.json("POST", this.desktop(id) + "/extend", { ttl_seconds: ttlSeconds }, request);
  }

  /** Lists the desktop's command history, newest first. */
  async listHistory(id: string, request?: RequestOptions): Promise<HistoryEntry[]> {
    const result = await this.json<{ entries: HistoryEntry[] | null }>("GET", this.desktop(id) + "/history", undefined, request);
    return result.entries ?? [];
  }

  /** Lists the processes running inside the desktop. */
  async listProcesses(id: string, request?: RequestOptions): Promise<ProcessInfo[]> {
    const result = await this.json<{ processes: ProcessInfo[] | null }>("GET", this.desktop(id) + "/processes", undefined, request);
    return result.processes ?? [];
  }

  /** Alias of {@link WarmboxClient.extendDesktop}. */
  extend(id: string, ttlSeconds: number, request?: RequestOptions): Promise<Desktop> {
    return this.extendDesktop(id, ttlSeconds, request);
  }

  /** Alias of {@link WarmboxClient.listHistory}. */
  history(id: string, request?: RequestOptions): Promise<HistoryEntry[]> {
    return this.listHistory(id, request);
  }

  /** Alias of {@link WarmboxClient.listProcesses}. */
  processes(id: string, request?: RequestOptions): Promise<ProcessInfo[]> {
    return this.listProcesses(id, request);
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
