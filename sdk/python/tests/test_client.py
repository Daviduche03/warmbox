import json
import threading
import unittest
from contextlib import closing
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlsplit

from warmbox import APIError, WarmboxClient


class ClientTests(unittest.TestCase):
    def setUp(self):
        self.requests = []
        self.response = {"id": "vm1"}
        self.status = 200
        self.content_type = "application/json"
        self.stream = None
        self.release = threading.Event()
        self.finished = threading.Event()
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def handle_request(self):
                body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
                owner.requests.append((self.command, urlsplit(self.path), dict(self.headers), body))
                self.send_response(owner.status)
                self.send_header("Content-Type", owner.content_type)
                if owner.status == 302:
                    self.send_header("Location", f"http://127.0.0.1:{owner.server.server_port}/leak")
                self.end_headers()
                if owner.stream is not None:
                    for part in owner.stream:
                        if part is None:
                            owner.release.wait(3)
                        else:
                            self.wfile.write(part)
                            self.wfile.flush()
                    owner.finished.set()
                elif isinstance(owner.response, bytes):
                    self.wfile.write(owner.response)
                else:
                    self.wfile.write(json.dumps(owner.response).encode())

            do_GET = do_POST = do_PUT = do_DELETE = handle_request

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.client = WarmboxClient(f"http://127.0.0.1:{self.server.server_port}",
                                   token="test-token", workspace_id="workspace-1", timeout=5)

    def tearDown(self):
        self.release.set()
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()

    def test_desktop_lifecycle_and_exec(self):
        self.assertEqual(self.client.create_desktop({"image": "headless", "mem_mib": 512}), {"id": "vm1"})
        self.assertEqual(json.loads(self.requests[-1][3]), {"image": "headless", "mem_mib": 512})
        self.response = {"desktops": None}
        self.assertEqual(self.client.list_desktops(), [])
        self.response = {"desktops": [{"id": "vm1", "headless": True}]}
        self.assertEqual(self.client.list_desktops()[0]["id"], "vm1")
        self.response = {"id": "vm1", "headless": True}
        self.assertTrue(self.client.get_desktop("vm1")["headless"])
        self.client.pause_desktop("vm1")
        self.client.resume_desktop("vm1")
        self.client.destroy_desktop("vm1")
        result = {"exit": 7, "stdout": "out", "stderr": "err", "duration_ms": 2, "timed_out": False}
        self.response = result
        options = {"argv": ["sh", "-c", "exit 7"], "env": {"K": "V"}, "cwd": "/home", "stdin": "input", "timeout_ms": 2000}
        self.assertEqual(self.client.exec("vm1", options), result)
        self.assertEqual(json.loads(self.requests[-1][3]), options)
        self.assertEqual([(r[0], r[1].path) for r in self.requests], [
            ("POST", "/api/desktops"), ("GET", "/api/desktops"), ("GET", "/api/desktops"),
            ("GET", "/api/desktops/vm1"), ("POST", "/api/desktops/vm1/pause"),
            ("POST", "/api/desktops/vm1/resume"), ("DELETE", "/api/desktops/vm1"),
            ("POST", "/api/desktops/vm1/exec"),
        ])
        for _, _, headers, _ in self.requests:
            self.assertEqual(headers["Authorization"], "Bearer test-token")
            self.assertEqual(headers["X-Workspace-Id"], "workspace-1")
        self.assertEqual(self.requests[-1][2]["Content-Type"], "application/json")

    def test_binary_files_and_query_encoding(self):
        path = "/home/ü &?#.bin"
        binary = b"\x00\xff\x80hello"
        self.response = {"written": len(binary)}
        self.assertEqual(self.client.write_file("vm1", path, binary), self.response)
        self.assertEqual(self.requests[-1][3], binary)
        self.response = binary
        self.content_type = "application/octet-stream"
        self.assertEqual(self.client.read_file("vm1", path), binary)
        self.response = [{"name": "ü &?#.bin", "type": "file", "size": len(binary), "mode": "-rw-r--r--", "mtime": "2026-10-06T00:00:00Z"}]
        self.content_type = "application/json"
        self.assertEqual(self.client.list_files("vm1", path), self.response)
        self.response = {"status": "deleted"}
        self.client.delete_file("vm1", path)
        for request in self.requests:
            self.assertEqual(parse_qs(request[1].query), {"path": [path]})
        self.client.move_file("vm1", path, "/home/new")
        self.assertEqual(json.loads(self.requests[-1][3]), {"from": path, "to": "/home/new"})
        self.response = {"written": 2}
        self.client.write_file("vm1", path, "ü")
        self.assertEqual(self.requests[-1][3], "ü".encode())

    def test_runs_and_sessions(self):
        self.client.start_run("vm1", {"cmd": "cat", "interactive": True, "max_bytes": 100})
        self.assertEqual(json.loads(self.requests[-1][3]), {"cmd": "cat", "interactive": True, "max_bytes": 100})
        self.response = {"runs": None}
        self.assertEqual(self.client.list_runs("vm1"), [])
        self.response = {"id": "run1", "running": False, "output_bytes": 0, "exit": 1}
        self.assertEqual(self.client.get_run("vm1", "run1", tail=99)["exit"], 1)
        self.assertEqual(parse_qs(self.requests[-1][1].query), {"tail": ["99"]})
        self.client.write_stdin("vm1", "run1", b"\x00input")
        self.assertEqual(self.requests[-1][3], b"\x00input")
        self.client.kill_run("vm1", "run1")
        self.client.create_session("vm1", {"cwd": "/home", "shell": "/bin/sh"})
        self.assertEqual(json.loads(self.requests[-1][3]), {"cwd": "/home", "shell": "/bin/sh"})
        self.client.session_input("vm1", "session1", "pwd")
        self.assertEqual(self.requests[-1][3], b"pwd")
        self.client.destroy_session("vm1", "session1")
        self.assertEqual([(r[0], r[1].path) for r in self.requests], [
            ("POST", "/api/desktops/vm1/runs"), ("GET", "/api/desktops/vm1/runs"),
            ("GET", "/api/desktops/vm1/runs/run1"), ("POST", "/api/desktops/vm1/runs/run1/stdin"),
            ("DELETE", "/api/desktops/vm1/runs/run1"), ("POST", "/api/desktops/vm1/sessions"),
            ("POST", "/api/desktops/vm1/sessions/session1/input"), ("DELETE", "/api/desktops/vm1/sessions/session1"),
        ])

    def test_live_stream_with_split_utf8_and_multiline_data(self):
        self.content_type = "text/event-stream; charset=utf-8"
        first = ': heartbeat\r\nevent: output\r\ndata: {"out":\r\ndata: "héllo"}\r\n\r\n'.encode()
        end = b'data: {"exit":{"id":"run1","running":false,"exit":7}}\n\n'
        split = first.index("é".encode()) + 1
        self.stream = [first[:split], first[split:], None, end]
        with closing(self.client.stream_run("vm1", "run1", from_offset=42)) as events:
            self.assertEqual(next(events), {"out": "héllo"})
            self.assertFalse(self.finished.is_set())
            self.release.set()
            self.assertEqual(next(events)["exit"]["exit"], 7)
            self.assertEqual(list(events), [])
        self.assertEqual(parse_qs(self.requests[-1][1].query), {"from": ["42"]})
        self.assertEqual(self.requests[-1][2]["Accept"], "text/event-stream")
        self.assertEqual(self.requests[-1][2]["Authorization"], "Bearer test-token")
        self.assertEqual(self.requests[-1][2]["X-Workspace-Id"], "workspace-1")
        self.assertEqual(self.requests[-1][1].path, "/api/desktops/vm1/runs/run1/stream")

    def test_session_stream_and_early_close(self):
        self.content_type = "text/event-stream"
        self.stream = [b'data: {"out":"ready"}\n\n', None, b'data: {"exit":{}}\n\n']
        events = self.client.stream_session("vm1", "session1")
        self.assertEqual(next(events), {"out": "ready"})
        events.close()
        self.assertEqual(self.requests[-1][1].path, "/api/desktops/vm1/sessions/session1/output")

    def test_stream_failures(self):
        self.content_type = "text/event-stream"
        for body, error in [(b'data: {"out":"partial"}\n\n', ConnectionError),
                            (b'data: nope\n\n', ValueError), (b'data: []\n\n', ValueError)]:
            with self.subTest(body=body):
                self.stream = [body]
                with self.assertRaises(error):
                    list(self.client.stream_run("vm1", "run1"))
        self.stream = None
        self.content_type = "application/json"
        with self.assertRaisesRegex(ValueError, "text/event-stream"):
            list(self.client.stream_run("vm1", "run1"))

    def test_errors_and_redirects(self):
        for status, response, message in [(404, {"error": "unknown desktop"}, "unknown desktop"),
                                           (401, b"unauthorized\n", "unauthorized"),
                                           (502, b"<html>bad gateway</html>", "bad gateway")]:
            with self.subTest(status=status):
                self.status, self.response = status, response
                with self.assertRaises(APIError) as caught:
                    self.client.get_desktop("vm1")
                self.assertEqual(caught.exception.status, status)
                self.assertIn(message, str(caught.exception))
        self.status, self.response = 302, b"redirect"
        before = len(self.requests)
        with self.assertRaises(APIError) as caught:
            self.client.get_desktop("vm1")
        self.assertEqual(caught.exception.status, 302)
        self.assertEqual(len(self.requests), before + 1)

    def test_url_and_id_validation(self):
        for url in ["file:///etc/passwd", "https://user:password@example.com", "http://host?token=x", "http://host#x"]:
            with self.subTest(url=url), self.assertRaises(ValueError):
                WarmboxClient(url, token="test-token")
        for value in ["", ".", ".."]:
            with self.subTest(value=value), self.assertRaises(ValueError):
                self.client.get_desktop(value)
        for offset in [-1, True, 0.5, 1 << 63]:
            before = len(self.requests)
            with self.subTest(offset=offset), self.assertRaises(ValueError):
                list(self.client.stream_run("vm1", "run1", from_offset=offset))
            self.assertEqual(len(self.requests), before)
        self.client.get_desktop("a/b ?#")
        self.assertEqual(self.requests[-1][1].path, "/api/desktops/a%2Fb%20%3F%23")


if __name__ == "__main__":
    unittest.main()
