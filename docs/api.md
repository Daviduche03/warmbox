# The HTTP API

Everything the dashboard does, an agent or an SDK can do over HTTP. The API is
gated by the same session cookie and API tokens as the dashboard: see
[running.md](running.md) for how to sign in.

## The shape of an answer

Every answer is JSON with the same shape, so a client never has to guess
whether a body is a result or a failure.

A result:

```json
{ "ok": true, "id": "9f2c1ab4", "vnc": "/d/9f2c1ab4" }
```

A failure:

```json
{
  "ok": false,
  "type": "error",
  "status": 409,
  "code": "desktop_not_ready",
  "message": "the desktop is not ready yet",
  "error": {
    "code": "desktop_not_ready",
    "message": "the desktop is not ready yet",
    "status": 409,
    "details": { "retryable": true }
  },
  "requestId": "req_1a2b3c4d5e6f7a8b"
}
```

- `code` is stable and machine-readable: `invalid_json`, `invalid_limit`,
  `invalid_ttl`, `invalid_resources`, `unauthorized`, `forbidden`,
  `unknown_desktop`, `desktop_not_ready`, `desktop_hibernated`,
  `desktop_headless`, `volume_in_use`, `already_exists`, `idempotency_key_reused`,
  `agent_unreachable`, `port_unreachable`, and more. Branch on this, not on the
  message.
- `message` is a sentence for a human. It can change.
- `details` carries what a client needs to act — `retryable: true` means the
  same call will probably work in a moment.
- `requestId` is echoed in the `X-Request-Id` header too. Quote it in a bug
  report and it can be found in the daemon log.

## Lists are paged

`GET /api/desktops`, `GET /api/volumes` and `GET /api/snapshots` take:

- `?limit=N` — how many items to return, 1 to 200 (default 100).
- `?cursor=…` — where to carry on from, as given by the previous page.

and answer with a `pageInfo` beside the list:

```json
{ "ok": true, "desktops": [ … ], "pageInfo": { "nextCursor": "OWY0MmMxYWI0", "hasMore": true, "limit": 100 } }
```

`nextCursor` is empty and `hasMore` is false on the last page. A client that
ignores both gets every item, which is what the first version of the API
always did.

## Creating a desktop

`POST /api/desktops` takes:

```json
{ "volume": "mywork", "image": "xfce", "cpus": 4, "mem_mib": 4096, "ttl_seconds": 3600 }
```

- `ttl_seconds` — how long the desktop lives before it stops by itself. `0`
  (the default) means it never stops on its own. The deadline is kept in the
  catalog, so it survives a daemon restart.
- `Idempotency-Key: <your key>` — makes the create safe to retry. The same key
  returns the same desktop instead of a second one that costs twice. The same
  key with a *different* body is a mistake and answers `409`
  `idempotency_key_reused`.

`POST /api/desktops/{id}/extend` moves the deadline:

```json
{ "ttl_seconds": 7200 }
```

## The guest, over HTTP

The daemon proxies these to the agent inside the guest. They are the same
routes the [agent API](agent-api.md) describes, under the desktop:

| Route | What it does |
|---|---|
| `POST /api/desktops/{id}/exec` | Run a command |
| `GET /api/desktops/{id}/runs` | List background runs |
| `POST /api/desktops/{id}/sessions` | Open an interactive session |
| `GET /api/desktops/{id}/files` | List a directory |
| `GET`/`PUT`/`DELETE /api/desktops/{id}/file` | Read, write, delete a file |
| `GET /api/desktops/{id}/screenshot` | A PNG of the screen |
| `GET /api/desktops/{id}/screen` | The screen size |
| `POST /api/desktops/{id}/input` | Type, click, move |
| `GET /api/desktops/{id}/history` | What the guest has run |
| `GET /api/desktops/{id}/processes` | What is running now |

A call to a desktop that is still booting **waits** for it rather than
failing: the guest's agent comes up a moment after the VM does, so "create,
then immediately run something" works without a retry loop.

## SDKs

- Python: `pip install warmbox`
- TypeScript: a checkout for now — see [sdk/typescript](../sdk/typescript).

Both speak the shapes above, and both raise an error that carries the status,
the `code`, the message and whether the failure was `retryable`.
