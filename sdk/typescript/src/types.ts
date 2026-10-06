export interface ClientOptions {
  baseUrl?: string;
  token: string;
  workspaceId?: string;
  timeoutMs?: number;
}

export interface RequestOptions {
  signal?: AbortSignal;
}

export interface CreateDesktopOptions {
  volume?: string;
  image?: string;
  cpus?: number;
  mem_mib?: number;
  allow?: string[];
  deny?: string[];
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
