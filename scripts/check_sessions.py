"""Exercise the built stdio binary with real clients using only a fresh temporary root."""
import concurrent.futures
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time

BINARY = str(Path(sys.argv[1]).resolve())


class Client:
    def __init__(self, root, detached_parent=False):
        cmd = [BINARY, "serve"]
        if detached_parent:
            # The test process keeps the input pipe open even after this parent dies.
            cmd = [sys.executable, "-c",
                   "import subprocess,sys,time; p=subprocess.Popen(sys.argv[1:]); time.sleep(60)",
                   *cmd]
        self.process = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                        env={**os.environ, "CNKI_DATA_DIR": str(root)})
        self.seq = 0

    def send(self, message):
        self.process.stdin.write(json.dumps({"jsonrpc": "2.0", **message}).encode() + b"\n")
        self.process.stdin.flush()

    def request(self, method, params):
        self.seq += 1
        self.send({"id": self.seq, "method": method, "params": params})
        while line := self.process.stdout.readline():
            reply = json.loads(line)
            if reply.get("id") == self.seq:
                return reply["result"]
        raise RuntimeError("unexpected EOF")

    def initialize(self):
        self.request("initialize", {"protocolVersion": "2025-11-25", "capabilities": {},
                     "clientInfo": {"name": "session-test", "version": "1"}})
        self.send({"method": "notifications/initialized"})

    def call(self, name, args=None):
        response = self.request("tools/call", {"name": name, "arguments": args or {}})
        if response.get("isError"):
            raise RuntimeError(response["content"])
        return response.get("structuredContent") or json.loads(response["content"][0]["text"])


def wait_for_shutdown(root):
    deadline = time.monotonic() + 12
    while (root / ".cnki-endpoint.json").exists() and time.monotonic() < deadline:
        time.sleep(0.1)
    assert not (root / ".cnki-endpoint.json").exists(), "backend remained after disconnect"


def main():
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        a, b = Client(root), Client(root)
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            list(pool.map(lambda c: c.initialize(), [a, b]))
            states = list(pool.map(lambda c: c.call("status"), [a, b]))
        assert all(s["login"] == "not_checked" and "active_operation" not in s for s in states), states
        pid = lambda c: c.call("status", {"details": True})["diagnostics"]["process_id"]
        leader = pid(a)
        assert leader == pid(b)
        assert len(a.request("tools/list", {})["tools"]) == 9
        a.process.kill()
        a.process.wait(5)
        assert pid(b) == leader
        b.process.stdin.close()
        b.process.wait(5)
        wait_for_shutdown(root)

        c = Client(root, detached_parent=True)
        c.initialize()
        assert c.call("status")["login"] == "not_checked"
        c.process.kill()
        c.process.wait(5)
        # stdin is intentionally open. The bridge must notice parent death.
        wait_for_shutdown(root)
        c.process.stdin.close()
        leftovers = sorted(p.name for p in root.iterdir() if p.name not in {".cnki-owner.json", "cnki.sqlite", "cnki.sqlite-wal", "cnki.sqlite-shm", ".cnki-store.lock", ".cnki-worker.lock", ".cnki-launch.lock", "config.json"})
        assert not leftovers, leftovers
        print("Two simultaneous MCP sessions shared a worker; single-client loss, last EOF and parent death passed.")


if __name__ == "__main__":
    main()
