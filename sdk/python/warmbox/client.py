import json
from collections.abc import Generator
from email.message import Message
from http.client import HTTPResponse
from typing import IO, Any, cast
from urllib.error import HTTPError
from urllib.parse import quote, urlencode, urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener

from .types import (
    CreatedDesktop, CreatedRun, CreateDesktopOptions, Desktop, DesktopList,
    ErrorDetails, ExecOptions, ExecResult, FileInfo, HistoryEntry, PageInfo,
    ProcessInfo, RunOptions, RunStatus, SessionOptions, Snapshot, SnapshotList,
    StatusResult, StreamEvent, Volume, VolumeList, WriteResult,
)


class APIError(Exception):
    """A failed warmbox request.

    Carries the HTTP ``status``, the machine-readable ``code`` slug, the
    daemon's ``server_message``, the optional ``details`` mapping (which may
    include ``retryable``) and the ``request_id`` echoed by the daemon.
    """

    def __init__(self, status: int, body: str, request_id: str | None = None) -> None:
        self.status = status
        self.body = body
        self.code: str | None = None
        self.details: ErrorDetails | None = None
        self.request_id: str | None = request_id
        server_message = body.strip()
        try:
            data = json.loads(body)
        except ValueError:
            data = None
        if isinstance(data, dict):
            error = data.get("error")
            nested = error if isinstance(error, dict) else {}
            code = data.get("code")
            if not isinstance(code, str):
                code = nested.get("code")
            if isinstance(code, str):
                self.code = code
            message = data.get("message")
            if not isinstance(message, str):
                message = nested.get("message")
            if not isinstance(message, str) and isinstance(error, str):
                message = error
            if isinstance(message, str) and message:
                server_message = message
            details = nested.get("details")
            if details is None:
                details = data.get("details")
            if isinstance(details, dict):
                self.details = cast(ErrorDetails, details)
            echoed = data.get("requestId")
            if isinstance(echoed, str) and echoed:
                self.request_id = echoed
        self.server_message = server_message
        super().__init__(f"warmbox HTTP {status}: {server_message}")

    @property
    def retryable(self) -> bool:
        """True when the daemon marked the failure as transient."""
        return bool(self.details and self.details.get("retryable"))


class _NoRedirect(HTTPRedirectHandler):
    def redirect_request(
        self, req: Request, fp: IO[bytes], code: int, msg: str,
        headers: Message, newurl: str,
    ) -> None:
        return None


def _segment(value: str) -> str:
    if not value or value in (".", ".."):
        raise ValueError("a non-empty resource ID is required")
    return quote(value, safe="")


def _page_query(limit: int | None, cursor: str | None) -> str:
    params: dict[str, str] = {}
    if limit is not None:
        if isinstance(limit, bool) or not isinstance(limit, int) or not 1 <= limit <= 200:
            raise ValueError("limit must be an integer between 1 and 200")
        params["limit"] = str(limit)
    if cursor is not None:
        params["cursor"] = cursor
    return "?" + urlencode(params) if params else ""


