import assert from "node:assert/strict";
import { once } from "node:events";
import { createServer } from "node:http";
import test from "node:test";
import { APIError, WarmboxClient } from "../dist/index.js";

async function fixture(t) {
  const state = { requests: [], status: 200, response: { id: "vm1" }, contentType: "application/json" };
  const server = createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    state.requests.push({ method: req.method, url: new URL(req.url, "http://localhost"), headers: req.headers, body: Buffer.concat(chunks) });
    res.writeHead(state.status, { "Content-Type": state.contentType,
      ...(state.status === 302 ? { Location: `http://127.0.0.1:${server.address().port}/leak` } : {}) });
    if (state.stream) return state.stream(res);
    res.end(Buffer.isBuffer(state.response) ? state.response : JSON.stringify(state.response));
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  t.after(async () => {
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
  });
  state.baseUrl = `http://127.0.0.1:${server.address().port}`;
  state.client = new WarmboxClient({ baseUrl: state.baseUrl, token: "test-token", workspaceId: "workspace-1" });
  return state;
}

test("desktop lifecycle and exec use the REST shapes and workspace auth", async t => {
  const f = await fixture(t);
  const c = f.client;
  assert.deepEqual(await c.createDesktop({ image: "headless", mem_mib: 512 }), { id: "vm1" });
  assert.deepEqual(JSON.parse(f.requests.at(-1).body), { image: "headless", mem_mib: 512 });
  f.response = { desktops: null };
  assert.deepEqual(await c.listDesktops(), []);
  f.response = { desktops: [{ id: "vm1", headless: true }] };
  assert.equal((await c.listDesktops())[0].id, "vm1");
  f.response = { id: "vm1", headless: true };
  assert.equal((await c.getDesktop("vm1")).headless, true);
  await c.pauseDesktop("vm1");
  await c.resumeDesktop("vm1");
  await c.destroyDesktop("vm1");
  f.response = { exit: 7, stdout: "out", stderr: "err", duration_ms: 2, timed_out: false };
  const options = { argv: ["sh", "-c", "exit 7"], env: { K: "V" }, cwd: "/home", stdin: "input", timeout_ms: 2000 };
  assert.deepEqual(await c.exec("vm1", options), f.response);
  assert.deepEqual(JSON.parse(f.requests.at(-1).body), options);
  assert.deepEqual(f.requests.map(r => [r.method, r.url.pathname]), [
    ["POST", "/api/desktops"], ["GET", "/api/desktops"], ["GET", "/api/desktops"],
    ["GET", "/api/desktops/vm1"], ["POST", "/api/desktops/vm1/pause"],
    ["POST", "/api/desktops/vm1/resume"], ["DELETE", "/api/desktops/vm1"],
    ["POST", "/api/desktops/vm1/exec"],
  ]);
  for (const request of f.requests) {
    assert.equal(request.headers.authorization, "Bearer test-token");
    assert.equal(request.headers["x-workspace-id"], "workspace-1");
  }
  assert.equal(f.requests.at(-1).headers["content-type"], "application/json");
});

test("binary file round trips and special paths", async t => {
  const f = await fixture(t);
  const path = "/home/ü &?#.bin";
  const binary = new Uint8Array([0, 255, 128, 1]);
  f.response = { written: binary.length };
  assert.deepEqual(await f.client.writeFile("vm1", path, binary), f.response);
  assert.deepEqual(f.requests.at(-1).body, Buffer.from(binary));
  f.response = Buffer.from(binary);
  f.contentType = "application/octet-stream";
  assert.deepEqual(await f.client.readFile("vm1", path), binary);
  f.response = [{ name: "ü &?#.bin", type: "file", size: 4, mode: "-rw-r--r--", mtime: "2026-10-06T00:00:00Z" }];
  f.contentType = "application/json";
  assert.deepEqual(await f.client.listFiles("vm1", path), f.response);
  f.response = { status: "deleted" };
  await f.client.deleteFile("vm1", path);
  for (const request of f.requests) assert.equal(request.url.searchParams.get("path"), path);
  await f.client.moveFile("vm1", path, "/home/new");
  assert.deepEqual(JSON.parse(f.requests.at(-1).body), { from: path, to: "/home/new" });
  f.response = { written: 2 };
  await f.client.writeFile("vm1", path, "ü");
  assert.deepEqual(f.requests.at(-1).body, Buffer.from("ü"));
});

