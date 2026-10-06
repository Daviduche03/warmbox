# warmbox for TypeScript

A typed client for Node.js 18+, using built-in `fetch` with no runtime
dependencies. Use an API token from **Settings → API tokens** and the daemon's
HTTPS address for a remote host, or its loopback address through an SSH tunnel.
Keep tokens in server-side code.

Build an installable package from a checkout (it is not published to npm yet):

```sh
cd sdk/typescript
npm ci
npm pack
```

Install the resulting `warmbox-0.4.0.tgz` in your agent project with
`npm install /path/to/warmbox-0.4.0.tgz`.

## For agents

Set `WARMBOX_TOKEN` to your API token and `WARMBOX_WORKSPACE_ID` to your workspace
ID. `WARMBOX_URL` defaults to `http://127.0.0.1:7070` in this example.

```typescript
import { WarmboxClient } from "warmbox";

const client = new WarmboxClient({
  baseUrl: process.env.WARMBOX_URL ?? "http://127.0.0.1:7070",
  token: process.env.WARMBOX_TOKEN!, workspaceId: process.env.WARMBOX_WORKSPACE_ID!,
});
const desktop = await client.createDesktop({ image: "headless" });
try {
  console.log((await client.exec(desktop.id, { cmd: "uname -a" })).stdout);
} finally {
  await client.destroyDesktop(desktop.id);
}
```

## Commands, files and streams

Methods use the daemon's JSON field names. `exec` accepts `cmd` or `argv`, plus
`cwd`, `env`, `stdin` and `timeout_ms`. A command's nonzero exit code is returned
in the result; HTTP failures throw `APIError` with `status` and `body`.

```typescript
await client.writeFile(desktopId, "/home/input.bin", new Uint8Array([0, 255]));
const data = await client.readFile(desktopId, "/home/input.bin");
const run = await client.startRun(desktopId, { cmd: "uname -a" });
for await (const event of client.streamRun(desktopId, run.id)) {
  if (event.out !== undefined) process.stdout.write(event.out);
  else console.log("exit:", event.exit.exit);
}
```

`streamRun` and `streamSession` yield output events followed by an exit event.
They accept `{ from: byteOffset, signal }` to resume or cancel a stream.
Reconnecting is explicit. A connection that ends without an exit event throws.
Breaking the loop closes the connection and leaves the command running;
`killRun` or `destroySession` stops it.

The client also provides `listDesktops`, `getDesktop`, `pauseDesktop`,
`resumeDesktop`, `listFiles`, `deleteFile`, `moveFile`, `listRuns`,
`getRun(id, runId, { tail })`, `writeStdin`, `createSession`, `sessionInput` and
`destroySession`. Sessions keep shell state between inputs; `sessionInput`
sends a shell line, while `writeStdin` sends raw bytes.

Requests default to a 120-second deadline (`timeoutMs`), separate from the
guest's `timeout_ms`. Streams have no automatic deadline. Every method accepts
a request option with an `AbortSignal`; a supplied signal takes control of the
deadline. Requests are not retried and redirects are rejected, so desktop
creation is never replayed and credentials are not forwarded to another URL.

## Development

```sh
cd sdk/typescript
npm ci
npm test
npm run typecheck
npm pack
```

The tests use a local HTTP fixture; no VM is needed. The package version matches
the current warmbox release (`0.4.0`); update it alongside the next release.
