# Agent API — design (for review)

Status: **phase 1 landed — `exec` + `files`.** `warmbox-agent` runs in the guest
and the daemon proxies `POST /api/desktops/{id}/exec` and the `/file(s)` routes to
it. Remaining: streaming/background exec (phase 2), screenshot/input (phase 3),
SDKs (phase 4).

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
2. **Streaming + background exec** (SSE stream, `detach:true` → runId). For servers.
3. **Screenshot + input.** Computer-use.
4. **SDKs + a small jobs/tasks layer.**

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
- **Persistent sessions** — a long-lived shell per desktop so `cwd`/env/exports
  persist across calls (today every call is a fresh `sh -c` and loses state).
  `POST /sessions`, `POST /sessions/{id}/input`, `GET /sessions/{id}/output`,
  `DELETE /sessions/{id}`.
- **Background + streaming exec** — every exec returns a `runId` immediately, with
  a live output stream and status/kill/attach. Needed for anything outliving a
  call (dev servers, builds). `POST /exec → {runId}`,
  `GET /exec/{runId}/stream` (SSE), `DELETE /exec/{runId}`.
- **Send stdin to a running process** — prompts and interactivity.

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
