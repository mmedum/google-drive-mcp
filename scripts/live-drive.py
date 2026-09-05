#!/usr/bin/env python3
"""Drive the server over stdio against a real Google account.

Every tool is called and its result printed with ids, URLs, addresses and
domains replaced by stable placeholders, so a transcript can be pasted
into an issue or a commit message without leaking anything. Nothing here
writes to Drive: phase 0 registers only read tools.

    python3 scripts/live-drive.py ./google-drive-mcp

Options:
    --file REF     also run get_file and list_folder against this
                   reference (an id, URL or path)
    --raw          print results without redaction (never do this in a
                   terminal you are sharing)
"""

import argparse
import json
import re
import subprocess
import sys
import threading


class Server:
    """One stdio MCP session."""

    def __init__(self, binary, env=None):
        self.proc = subprocess.Popen(
            [binary],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
            env=env,
        )
        self.next_id = 0
        self.stderr = []
        threading.Thread(target=self._drain_stderr, daemon=True).start()

    def _drain_stderr(self):
        for line in self.proc.stderr:
            self.stderr.append(line.rstrip())

    def request(self, method, params=None):
        self.next_id += 1
        frame = {"jsonrpc": "2.0", "id": self.next_id, "method": method}
        if params is not None:
            frame["params"] = params
        self.proc.stdin.write(json.dumps(frame) + "\n")
        self.proc.stdin.flush()
        while True:
            line = self.proc.stdout.readline()
            if not line:
                raise SystemExit("server closed stdout; stderr:\n" + "\n".join(self.stderr[-20:]))
            msg = json.loads(line)
            if msg.get("id") == self.next_id:
                return msg

    def notify(self, method, params=None):
        frame = {"jsonrpc": "2.0", "method": method}
        if params is not None:
            frame["params"] = params
        self.proc.stdin.write(json.dumps(frame) + "\n")
        self.proc.stdin.flush()

    def close(self):
        try:
            self.proc.stdin.close()
            self.proc.wait(timeout=10)
        except Exception:
            self.proc.kill()


class Redactor:
    """Replaces anything account-specific with a stable placeholder.

    The same input always gets the same placeholder, so a transcript
    still reads as a story: FILE_1 in one result is FILE_1 in the next.
    """

    # Drive ids, addresses, and the URLs that carry them. The id pattern
    # is deliberately narrow: 19 characters is the shortest id Google
    # issues, and requiring both a capital and a digit keeps ordinary
    # words like "google-drive-mcp" and "modified_before" readable.
    PATTERNS = [
        ("EMAIL", re.compile(r"\b[\w.+-]+@[\w-]+\.[\w.-]+\b")),
        ("URL", re.compile(r"https://(?:drive|docs)\.google\.com/\S+")),
        (
            "ID",
            re.compile(
                r"\b(?=[A-Za-z0-9_-]{19,}\b)(?=[A-Za-z0-9_-]*[A-Z])(?=[A-Za-z0-9_-]*[0-9])[A-Za-z0-9_-]+\b"
            ),
        ),
    ]

    def __init__(self, enabled=True):
        self.enabled = enabled
        self.seen = {}
        self.counts = {}

    def _placeholder(self, kind, value):
        if value not in self.seen:
            self.counts[kind] = self.counts.get(kind, 0) + 1
            self.seen[value] = f"<{kind}_{self.counts[kind]}>"
        return self.seen[value]

    def __call__(self, text):
        if not self.enabled:
            return text
        for kind, pattern in self.PATTERNS:
            text = pattern.sub(lambda m: self._placeholder(kind, m.group(0)), text)
        return text


def call_tool(server, redact, name, arguments, expect_error=False):
    print(f"\n=== {name} {json.dumps(arguments, sort_keys=True)} ===")
    msg = server.request("tools/call", {"name": name, "arguments": arguments})
    if "error" in msg:
        print("protocol error:", redact(json.dumps(msg["error"])))
        return None, False
    result = msg["result"]
    is_error = bool(result.get("isError"))
    body = "".join(c.get("text", "") for c in result.get("content", []))
    print(redact(body).rstrip())
    if is_error and not expect_error:
        print("!! unexpected tool error")
    if not is_error and expect_error:
        print("!! expected a refusal and did not get one")
    return result, is_error == expect_error


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("binary", nargs="?", default="./google-drive-mcp")
    ap.add_argument("--file", help="a reference to run get_file and list_folder against")
    ap.add_argument("--raw", action="store_true", help="do not redact (do not share the output)")
    args = ap.parse_args()

    redact = Redactor(enabled=not args.raw)
    server = Server(args.binary)
    ok = True
    try:
        init = server.request(
            "initialize",
            {
                "protocolVersion": "2025-11-25",
                "capabilities": {},
                "clientInfo": {"name": "live-drive", "version": "0"},
            },
        )
        print("protocol:", init["result"]["protocolVersion"])
        server.notify("notifications/initialized")

        tools = [t["name"] for t in server.request("tools/list")["result"]["tools"]]
        print("tools:", ", ".join(sorted(tools)))

        _, good = call_tool(server, redact, "get_account", {})
        ok &= good

        _, good = call_tool(server, redact, "list_folder", {"folder": "root", "page_size": 10})
        ok &= good

        res, good = call_tool(
            server, redact, "search_files", {"kind": "folder", "limit": 5, "order_by": "modified"}
        )
        ok &= good

        # Refusals that must be refusals, not silent successes.
        _, good = call_tool(
            server, redact, "get_file", {"file": "https://example.com/not-a-drive-link"}, expect_error=True
        )
        ok &= good
        _, good = call_tool(server, redact, "search_files", {}, expect_error=True)
        ok &= good

        if args.file:
            _, good = call_tool(server, redact, "get_file", {"file": args.file})
            ok &= good
            _, good = call_tool(server, redact, "list_folder", {"folder": args.file, "recursive": True, "max_depth": 2})
            ok &= good
        else:
            print("\n(pass --file REF to also exercise get_file and a recursive listing)")
    finally:
        server.close()

    stderr = [line for line in server.stderr if line.strip()]
    if stderr:
        print("\n=== stderr ===")
        for line in stderr[-20:]:
            print(redact(line))

    print("\nOK" if ok else "\nsome calls did not behave as expected")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
