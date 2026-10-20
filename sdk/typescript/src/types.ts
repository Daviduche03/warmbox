export interface ClientOptions {
  baseUrl?: string;
  token: string;
  workspaceId?: string;
  timeoutMs?: number;
}

export interface RequestOptions {
  signal?: AbortSignal;
}

export interface ListOptions extends RequestOptions {
  limit?: number;
  cursor?: string;
}

export interface PageInfo {
  nextCursor?: string | null;
  hasMore?: boolean;
  limit?: number;
}

export interface DesktopList {
  desktops: Desktop[];
  pageInfo?: PageInfo;
}

export interface Volume {
  name: string;
  size?: number;
  chunk_size?: number;
  created?: string;
  created_at?: string;
  updated_at?: string;
  remote?: string;
  from?: string;
  workspace_id?: string;
}

export interface VolumeList {
  volumes: Volume[];
  pageInfo?: PageInfo;
}

export interface Snapshot {
  id: string;
  volume?: string;
  size?: number;
  chunk_size?: number;
  created_at?: string;
  workspace_id?: string;
}

export interface SnapshotList {
  snapshots: Snapshot[];
  pageInfo?: PageInfo;
}

export interface HistoryEntry {
  id: string;
  command: string;
  startedAt: string;
  endedAt: string;
  exitCode: number;
  output: string;
}

export interface ProcessInfo {
  pid: number;
  ppid: number;
  user: string;
  etime: string;
  args: string;
}

export interface ErrorDetails {
  retryable?: boolean;
  [key: string]: unknown;
}

export interface CreateDesktopOptions {
  volume?: string;
  image?: string;
  cpus?: number;
  mem_mib?: number;
  allow?: string[];
  deny?: string[];
  /** Auto-stop delay in seconds; 0 means the desktop never auto-stops. */
  ttlSeconds?: number;
  /** Daemon-side spelling of `ttlSeconds`; both are accepted. */
  ttl_seconds?: number;
  /** Key that makes a repeated create return the same desktop. */
  idempotencyKey?: string;
}

export interface CreatedDesktop {
  id: string;
  vnc?: string;
  ws?: string;
  volume?: string;
  headless?: boolean;
}

export interface Desktop {
  id: string;
  state: "booting" | "ready" | "busy" | "paused" | "dead";
  started: string;
  guest_ip?: string;
  volume?: string;
  workspace?: string;
  headless?: boolean;
  allow?: string[];
  deny?: string[];
}

export interface ExecOptions {
  cmd?: string;
  argv?: string[];
  cwd?: string;
  env?: Record<string, string>;
  timeout_ms?: number;
  stdin?: string;
}

export interface ExecResult {
  exit: number;
  stdout: string;
  stderr: string;
  duration_ms: number;
  timed_out: boolean;
}

export interface FileInfo {
  name: string;
  type: "file" | "dir" | "other";
  size: number;
  mode: string;
  mtime: string;
}

export interface RunOptions extends ExecOptions {
  shell?: string;
  interactive?: boolean;
  max_bytes?: number;
}

export interface SessionOptions {
  cwd?: string;
  env?: Record<string, string>;
  shell?: string;
}

export interface CreatedRun {
  id: string;
  running?: boolean;
}

export interface RunStatus {
  id: string;
  running: boolean;
  started: string;
  output_bytes: number;
  output?: string;
  truncated?: boolean;
  exit?: number;
  duration_ms?: number;
  signal?: string;
  timed_out?: boolean;
}

export type StreamEvent = { out: string; exit?: never } | { exit: RunStatus; out?: never };

export interface StatusResult {
  status: string;
  id?: string;
}

export interface WriteResult {
  written: number;
}
