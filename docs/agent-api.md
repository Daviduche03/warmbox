# Agent API — design (for review)

Status: **exec+e files, plus background/streaming runs and interactive sessions.**
`warmbox-agent` runs in the guest; the daemon proxies `POST /api/desktops/{id}/exec`,
the `/file(s)` routes, and the `/runs` + `/sessions` routes to it. Remaining:
screenshot/input (computer-use), and the SDKs.

Let an agent (or the host / an SDK) **run commands, move files, and (optionally)
drive the screen** inside a warmbox VM — without noVNC and without SSH keys.

Non-goals: not a job scheduler/queue, not multi-tenant policy, and not a
replacement for the GUI (it's an automation channel alongside it).

## Shape: a guest agent + a host proxy

```
SDK / agent ──HTTP──▶ warmbox daemon (host) ──▶ warmbox-agent (guest) ──▶ sh / fs / X
             token        (one endpoint, auth)      localhost only
```

- **`warmbox-agent`** (new, guest side): a small static binary inside the VM
  exposing a JSON/HTTP API on `127.0.0.1:7077` — `exec` + `files` first, then
  `screenshot`/`input`. It runs as the VM's root (the whole guest is root).
- **Daemon proxy**: the host daemon exposes `/api/desktops/{id}/…` and forwards to
  the guest agent over the same dial path as VNC (vfkit: the guest IP; QEMU: a
  host-forward). Agents talk to **one** endpoint; the daemon reaches the guest and
  enforces auth.
- **Why not SSH:** key management, another daemon, and clunkier file transfer for
  an SDK. SSH can be added later for humans.

## Transport
- `Backend` learns to forward a **second guest port** (the agent) the way it
  forwards VNC: vfkit dials the guest IP; QEMU adds a second `hostfwd`.
  `Instance` gains `AgentAddr`.
- The agent binds **localhost only**; the daemon proxies it. No new network
  exposure.

## API v1 (host daemon)
Everything is gated by the existing daemon token.

### Exec
```
POST /api/desktops/{id}/exec
  { "cmd": "go test ./...", "cwd": "/workspace", "env": {"K":"V"},
    "timeout_ms": 60000, "stdin": "" }
→ 200 { "exit": 0, "stdout": "...", "stderr": "...", "duration_ms": 1234,
        "timed_out": false }
```
- runs `sh -c <cmd>` by default; `argv: [...]` for no-shell.
- output cap + timeout (process group killed on timeout).

### Files
```
GET    /api/desktops/{id}/files?path=/workspace          → [{name,type,size,mode,mtime}]
GET    /api/desktops/{id}/file?path=/workspace/a.txt     → bytes (Range supported)
PUT    /api/desktops/{id}/file?path=…                    → write bytes
DELETE /api/desktops/{id}/file?path=…
POST   /api/desktops/{id}/file/move  {from,to}
```
- rooted at a configurable **agent root** (default `/`; recommend `/workspace` or
  the volume). Path-traversal protected.

### Screen / input (computer-use) — phase 3
```
GET  /api/desktops/{id}/screenshot                       → PNG
POST /api/desktops/{id}/input  {type:"text"|"key"|"move"|"click", …}
```
- implemented in the guest with `xwd` (+ an X image tool) and `xdotool`.

## Guest agent internals
- Single Go binary, `CGO_ENABLED=0`, a few MB, baked into the base image.
- Exec: `sh -c`, per-request timeout + output cap, process-group kill.
- Files: rooted, `filepath.Clean` + root-prefix check.
- Binds `127.0.0.1:7077` (or a unix socket at `/run/warmbox-agent.sock`), started
  by the guest `init`.

## Security posture
- The guest agent is **root inside the VM** (the VM is the trust boundary; the
  agent is not a sandbox).
- Host API stays single-user + token-gated; no TLS yet (see roadmap).
- File root defaults to a scoped dir (`/workspace`/the volume), not `/`.

## SDKs
Thin clients over the host API: Go first, then Python/TypeScript.
`client.Exec(id, cmd)`, `client.WriteFile(id, path, bytes)`, `client.Screenshot(id)`.

## Phasing
1. **Exec (one-shot) + files (list/read/write/delete/move).** The 80% for agents.
2. **Streaming + background runs, and sessions.** ✅ landed.
3. **Screenshot + input.** Computer-use.
4. **SDKs + a small jobs/tasks layer.**

## Runs (background + streaming exec) — landed

`exec` is one-shot and bounded; `runs` are for anything that outlives a call.
The daemon proxies these to the guest agent (paths are the same, prefixed with
`/api/desktops/{id}`):

```
POST   /runs                 {cmd|argv, cwd, env, shell, interactive, stdin, timeout_ms}
                             -> {id}                     (starts, returns immediately)
GET    /runs                 -> {runs:[status]}
GET    /runs/{id}?tail=N     -> status (+ last N bytes of output)
GET    /runs/{id}/stream?from=N -> SSE: data:{"out":"…"}* then data:{"exit":{…}}
POST   /runs/{id}/stdin      -> write stdin
DELETE /runs/{id}            -> kill the process group
```

Output is buffered (4 MiB of scrollback) so a client can attach late and still
read the tail; `from=N` resumes a stream at an offset. A build that used to hit
the 60s exec cap now runs for as long as it needs with live output.

## Sessions — landed

A session is a run of an interactive shell, so `cwd`/env/exports **persist
across inputs** — the thing one-shot exec could never do.

```
POST /sessions               -> {id}            (interactive shell)
POST /sessions/{id}/input    -> write a shell line (newline appended)
GET  /sessions/{id}/output   -> SSE stream
DELETE /sessions/{id}        -> kill
```

Both are scoped to the desktop (and therefore to the desktop's workspace): the
daemon proxies them through the same `visibleVM` check as `exec`, and no new
database rows are written — runs live and die with the guest.

## Decisions (phase 1)
- **Transport:** HTTP — `warmbox-agent` binds the guest interface `:7077`; the
  daemon proxies to it (vfkit: guest IP; QEMU: a second `hostfwd`). The guest net
  is NAT, so only the host can reach it.
- **Root:** `/` by default (set `WARMBOX_AGENT_ROOT` to scope it). Path traversal
  is rejected.
- **Ship always:** the agent is baked into the base image and started by `init`.
- **Auth:** the host API keeps the daemon token; the agent itself is unauthenticated
  because the VM's NAT network is the boundary (single-user). Revisit for fleets.

## Roadmap

### exec — make it agent-grade
Today `exec` is one-shot. Planned, in priority order:

**Tier 1**
- ✅ **Persistent sessions** — a long-lived shell per desktop so `cwd`/env/exports
  persist across calls. `POST /sessions`, `POST /sessions/{id}/input`,
  `GET /sessions/{id}/output`, `DELETE /sessions/{id}`.
- ✅ **Background + streaming exec** — every run returns an `id` immediately, with a
  live SSE output stream and status/kill. `POST /runs → {id}`,
  `GET /runs/{id}/stream`, `DELETE /runs/{id}`.
- ✅ **Send stdin to a running process** — `POST /runs/{id}/stdin`, and shell lines
  to a session.

**Tier 2**
- `tty: true` (correct output for tools that check `isatty`).
- shell selection (`sh`/`bash`).
- binary-safe output (`encoding: "base64"`).
- richer result: `signal`, `truncated`, `stdout_bytes`/`stderr_bytes`.
- per-desktop default `cwd`/`env`.

**Tier 3**
- concurrency cap / queue per VM (no fork-bombs).
- cgroup resource limits (memory/CPU/pids).
- cancel on disconnect; an audit log of execs; run as a non-root user.

### computer-use (later)
- `GET /screenshot` + `POST /input` (guest `scrot` + `xdotool`), for operating
  arbitrary GUI windows. For web tasks, prefer CDP or `exec` — pixels are the
  slow path.

### SDKs
- Go first, then Python / TypeScript.

## Still open
- Screenshot source: guest `scrot` vs grabbing the VNC framebuffer in the daemon.
- Input API: an actions DSL (click/type/scroll) vs raw events.
- Per-VM agent tokens (only needed if the guest network becomes reachable by others).
