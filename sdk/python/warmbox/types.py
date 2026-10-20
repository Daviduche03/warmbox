from typing import Literal, NotRequired, TypedDict


class CreateDesktopOptions(TypedDict, total=False):
    volume: str
    image: str
    cpus: int
    mem_mib: int
    allow: list[str]
    deny: list[str]
    # Auto-stop delay in seconds; 0 means the desktop never auto-stops.
    ttl_seconds: int
    # Sent as the Idempotency-Key header rather than in the request body.
    idempotency_key: str


class ErrorDetails(TypedDict, total=False):
    retryable: bool


class PageInfo(TypedDict, total=False):
    nextCursor: str | None
    hasMore: bool
    limit: int


class DesktopList(TypedDict, total=False):
    desktops: list["Desktop"]
    pageInfo: PageInfo


class Volume(TypedDict, total=False):
    name: str
    size: int
    chunk_size: int
    created: str
    created_at: str
    updated_at: str
    remote: str
    workspace_id: str


class VolumeList(TypedDict, total=False):
    volumes: list[Volume]
    pageInfo: PageInfo


class Snapshot(TypedDict, total=False):
    id: str
    volume: str
    size: int
    chunk_size: int
    created_at: str
    workspace_id: str


class SnapshotList(TypedDict, total=False):
    snapshots: list[Snapshot]
    pageInfo: PageInfo


class HistoryEntry(TypedDict, total=False):
    id: str
    command: str
    startedAt: str
    endedAt: str
    exitCode: int
    output: str


class ProcessInfo(TypedDict, total=False):
    pid: int
    ppid: int
    user: str
    etime: str
    args: str


class CreatedDesktop(TypedDict):
    id: str
    vnc: NotRequired[str]
    ws: NotRequired[str]
    volume: NotRequired[str]
    headless: NotRequired[bool]


class Desktop(TypedDict):
    id: str
    state: Literal["booting", "ready", "busy", "paused", "dead"]
    started: str
    guest_ip: NotRequired[str]
    volume: NotRequired[str]
    workspace: NotRequired[str]
    headless: NotRequired[bool]
    allow: NotRequired[list[str]]
    deny: NotRequired[list[str]]


class ExecOptions(TypedDict, total=False):
    cmd: str
    argv: list[str]
    cwd: str
    env: dict[str, str]
    timeout_ms: int
    stdin: str


class ExecResult(TypedDict):
    exit: int
    stdout: str
    stderr: str
    duration_ms: int
    timed_out: bool


class FileInfo(TypedDict):
    name: str
    type: Literal["file", "dir", "other"]
    size: int
    mode: str
    mtime: str


class RunOptions(ExecOptions, total=False):
    shell: str
    interactive: bool
    max_bytes: int


class SessionOptions(TypedDict, total=False):
    cwd: str
    env: dict[str, str]
    shell: str


class CreatedRun(TypedDict):
    id: str
    running: NotRequired[bool]


class RunStatus(TypedDict):
    id: str
    running: bool
    started: str
    output_bytes: int
    output: NotRequired[str]
    truncated: NotRequired[bool]
    exit: NotRequired[int]
    duration_ms: NotRequired[int]
    signal: NotRequired[str]
    timed_out: NotRequired[bool]


class OutputEvent(TypedDict):
    out: str


class ExitEvent(TypedDict):
    exit: RunStatus


StreamEvent = OutputEvent | ExitEvent


class StatusResult(TypedDict):
    status: str
    id: NotRequired[str]


class WriteResult(TypedDict):
    written: int