class WarmboxClient:
    def __init__(
        self, base_url: str = "http://127.0.0.1:7070", *, token: str,
        workspace_id: str | None = None, timeout: float | None = 120,
    ) -> None:
        url = urlsplit(base_url)
        if (url.scheme not in ("http", "https") or not url.hostname
                or url.username is not None or url.password is not None
                or url.query or url.fragment):
            raise ValueError("base_url must be an HTTP(S) URL without credentials, query or fragment")
        if not token:
            raise ValueError("an API token is required")
        if timeout is not None and timeout <= 0:
            raise ValueError("timeout must be positive or None")
        self._base_url = base_url.rstrip("/")
        self._timeout = timeout
        self._headers = {"Authorization": f"Bearer {token}"}
        if workspace_id is not None:
            self._headers["X-Workspace-ID"] = workspace_id
        self._opener = build_opener(_NoRedirect())

    def _open(
        self, method: str, path: str, *, data: bytes | None = None,
        content_type: str | None = None, accept: str = "application/json",
        headers: dict[str, str] | None = None,
    ) -> HTTPResponse:
        request_headers = {**self._headers, "Accept": accept}
        if content_type is not None:
            request_headers["Content-Type"] = content_type
        if headers:
            request_headers.update(headers)
        request = Request(self._base_url + path, data=data, headers=request_headers, method=method)
        try:
            return cast(HTTPResponse, self._opener.open(request, timeout=self._timeout))
        except HTTPError as error:
            with error:
                body = error.read().decode("utf-8", errors="replace")
            request_id = error.headers.get("X-Request-Id") if error.headers else None
            raise APIError(error.code, body, request_id) from None

    def _json(self, method: str, path: str, payload: Any = None,
              headers: dict[str, str] | None = None) -> Any:
        data = None if payload is None else json.dumps(payload).encode("utf-8")
        with self._open(method, path, data=data,
                        content_type="application/json" if data is not None else None,
                        headers=headers) as response:
            body = response.read()
        return json.loads(body) if body else None

    def _raw(self, method: str, path: str, data: str | bytes) -> StatusResult | WriteResult:
        body = data.encode("utf-8") if isinstance(data, str) else data
        with self._open(method, path, data=body, content_type="application/octet-stream") as response:
            return cast(StatusResult | WriteResult, json.load(response))

    def _desktop(self, desktop_id: str) -> str:
        return "/api/desktops/" + _segment(desktop_id)

    def create_desktop(self, options: CreateDesktopOptions | None = None, *,
                       idempotency_key: str | None = None) -> CreatedDesktop:
        payload = cast(dict[str, Any], dict(options or {}))
        if idempotency_key is None:
            in_options = payload.pop("idempotency_key", None)
            if isinstance(in_options, str):
                idempotency_key = in_options
        headers = ({"Idempotency-Key": idempotency_key}
                   if idempotency_key is not None else None)
        return cast(CreatedDesktop, self._json("POST", "/api/desktops", payload, headers))

    def list_desktops(self, *, limit: int | None = None, cursor: str | None = None) -> list[Desktop]:
        result = self._json("GET", "/api/desktops" + _page_query(limit, cursor))
        return cast(list[Desktop], result["desktops"] or [])

    def list_desktops_page(self, *, limit: int | None = None,
                           cursor: str | None = None) -> DesktopList:
        return cast(DesktopList, self._json("GET", "/api/desktops" + _page_query(limit, cursor)))

    def list_volumes(self, *, limit: int | None = None,
                     cursor: str | None = None) -> list[Volume]:
        result = self._json("GET", "/api/volumes" + _page_query(limit, cursor))
        return cast(list[Volume], result["volumes"] or [])

    def list_volumes_page(self, *, limit: int | None = None,
                          cursor: str | None = None) -> VolumeList:
        return cast(VolumeList, self._json("GET", "/api/volumes" + _page_query(limit, cursor)))

    def list_snapshots(self, *, limit: int | None = None,
                       cursor: str | None = None) -> list[Snapshot]:
        result = self._json("GET", "/api/snapshots" + _page_query(limit, cursor))
        return cast(list[Snapshot], result["snapshots"] or [])

    def list_snapshots_page(self, *, limit: int | None = None,
                            cursor: str | None = None) -> SnapshotList:
        return cast(SnapshotList, self._json("GET", "/api/snapshots" + _page_query(limit, cursor)))

    def extend_desktop(self, desktop_id: str, ttl_seconds: int) -> Desktop:
        return cast(Desktop, self._json("POST", self._desktop(desktop_id) + "/extend",
                                        {"ttl_seconds": ttl_seconds}))

    def list_history(self, desktop_id: str) -> list[HistoryEntry]:
        return cast(list[HistoryEntry], self._json("GET", self._desktop(desktop_id) + "/history")["entries"] or [])

    def list_processes(self, desktop_id: str) -> list[ProcessInfo]:
        return cast(list[ProcessInfo], self._json("GET", self._desktop(desktop_id) + "/processes")["processes"] or [])

    def extend(self, desktop_id: str, ttl_seconds: int) -> Desktop:
        return self.extend_desktop(desktop_id, ttl_seconds)

    def history(self, desktop_id: str) -> list[HistoryEntry]:
        return self.list_history(desktop_id)

    def processes(self, desktop_id: str) -> list[ProcessInfo]:
        return self.list_processes(desktop_id)

    def get_desktop(self, desktop_id: str) -> Desktop:
        return cast(Desktop, self._json("GET", self._desktop(desktop_id)))

    def destroy_desktop(self, desktop_id: str) -> StatusResult:
        return cast(StatusResult, self._json("DELETE", self._desktop(desktop_id)))

    def pause_desktop(self, desktop_id: str) -> StatusResult:
        return cast(StatusResult, self._json("POST", self._desktop(desktop_id) + "/pause"))

    def resume_desktop(self, desktop_id: str) -> StatusResult:
        return cast(StatusResult, self._json("POST", self._desktop(desktop_id) + "/resume"))

    def exec(self, desktop_id: str, options: ExecOptions) -> ExecResult:
        return cast(ExecResult, self._json("POST", self._desktop(desktop_id) + "/exec", options))

    def list_files(self, desktop_id: str, path: str = "/") -> list[FileInfo]:
        return cast(list[FileInfo], self._json("GET", self._desktop(desktop_id) + "/files?" + urlencode({"path": path})))

    def read_file(self, desktop_id: str, path: str) -> bytes:
        with self._open("GET", self._desktop(desktop_id) + "/file?" + urlencode({"path": path}),
                        accept="application/octet-stream") as response:
            return response.read()

    def write_file(self, desktop_id: str, path: str, data: str | bytes) -> WriteResult:
        return cast(WriteResult, self._raw("PUT", self._desktop(desktop_id) + "/file?" + urlencode({"path": path}), data))

    def delete_file(self, desktop_id: str, path: str) -> StatusResult:
        return cast(StatusResult, self._json("DELETE", self._desktop(desktop_id) + "/file?" + urlencode({"path": path})))

    def move_file(self, desktop_id: str, source: str, destination: str) -> StatusResult:
        return cast(StatusResult, self._json("POST", self._desktop(desktop_id) + "/file/move",
                                            {"from": source, "to": destination}))

    def start_run(self, desktop_id: str, options: RunOptions) -> CreatedRun:
        return cast(CreatedRun, self._json("POST", self._desktop(desktop_id) + "/runs", options))

    def list_runs(self, desktop_id: str) -> list[RunStatus]:
        return cast(list[RunStatus], self._json("GET", self._desktop(desktop_id) + "/runs")["runs"] or [])

    def get_run(self, desktop_id: str, run_id: str, *, tail: int = 0) -> RunStatus:
        return cast(RunStatus, self._json("GET", self._desktop(desktop_id) + "/runs/" + _segment(run_id)
                                         + "?" + urlencode({"tail": tail})))

    def write_stdin(self, desktop_id: str, run_id: str, data: str | bytes) -> StatusResult:
        return cast(StatusResult, self._raw("POST", self._desktop(desktop_id) + "/runs/" + _segment(run_id) + "/stdin", data))

    def kill_run(self, desktop_id: str, run_id: str) -> StatusResult:
        return cast(StatusResult, self._json("DELETE", self._desktop(desktop_id) + "/runs/" + _segment(run_id)))

    def stream_run(self, desktop_id: str, run_id: str, *, from_offset: int = 0) -> Generator[StreamEvent, None, None]:
        return self._stream(self._desktop(desktop_id) + "/runs/" + _segment(run_id) + "/stream", from_offset)

    def create_session(self, desktop_id: str, options: SessionOptions | None = None) -> CreatedRun:
        return cast(CreatedRun, self._json("POST", self._desktop(desktop_id) + "/sessions", options or {}))

    def session_input(self, desktop_id: str, session_id: str, data: str | bytes) -> StatusResult:
        return cast(StatusResult, self._raw("POST", self._desktop(desktop_id) + "/sessions/" + _segment(session_id) + "/input", data))

    def stream_session(self, desktop_id: str, session_id: str, *, from_offset: int = 0) -> Generator[StreamEvent, None, None]:
        return self._stream(self._desktop(desktop_id) + "/sessions/" + _segment(session_id) + "/output", from_offset)

    def destroy_session(self, desktop_id: str, session_id: str) -> StatusResult:
        return cast(StatusResult, self._json("DELETE", self._desktop(desktop_id) + "/sessions/" + _segment(session_id)))

    def _stream(self, path: str, from_offset: int) -> Generator[StreamEvent, None, None]:
        if isinstance(from_offset, bool) or not isinstance(from_offset, int) or not 0 <= from_offset < 1 << 63:
            raise ValueError("from_offset must be a non-negative 64-bit integer")
        with self._open("GET", path + "?" + urlencode({"from": from_offset}), accept="text/event-stream") as response:
            if response.headers.get_content_type() != "text/event-stream":
                raise ValueError("expected a text/event-stream response")
            lines: list[str] = []
            for raw in response:
                line = raw.decode("utf-8").rstrip("\r\n")
                if line.startswith("data:"):
                    value = line[5:]
                    lines.append(value[1:] if value.startswith(" ") else value)
                elif not line and lines:
                    event = json.loads("\n".join(lines))
                    lines.clear()
                    if not isinstance(event, dict) or not ("out" in event or "exit" in event):
                        raise ValueError("invalid warmbox stream event")
                    yield cast(StreamEvent, event)
                    if "exit" in event:
                        return
            raise ConnectionError("warmbox stream ended before an exit event")
