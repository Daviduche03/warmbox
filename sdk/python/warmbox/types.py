from typing import Literal, NotRequired, TypedDict


class CreateDesktopOptions(TypedDict, total=False):
    volume: str
    image: str
    cpus: int
    mem_mib: int
    allow: list[str]
    deny: list[str]


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