test("run and session routes, raw stdin and tail", async t => {
  const f = await fixture(t);
  const c = f.client;
  await c.startRun("vm1", { cmd: "cat", interactive: true, max_bytes: 100 });
  assert.deepEqual(JSON.parse(f.requests.at(-1).body), { cmd: "cat", interactive: true, max_bytes: 100 });
  f.response = { runs: null };
  assert.deepEqual(await c.listRuns("vm1"), []);
  f.response = { id: "run1", running: false, output_bytes: 0, exit: 1 };
  assert.equal((await c.getRun("vm1", "run1", { tail: 99 })).exit, 1);
  assert.equal(f.requests.at(-1).url.searchParams.get("tail"), "99");
  await c.writeStdin("vm1", "run1", new Uint8Array([0, 1, 255]));
  assert.deepEqual(f.requests.at(-1).body, Buffer.from([0, 1, 255]));
  await c.killRun("vm1", "run1");
  await c.createSession("vm1", { cwd: "/home", shell: "/bin/sh" });
  assert.deepEqual(JSON.parse(f.requests.at(-1).body), { cwd: "/home", shell: "/bin/sh" });
  await c.sessionInput("vm1", "session1", "pwd");
  assert.equal(f.requests.at(-1).body.toString(), "pwd");
  await c.destroySession("vm1", "session1");
  assert.deepEqual(f.requests.map(r => [r.method, r.url.pathname]), [
    ["POST", "/api/desktops/vm1/runs"], ["GET", "/api/desktops/vm1/runs"],
    ["GET", "/api/desktops/vm1/runs/run1"], ["POST", "/api/desktops/vm1/runs/run1/stdin"],
    ["DELETE", "/api/desktops/vm1/runs/run1"], ["POST", "/api/desktops/vm1/sessions"],
    ["POST", "/api/desktops/vm1/sessions/session1/input"], ["DELETE", "/api/desktops/vm1/sessions/session1"],
  ]);
});

test("stream yields live output, handles byte boundaries and preserves auth", { timeout: 5_000 }, async t => {
  const f = await fixture(t);
  f.contentType = "text/event-stream; charset=utf-8";
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  t.after(() => release());
  f.stream = async res => {
    const data = Buffer.from(': heartbeat\r\nevent: output\r\ndata: {"out":\r\ndata: "héllo"}\r\n\r\n');
    for (let i = 0; i < data.length; i++) {
      res.write(data.subarray(i, i + 1));
      await new Promise(resolve => setImmediate(resolve));
    }
    await gate;
    res.end('data: {"exit":{"id":"run1","running":false,"exit":7}}\n\n');
  };
  const events = f.client.streamRun("vm1", "run1", { from: 42 });
  assert.deepEqual((await events.next()).value, { out: "héllo" });
  release();
  assert.equal((await events.next()).value.exit.exit, 7);
  assert.equal((await events.next()).done, true);
  const request = f.requests.at(-1);
  assert.equal(request.url.pathname, "/api/desktops/vm1/runs/run1/stream");
  assert.equal(request.url.searchParams.get("from"), "42");
  assert.equal(request.headers.accept, "text/event-stream");
  assert.equal(request.headers.authorization, "Bearer test-token");
  assert.equal(request.headers["x-workspace-id"], "workspace-1");
});

test("breaking a session stream closes the connection", { timeout: 5_000 }, async t => {
  const f = await fixture(t);
  f.contentType = "text/event-stream";
  let closed;
  const disconnected = new Promise(resolve => { closed = resolve; });
  f.stream = res => {
    res.on("close", () => closed());
    res.write('data: {"out":"ready"}\n\n');
  };
  for await (const event of f.client.streamSession("vm1", "session1")) {
    assert.deepEqual(event, { out: "ready" });
    break;
  }
  await disconnected;
  assert.equal(f.requests.at(-1).url.pathname, "/api/desktops/vm1/sessions/session1/output");
});

