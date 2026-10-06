# warmbox for Python

A synchronous, typed client for Python 3.11+. It uses the standard library and
talks to the host daemon, with an API token from **Settings → API tokens**.
Use the daemon's HTTPS address for a remote host, or its loopback address through
an SSH tunnel. Certificate verification stays enabled.

Install from a checkout (the package is not published to PyPI yet):

```sh
python -m pip install ./sdk/python
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

`stream_run` and `stream_session` yield output events followed by an exit event.
They accept `from_offset` to resume at a byte offset; reconnecting is explicit.
A connection that ends without an exit event raises `ConnectionError`. Use
`closing` when you may stop reading early. Closing a stream leaves the command
running; `kill_run` or `destroy_session` stops it.

The client also provides `list_desktops`, `get_desktop`, `pause_desktop`,
`resume_desktop`, `list_files`, `delete_file`, `move_file`, `list_runs`,
`get_run(tail=...)`, `write_stdin`, `create_session`, `session_input` and
`destroy_session`. Sessions keep shell state between inputs; `session_input`
sends a shell line, while `write_stdin` sends raw bytes.

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
