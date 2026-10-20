# warmbox for Python

A synchronous, typed client for Python 3.11+. It uses the standard library and
talks to the host daemon, with an API token from **Settings → API tokens**.
Use the daemon's HTTPS address for a remote host, or its loopback address through
an SSH tunnel. Certificate verification stays enabled.

Install from PyPI (or from a checkout with `python -m pip install ./sdk/python`):

```sh
python -m pip install warmbox
```

## For agents

Set `WARMBOX_TOKEN` to your API token and `WARMBOX_WORKSPACE_ID` to your workspace
ID. `WARMBOX_URL` defaults to `http://127.0.0.1:7070` in this example.

```python
import os
from warmbox import WarmboxClient

client = WarmboxClient(os.getenv("WARMBOX_URL", "http://127.0.0.1:7070"),
                       token=os.environ["WARMBOX_TOKEN"],
                       workspace_id=os.environ["WARMBOX_WORKSPACE_ID"])
desktop = client.create_desktop({"image": "headless"})
try:
    result = client.exec(desktop["id"], {"cmd": "uname -a"})
    print(result["stdout"])
finally:
    client.destroy_desktop(desktop["id"])
```

## Commands, files and streams

Methods use the daemon's JSON field names. `exec` accepts `cmd` or `argv`, plus
`cwd`, `env`, `stdin` and `timeout_ms`. A command's nonzero exit code is returned
in the result; HTTP failures raise `APIError` with `status` and `body`.

```python
from contextlib import closing

client.write_file(desktop_id, "/home/input.bin", b"\x00\xff")
data = client.read_file(desktop_id, "/home/input.bin")
run = client.start_run(desktop_id, {"cmd": "uname -a"})
with closing(client.stream_run(desktop_id, run["id"])) as events:
    for event in events:
        if "out" in event:
            print(event["out"], end="", flush=True)
        else:
            print("exit:", event["exit"].get("exit"))
```

## Errors

The daemon returns a structured error envelope. `APIError` exposes it:

```python
try:
    client.exec(desktop_id, {"cmd": "uname -a"})
except APIError as error:
    print(error.status)        # 409
    print(error.code)          # "desktop_not_ready"
    print(error.server_message)  # "the desktop is still booting"
    print(error.details)       # {"retryable": True}
    print(error.request_id)    # "req_1a2b3c4d"
```

`code` is a stable snake_case slug, `details` is optional and may carry
`retryable: True` for transient failures, and `request_id` is echoed in the
`X-Request-Id` response header. `str(error)` stays human-readable and
`error.retryable` is a shortcut for `details["retryable"]`.

## Lifetimes, pagination and history

`create_desktop` accepts `ttl_seconds` (0 = never auto-stop) and an
`idempotency_key`; repeating a key returns the same desktop.

```python
desktop = client.create_desktop({"image": "headless", "ttl_seconds": 900, "idempotency_key": "job-42"})
client.extend_desktop(desktop["id"], 1800)
for entry in client.list_history(desktop["id"]):
    print(entry["command"], entry["exitCode"])
for process in client.list_processes(desktop["id"]):
    print(process["pid"], process["args"])
```

`list_desktops_page`, `list_volumes` and `list_snapshots` take `limit` (1-200)
and `cursor` and return the daemon's `pageInfo`:

```python
page = client.list_desktops_page(limit=50)
while page.get("pageInfo", {}).get("hasMore"):
    page = client.list_desktops_page(limit=50, cursor=page["pageInfo"]["nextCursor"])
```

`stream_run` and `stream_session` yield output events followed by an exit event.
They accept `from_offset` to resume at a byte offset; reconnecting is explicit.
A connection that ends without an exit event raises `ConnectionError`. Use
`closing` when you may stop reading early. Closing a stream leaves the command
running; `kill_run` or `destroy_session` stops it.

The client also provides `list_desktops`, `get_desktop`, `pause_desktop`,
`resume_desktop`, `list_files`, `delete_file`, `move_file`, `list_runs`,
`get_run(tail=...)`, `write_stdin`, `create_session`, `session_input` and
`destroy_session`. Sessions keep shell state between inputs; `session_input`
sends a shell line, while `write_stdin` sends raw bytes. `extend_desktop`,
`list_history` and `list_processes` cover a desktop's lifetime, command history
and running processes.

`timeout` defaults to 120 seconds for socket operations, including idle stream
reads. Use `timeout=None` for an unbounded wait. It is separate from the guest's
`timeout_ms`. Requests are not retried and redirects are rejected, so a desktop
creation is never replayed and credentials are not forwarded to another URL.

## Development

```sh
cd sdk/python
python -m unittest discover -s tests -v
python -m pip install build mypy
python -m mypy --strict warmbox
python -m build
```

The tests use a local HTTP fixture; no VM is needed. The package version matches
the current warmbox release (`0.4.0`); update it alongside the next release.