test("incomplete and malformed streams fail", async t => {
  const f = await fixture(t);
  f.contentType = "text/event-stream";
  for (const [body, pattern] of [
    ['data: {"out":"partial"}\n\n', /before an exit event/],
    ["data: nope\n\n", /JSON|Unexpected/],
    ["data: []\n\n", /invalid warmbox stream event/],
  ]) {
    f.stream = res => res.end(body);
    await assert.rejects(async () => {
      for await (const event of f.client.streamRun("vm1", "run1")) void event;
    }, pattern);
  }
  f.stream = undefined;
  f.contentType = "application/json";
  await assert.rejects(f.client.streamRun("vm1", "run1").next(), /text\/event-stream/);
});

test("HTTP failures retain status and body, redirects are not followed", async t => {
  const f = await fixture(t);
  for (const [status, response, message] of [
    [404, { error: "unknown desktop" }, "unknown desktop"],
    [401, Buffer.from("unauthorized\n"), "unauthorized"],
    [502, Buffer.from("<html>bad gateway</html>"), "bad gateway"],
  ]) {
    f.status = status;
    f.response = response;
    await assert.rejects(f.client.getDesktop("vm1"), error => {
      assert.ok(error instanceof APIError);
      assert.equal(error.status, status);
      assert.ok(error.message.includes(message));
      return true;
    });
  }
  f.status = 302;
  const before = f.requests.length;
  await assert.rejects(f.client.getDesktop("vm1"), error => error instanceof APIError && error.status === 302);
  assert.equal(f.requests.length, before + 1);
});

test("structured error envelopes become typed APIError fields", async t => {
  const f = await fixture(t);
  f.status = 409;
  f.response = {
    ok: false,
    type: "error",
    status: 409,
    code: "desktop_not_ready",
    message: "the desktop is still booting",
    error: {
      code: "desktop_not_ready",
      message: "the desktop is still booting",
      status: 409,
      details: { retryable: true },
    },
    requestId: "req_1a2b3c4d",
  };
  await assert.rejects(f.client.exec("vm1", { cmd: "true" }), error => {
    assert.ok(error instanceof APIError);
    assert.equal(error.status, 409);
    assert.equal(error.code, "desktop_not_ready");
    assert.equal(error.serverMessage, "the desktop is still booting");
    assert.ok(error.message.includes("the desktop is still booting"));
    assert.deepEqual(error.details, { retryable: true });
    assert.equal(error.retryable, true);
    assert.equal(error.requestId, "req_1a2b3c4d");
    return true;
  });

  f.status = 400;
  f.response = {
    ok: false,
    type: "error",
    status: 400,
    code: "invalid_json",
    message: "the request body is not valid json",
    error: { code: "invalid_json", message: "the request body is not valid json", status: 400 },
    requestId: "req_9999",
  };
  await assert.rejects(f.client.getDesktop("vm1"), error => {
    assert.equal(error.code, "invalid_json");
    assert.equal(error.details, undefined);
    assert.equal(error.retryable, false);
    assert.equal(error.requestId, "req_9999");
    return true;
  });
});

test("create sends ttl_seconds and the idempotency key header", async t => {
  const f = await fixture(t);
  await f.client.createDesktop({ image: "headless", ttlSeconds: 900, idempotencyKey: "job-42" });
  assert.deepEqual(JSON.parse(f.requests.at(-1).body), { image: "headless", ttl_seconds: 900 });
  assert.equal(f.requests.at(-1).headers["idempotency-key"], "job-42");
  await f.client.createDesktop({ image: "headless" });
  assert.deepEqual(JSON.parse(f.requests.at(-1).body), { image: "headless" });
  assert.equal(f.requests.at(-1).headers["idempotency-key"], undefined);
});

