#!/usr/bin/env python3
"""Measure the forward proxy against a local mock upstream.

No external service is contacted. The mock upstream accepts a fixed payload
and streams a fixed SSE response. Results are written as JSON.
"""
from __future__ import annotations
import argparse, json, os, platform, statistics, subprocess, tempfile, threading, time, urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
def default_binary() -> Path:
    machine = platform.machine().lower()
    arch = "amd64" if machine in {"x86_64", "amd64"} else "arm64" if machine in {"aarch64", "arm64"} else ""
    candidate = ROOT / "dist" / f"linux-{arch}" / "tokensaver" if arch else ROOT / "dist" / "tokensaver"
    return candidate if candidate.exists() else ROOT / "dist" / "tokensaver"

BIN = default_binary()

class Upstream(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def do_POST(self):
        n = int(self.headers.get("Content-Length", "0"))
        self.rfile.read(n)
        if self.path.endswith("/responses"):
            body = b'{"id":"resp_test","object":"response","output":[]}'
            ctype = b"application/json"
        elif self.path.endswith("/messages"):
            body = b'{"id":"msg_test","type":"message","content":[]}'
            ctype = b"application/json"
        else:
            if self.headers.get("X-Test-Stream") == "1":
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.send_header("Connection", "close")
                self.end_headers()
                for chunk in [b'data: {"choices":[{"delta":{"content":"he"}}]}\n\n', b'data: {"choices":[{"delta":{"content":"llo"}}]}\n\n', b'data: [DONE]\n\n']:
                    self.wfile.write(chunk); self.wfile.flush()
                return
            body = b'{"id":"chatcmpl-test","object":"chat.completion","choices":[]}'
            ctype = b"application/json"
        self.send_response(200)
        self.send_header("Content-Type", ctype.decode())
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *args):
        pass

def post(url: str, body: bytes, headers: dict[str,str]) -> tuple[int,float,bytes]:
    req = urllib.request.Request(url, data=body, headers=headers, method="POST")
    started = time.perf_counter()
    with urllib.request.urlopen(req, timeout=30) as r:
        data = r.read()
        status = r.status
    return status, (time.perf_counter()-started)*1000, data

def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--iterations", type=int, default=20)
    ap.add_argument("--output", default="/tmp/tokensaver-gateway-mock.json")
    args = ap.parse_args()
    upstream = ThreadingHTTPServer(("127.0.0.1", 0), Upstream)
    thread = threading.Thread(target=upstream.serve_forever, daemon=True)
    thread.start()
    upstream_port = upstream.server_address[1]
    cfg = json.loads((ROOT/"config/gateway.example.json").read_text())
    cfg["listen"] = "127.0.0.1:19231"
    cfg["upstream"] = f"http://127.0.0.1:{upstream_port}"
    cfg["management_enabled"] = True
    cfg["core"]["threshold_tokens"] = 0
    cfg["core"]["min_savings_tokens"] = 0
    cfg["core"]["preserve_prefix_items"] = 0
    with tempfile.TemporaryDirectory() as td:
        cfgpath = Path(td)/"gateway.json"
        cfgpath.write_text(json.dumps(cfg))
        proc = subprocess.Popen([str(BIN), "-config", str(cfgpath)], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
        try:
            base = "http://127.0.0.1:19231"
            for _ in range(100):
                try:
                    urllib.request.urlopen(base+"/__tokensaver/health", timeout=1).read()
                    break
                except Exception:
                    time.sleep(0.05)
            fixtures = json.loads((ROOT/"tests/fixtures/long-coding-session.json").read_text())["messages"]
            chat = json.dumps({"model":"fixture","messages":fixtures}, separators=(",",":")).encode()
            results=[]
            for protocol,path in [("chat","/v1/chat/completions"),("responses","/v1/responses"),("anthropic","/v1/messages")]:
                path_body = chat if protocol=="chat" else json.dumps({"model":"fixture","input":[],"messages":[]}).encode() if protocol=="responses" else json.dumps({"model":"fixture","messages":[]}).encode()
                # For Responses/Anthropic use protocol-valid payloads with the same large tool content where possible.
                if protocol=="responses":
                    path_body = json.dumps({"model":"fixture","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"current"}]}]}).encode()
                if protocol=="anthropic":
                    path_body = json.dumps({"model":"fixture","messages":[{"role":"user","content":"current"}]}).encode()
                durations=[]; ok=0
                for _ in range(args.iterations):
                    status, ms, _ = post(base+path, path_body, {"Content-Type":"application/json"})
                    durations.append(ms)
                    ok += status==200
                stats=json.loads(urllib.request.urlopen(base+"/__tokensaver/stats").read())
                results.append({"protocol":protocol,"path":path,"requests":args.iterations,"ok":ok,"p50_ms":statistics.median(durations),"p95_ms":sorted(durations)[int((len(durations)-1)*0.95)],"stats":stats,"stats_snapshot_kind":"cumulative across prior protocols in this process"})
            out={"upstream":"local ThreadingHTTPServer mock","iterations":args.iterations,"results":results}
            Path(args.output).parent.mkdir(parents=True, exist_ok=True)
            Path(args.output).write_text(json.dumps(out, indent=2)+"\n")
            print(json.dumps(out, indent=2))
        finally:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
            upstream.shutdown()
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