test("pagination sends limit and cursor and exposes pageInfo", async t => {
  const f = await fixture(t);
  f.response = { desktops: [{ id: "vm1" }], pageInfo: { nextCursor: "c2", hasMore: true, limit: 50 } };
  const page = await f.client.listDesktopsPage({ limit: 50 });
  assert.equal(f.requests.at(-1).url.searchParams.get("limit"), "50");
  assert.equal(f.requests.at(-1).url.searchParams.get("cursor"), null);
  assert.deepEqual(page.pageInfo, { nextCursor: "c2", hasMore: true, limit: 50 });
  assert.equal(page.desktops[0].id, "vm1");
  await f.client.listDesktopsPage({ limit: 50, cursor: "c2" });
  assert.equal(f.requests.at(-1).url.searchParams.get("cursor"), "c2");

  f.response = { volumes: [{ name: "vol1" }], pageInfo: { nextCursor: "", hasMore: false, limit: 100 } };
  const volumes = await f.client.listVolumesPage({ limit: 10, cursor: "v1" });
  assert.equal(f.requests.at(-1).url.pathname, "/api/volumes");
  assert.equal(f.requests.at(-1).url.searchParams.get("limit"), "10");
  assert.equal(f.requests.at(-1).url.searchParams.get("cursor"), "v1");
  assert.equal(volumes.volumes[0].name, "vol1");
  assert.equal(volumes.pageInfo.hasMore, false);
  assert.deepEqual(await f.client.listVolumes({ limit: 10 }), [{ name: "vol1" }]);

  f.response = { snapshots: [{ id: "snap1" }], pageInfo: { nextCursor: null, hasMore: false, limit: 100 } };
  const snapshots = await f.client.listSnapshotsPage({ limit: 5 });
  assert.equal(f.requests.at(-1).url.pathname, "/api/snapshots");
  assert.equal(f.requests.at(-1).url.searchParams.get("limit"), "5");
  assert.equal(snapshots.snapshots[0].id, "snap1");
  assert.equal(snapshots.pageInfo.nextCursor, null);
  assert.deepEqual(await f.client.listSnapshots({ limit: 5 }), [{ id: "snap1" }]);

  assert.throws(() => f.client.listDesktopsPage({ limit: 0 }), /between 1 and 200/);
  assert.throws(() => f.client.listDesktopsPage({ limit: 201 }), /between 1 and 200/);
});

test("extend, history and processes use the new desktop routes", async t => {
  const f = await fixture(t);
  f.response = { id: "vm1", state: "ready", started: "2026-10-19T00:00:00Z" };
  const desktop = await f.client.extendDesktop("vm1", 1800);
  assert.equal(f.requests.at(-1).method, "POST");
  assert.equal(f.requests.at(-1).url.pathname, "/api/desktops/vm1/extend");
  assert.deepEqual(JSON.parse(f.requests.at(-1).body), { ttl_seconds: 1800 });
  assert.equal(desktop.id, "vm1");

  f.response = { entries: [{ id: "e1", command: "uname -a", startedAt: "s", endedAt: "e", exitCode: 0, output: "Linux" }] };
  const entries = await f.client.listHistory("vm1");
  assert.equal(f.requests.at(-1).url.pathname, "/api/desktops/vm1/history");
  assert.equal(entries[0].command, "uname -a");
  assert.equal(entries[0].exitCode, 0);

  f.response = { processes: [{ pid: 1, ppid: 0, user: "root", etime: "00:00:01", args: "init" }] };
  const processes = await f.client.listProcesses("vm1");
  assert.equal(f.requests.at(-1).url.pathname, "/api/desktops/vm1/processes");
  assert.equal(processes[0].pid, 1);
  assert.equal(processes[0].args, "init");
});

test("URLs, IDs, offsets and cancellation", async t => {
  const f = await fixture(t);
  for (const baseUrl of ["file:///etc/passwd", "https://user:password@example.com", "http://host?token=x", "http://host#x"]) {
    assert.throws(() => new WarmboxClient({ baseUrl, token: "test-token" }));
  }
  for (const id of ["", ".", ".."]) assert.throws(() => f.client.getDesktop(id));
  await assert.rejects(f.client.streamRun("vm1", "run1", { from: -1 }).next(), /non-negative integer/);
  await f.client.getDesktop("a/b ?#");
  assert.equal(f.requests.at(-1).url.pathname, "/api/desktops/a%2Fb%20%3F%23");
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(f.client.getDesktop("vm1", { signal: controller.signal }), { name: "AbortError" });
});
