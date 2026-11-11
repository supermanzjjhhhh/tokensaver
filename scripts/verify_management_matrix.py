#!/usr/bin/env python3
"""Exercise every TokenSaver management setting through a live CPA process.

The script never prints the management key. It restores the original Core config
on exit, including when an assertion fails. Use it against an isolated CPA test
container first and only then against a test CPA instance you control.
"""
from __future__ import annotations

import argparse
import concurrent.futures
import copy
import json
import re
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

PLUGIN_ID = "tokensaver-context-compression"
UI_PATH = f"/v0/resource/plugins/{PLUGIN_ID}/ui"
CONFIG_PATH = "/v0/management/tokensaver/config"
PREVIEW_PATH = "/v0/management/tokensaver/preview"
STATS_PATH = "/v0/management/tokensaver/stats"


def read_management_key(config_path: Path) -> str:
    text = config_path.read_text(encoding="utf-8")
    in_management = False
    for line in text.splitlines():
        if re.match(r"^management:\s*$", line):
            in_management = True
            continue
        if in_management and re.match(r"^\S", line):
            break
        if in_management:
            match = re.match(r"^\s+secret-key:\s*(.+?)\s*$", line)
            if match:
                value = match.group(1).strip()
                if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
                    value = value[1:-1]
                return value
    raise SystemExit(f"management.secret-key not found in {config_path}")


class Client:
    def __init__(self, base_url: str, key: str, timeout: float = 15.0):
        self.base_url = base_url.rstrip("/")
        self.key = key
        self.timeout = timeout

    def request(self, method: str, path: str, body: Any = None, *, auth: bool = True) -> tuple[int, dict[str, str], bytes]:
        data = None
        headers = {"Accept": "application/json"}
        if body is not None:
            data = json.dumps(body, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
            headers["Content-Type"] = "application/json"
        if auth:
            headers["Authorization"] = "Bearer " + self.key
        req = urllib.request.Request(self.base_url + path, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as response:
                return response.status, {k.lower(): v for k, v in response.headers.items()}, response.read()
        except urllib.error.HTTPError as error:
            return error.code, {k.lower(): v for k, v in error.headers.items()}, error.read()

    def json(self, method: str, path: str, body: Any = None, *, auth: bool = True) -> tuple[int, dict[str, str], Any]:
        status, headers, raw = self.request(method, path, body, auth=auth)
        try:
            payload = json.loads(raw) if raw else None
        except json.JSONDecodeError:
            payload = raw.decode("utf-8", errors="replace")
        return status, headers, payload


def base_config() -> dict[str, Any]:
    return {
        "enabled": True,
        "mode": "custom",
        "threshold_tokens": 0,
        "min_savings_tokens": 0,
        "preserve_prefix_items": 0,
        "preserve_last_user": True,
        "protect_system": True,
        "allow_lossy": False,
        "model_allow": ["*"],
        "model_deny": [],
        "engines": [
            {"id": "whitespace", "enabled": False},
            {"id": "json-minify", "enabled": False},
            {"id": "exact-dedup", "enabled": False},
            {"id": "diagnostic-trim", "enabled": False},
            {"id": "tool-truncate", "enabled": False},
        ],
        "tool_truncate": {"max_chars": 256, "head_pct": 0.5, "tail_pct": 0.25, "min_chars": 0},
        "diagnostic_trim": {"max_lines": 8, "min_chars": 0, "preserve_error_lines": True},
        "logging": {"enabled": True, "max_entries": 500, "include_model": True, "include_engine": True},
    }


def engine_config(*enabled: str, allow_lossy: bool = False) -> dict[str, Any]:
    cfg = base_config()
    cfg["allow_lossy"] = allow_lossy
    enabled_set = set(enabled)
    for engine in cfg["engines"]:
        engine["enabled"] = engine["id"] in enabled_set
    return cfg


def chat_body(messages: list[dict[str, Any]], model: str = "gpt-test", **extra: Any) -> dict[str, Any]:
    body = {"model": model, "messages": messages}
    body.update(extra)
    return body


def ws_body() -> dict[str, Any]:
    return chat_body([
        {"role": "assistant", "content": "old  \n\n\n text"},
        {"role": "user", "content": "now"},
    ])


def json_tool_body() -> dict[str, Any]:
    payload = json.dumps({"payload": "a" * 80, "items": [1, 2, 3]}, indent=2)
    return chat_body([
        {"role": "assistant", "content": "old"},
        {"role": "tool", "tool_call_id": "call-1", "content": payload},
        {"role": "user", "content": "now"},
    ])


def dedup_body() -> dict[str, Any]:
    return chat_body([
        {"role": "assistant", "content": "duplicate"},
        {"role": "assistant", "content": "duplicate"},
        {"role": "user", "content": "now"},
    ])


def diagnostic_body() -> dict[str, Any]:
    lines = [f"line {i:03d} " + "x" * 20 for i in range(30)]
    lines[12] = "ERROR keep me"
    return chat_body([
        {"role": "assistant", "content": "old"},
        {"role": "tool", "tool_call_id": "call-1", "content": "\n".join(lines)},
        {"role": "user", "content": "now"},
    ])


def truncate_body() -> dict[str, Any]:
    return chat_body([
        {"role": "assistant", "content": "old"},
        {"role": "tool", "tool_call_id": "call-1", "content": "A" * 800 + "B" * 800},
        {"role": "user", "content": "now"},
    ])


class Matrix:
    def __init__(self, client: Client):
        self.client = client
        self.checks: list[dict[str, Any]] = []

    def check(self, name: str, condition: bool, detail: Any = None) -> None:
        self.checks.append({"name": name, "ok": bool(condition), "detail": detail})
        if not condition:
            raise AssertionError(f"{name}: {detail}")

    def preview(self, config: dict[str, Any], body: Any, protocol: str | None = None) -> dict[str, Any]:
        envelope: dict[str, Any] = {"config": config, "body": body}
        if protocol:
            envelope["protocol"] = protocol
        status, _, payload = self.client.json("POST", PREVIEW_PATH, envelope)
        self.check("preview HTTP 200", status == 200, {"status": status, "payload": payload})
        self.check("preview response has stats", isinstance(payload, dict) and isinstance(payload.get("stats"), dict), payload)
        return payload

    def run(self) -> dict[str, Any]:
        original_status, _, original = self.client.json("GET", CONFIG_PATH)
        self.check("management auth and config GET", original_status == 200 and isinstance(original, dict), {"status": original_status, "payload": original})
        try:
            self.run_cases(original)
        finally:
            status, headers, payload = self.client.json("PUT", CONFIG_PATH, original)
            self.check("restore original config", status == 200 and payload == original, {"status": status, "payload": payload, "headers": headers})
        return {"ok": all(item["ok"] for item in self.checks), "checks": self.checks, "original_config": original}

    def run_cases(self, original: dict[str, Any]) -> None:
        # Authentication and UI resource.
        status, _, _ = self.client.json("GET", CONFIG_PATH, auth=False)
        self.check("management API requires auth", status in (401, 403), status)
        status, _, ui = self.client.request("GET", UI_PATH, auth=False)
        self.check("UI resource unauthenticated GET", status == 200, status)
        self.check("UI contains management key support", b"X-Management-Key" in ui and b"cli-proxy-auth" in ui, None)

        status, _, plugins = self.client.json("GET", "/v0/management/plugins")
        self.check("plugin list HTTP 200", status == 200, status)
        plugin = next((p for p in plugins.get("plugins", []) if p.get("id") == PLUGIN_ID), None)
        self.check("plugin registered and effective", bool(plugin and plugin.get("registered") and plugin.get("effective_enabled")), plugin)
        self.check("plugin version 0.2.0", (plugin or {}).get("metadata", {}).get("version") == "0.2.0", plugin)

        # Persistence and validation.
        probe = copy.deepcopy(original)
        probe["threshold_tokens"] = int(probe.get("threshold_tokens", 0)) + 1
        status, headers, payload = self.client.json("PUT", CONFIG_PATH, probe)
        self.check("config save persists", status == 200 and headers.get("x-tokensaver-persisted") == "true", {"status": status, "headers": headers, "payload": payload})
        status, _, reread = self.client.json("GET", CONFIG_PATH)
        self.check("config save roundtrip", status == 200 and reread.get("threshold_tokens") == probe["threshold_tokens"], reread)
        self.check("logging preserved on save", reread.get("logging") == original.get("logging"), {"before": original.get("logging"), "after": reread.get("logging")})

        invalid = copy.deepcopy(original)
        invalid["mode"] = "definitely-invalid"
        status, _, payload = self.client.json("PUT", CONFIG_PATH, invalid)
        self.check("invalid config rejected", status == 400, {"status": status, "payload": payload})

        # Global switches and modes.
        cfg = engine_config("whitespace")
        cfg["enabled"] = False
        result = self.preview(cfg, ws_body())
        self.check("enabled=false disables compression", result.get("applied") is False and result["stats"].get("skipped_reason") == "disabled", result["stats"])

        cfg = engine_config("whitespace")
        cfg["mode"] = "off"
        result = self.preview(cfg, ws_body())
        self.check("mode=off disables compression", result.get("applied") is False and result["stats"].get("skipped_reason") == "disabled", result["stats"])

        result = self.preview(engine_config("whitespace"), ws_body())
        self.check("custom whitespace applies", result.get("applied") is True and "whitespace" in result["stats"].get("engines", []), result["stats"])

        cfg = engine_config()
        cfg["mode"] = "SAFE"
        result = self.preview(cfg, ws_body())
        self.check("mode is normalized case-insensitively", result.get("applied") is True and "whitespace" in result["stats"].get("engines", []), result["stats"])

        result = self.preview(engine_config("json-minify"), json_tool_body())
        self.check("custom json-minify applies", result.get("applied") is True and "json-minify" in result["stats"].get("engines", []), result["stats"])

        result = self.preview(engine_config("exact-dedup"), dedup_body())
        self.check("custom exact-dedup applies", result.get("applied") is True and "exact-dedup" in result["stats"].get("engines", []), result["stats"])

        result = self.preview(engine_config("diagnostic-trim", allow_lossy=False), diagnostic_body())
        self.check("lossy engine blocked without allow_lossy", result.get("applied") is False, result["stats"])
        result = self.preview(engine_config("diagnostic-trim", allow_lossy=True), diagnostic_body())
        self.check("diagnostic-trim applies with allow_lossy", result.get("applied") is True and "diagnostic-trim" in result["stats"].get("engines", []), result["stats"])

        result = self.preview(engine_config("tool-truncate", allow_lossy=False), truncate_body())
        self.check("tool-truncate blocked without allow_lossy", result.get("applied") is False, result["stats"])
        result = self.preview(engine_config("tool-truncate", allow_lossy=True), truncate_body())
        self.check("tool-truncate applies with allow_lossy", result.get("applied") is True and "tool-truncate" in result["stats"].get("engines", []), result["stats"])

        # Presets.
        cfg = engine_config()
        cfg["mode"] = "safe"
        result = self.preview(cfg, ws_body())
        self.check("safe preset applies", result.get("applied") is True and "whitespace" in result["stats"].get("engines", []), result["stats"])

        cfg = engine_config(allow_lossy=False)
        cfg["mode"] = "balanced"
        result = self.preview(cfg, diagnostic_body())
        self.check("balanced preset respects allow_lossy=false", result.get("applied") is False, result["stats"])
        cfg["allow_lossy"] = True
        result = self.preview(cfg, diagnostic_body())
        self.check("balanced preset applies diagnostic-trim", result.get("applied") is True and "diagnostic-trim" in result["stats"].get("engines", []), result["stats"])

        cfg = engine_config(allow_lossy=True)
        cfg["mode"] = "aggressive"
        result = self.preview(cfg, truncate_body())
        self.check("aggressive preset applies tool-truncate", result.get("applied") is True and "tool-truncate" in result["stats"].get("engines", []), result["stats"])

        # Threshold and minimum savings.
        cfg = engine_config("whitespace")
        cfg["threshold_tokens"] = 100000
        result = self.preview(cfg, ws_body())
        self.check("threshold_tokens skips below threshold", result.get("applied") is False and result["stats"].get("skipped_reason") == "below-threshold", result["stats"])

        cfg = engine_config("whitespace")
        cfg["min_savings_tokens"] = 100000
        result = self.preview(cfg, ws_body())
        self.check("min_savings_tokens skips below minimum", result.get("applied") is False and result["stats"].get("skipped_reason") == "below-min-savings", result["stats"])

        # Prefix and current-turn protection.
        prefix_body = chat_body([
            {"role": "assistant", "content": "first  \n\n\n keep"},
            {"role": "assistant", "content": "second  \n\n\n change"},
            {"role": "user", "content": "now"},
        ])
        cfg = engine_config("whitespace")
        cfg["preserve_prefix_items"] = 1
        result = self.preview(cfg, prefix_body)
        self.check("preserve_prefix_items preserves first item", result.get("applied") is True and result["stats"].get("first_changed_item", 0) >= 1, result["stats"])

        current_body = chat_body([
            {"role": "assistant", "content": "old  \n\n\n change"},
            {"role": "user", "content": "current  \n\n\n keep"},
            {"role": "tool", "tool_call_id": "current-tool", "content": "current  \n\n\n keep"},
        ])
        cfg = engine_config("whitespace")
        cfg["preserve_last_user"] = True
        result = self.preview(cfg, current_body)
        self.check("preserve_last_user preserves current turn", result.get("applied") is True and result["stats"].get("current_turn_start") == 1, result["stats"])
        cfg["preserve_last_user"] = False
        result = self.preview(cfg, current_body)
        self.check("preserve_last_user=false allows current turn changes", result.get("applied") is True and result["stats"].get("first_changed_item") == 0, result["stats"])

        system_body = chat_body([
            {"role": "system", "content": "system  \n\n\n keep"},
            {"role": "developer", "content": "developer  \n\n\n keep"},
            {"role": "assistant", "content": "old  \n\n\n change"},
            {"role": "user", "content": "now"},
        ])
        cfg = engine_config("whitespace")
        cfg["protect_system"] = True
        result = self.preview(cfg, system_body)
        self.check("protect_system preserves system/developer", result.get("applied") is True and result["stats"].get("first_changed_item", 0) >= 2, result["stats"])
        cfg["protect_system"] = False
        result = self.preview(cfg, system_body)
        self.check("protect_system=false allows system changes", result.get("applied") is True and result["stats"].get("first_changed_item") == 0, result["stats"])

        # Model filters.
        cfg = engine_config("whitespace")
        cfg["model_allow"] = ["gpt-*"]
        result = self.preview(cfg, ws_body(), protocol="chat")
        self.check("model_allow permits matching model", result.get("applied") is True, result["stats"])
        denied = self.preview(cfg, chat_body([{"role": "assistant", "content": "old  \n\n\n text"}, {"role": "user", "content": "now"}], model="claude-test"), protocol="chat")
        self.check("model_allow blocks non-matching model", denied.get("applied") is False and denied["stats"].get("skipped_reason") == "model-not-allowed", denied["stats"])
        cfg["model_allow"] = ["*"]
        cfg["model_deny"] = ["gpt-*"]
        result = self.preview(cfg, ws_body(), protocol="chat")
        self.check("model_deny blocks matching model", result.get("applied") is False and result["stats"].get("skipped_reason") == "model-not-allowed", result["stats"])

        # Tool truncation parameters.
        cfg = engine_config("tool-truncate", allow_lossy=True)
        cfg["tool_truncate"] = {"max_chars": 256, "head_pct": 0.5, "tail_pct": 0.25, "min_chars": 0}
        result = self.preview(cfg, truncate_body())
        compressed_text = result["body"]["messages"][1]["content"]
        self.check("tool_truncate max_chars/head_pct/tail_pct apply", result.get("applied") is True and "A" * 100 in compressed_text and "B" * 50 in compressed_text and "tokensaver tool truncate" in compressed_text, compressed_text[:400])
        cfg["tool_truncate"]["min_chars"] = 100000
        result = self.preview(cfg, truncate_body())
        self.check("tool_truncate min_chars gates truncation", result.get("applied") is False, result["stats"])

        # Diagnostic trim parameters.
        cfg = engine_config("diagnostic-trim", allow_lossy=True)
        cfg["diagnostic_trim"] = {"max_lines": 8, "min_chars": 0, "preserve_error_lines": True}
        result = self.preview(cfg, diagnostic_body())
        self.check("diagnostic_trim max_lines applies", result.get("applied") is True and "diagnostic trim" in result["body"]["messages"][1]["content"], result["stats"])
        self.check("diagnostic_trim preserves error lines", "ERROR keep me" in result["body"]["messages"][1]["content"], result["body"]["messages"][1]["content"])
        cfg["diagnostic_trim"]["preserve_error_lines"] = False
        result = self.preview(cfg, diagnostic_body())
        self.check("diagnostic_trim can omit error lines", "ERROR keep me" not in result["body"]["messages"][1]["content"], result["body"]["messages"][1]["content"])
        cfg["diagnostic_trim"]["min_chars"] = 100000
        result = self.preview(cfg, diagnostic_body())
        self.check("diagnostic_trim min_chars gates trimming", result.get("applied") is False, result["stats"])

        # Protocol coverage.
        responses = {
            "model": "gpt-test",
            "input": [
                {"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "old  \n\n\n text"}]},
                {"type": "reasoning", "id": "rs_1", "encrypted_content": "opaque"},
                {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "now"}]},
            ],
        }
        result = self.preview(engine_config("whitespace"), responses, protocol="responses")
        self.check("Responses protocol compresses old message", result.get("applied") is True, result["stats"])
        self.check("Responses reasoning preserved", result["body"]["input"][1]["encrypted_content"] == "opaque", result["body"]["input"][1])

        anthropic = {
            "model": "claude-test",
            "system": [{"type": "text", "text": "system  \n\n\n keep", "cache_control": {"type": "ephemeral"}}],
            "messages": [
                {"role": "assistant", "content": [{"type": "text", "text": "old  \n\n\n text"}]},
                {"role": "user", "content": "now"},
            ],
        }
        result = self.preview(engine_config("whitespace"), anthropic, protocol="anthropic")
        self.check("Anthropic protocol compresses old message", result.get("applied") is True, result["stats"])
        self.check("Anthropic cache_control preserved", result["body"]["system"][0]["cache_control"]["type"] == "ephemeral", result["body"]["system"][0])

        # Cache-prefix observability. This is not a provider cache-hit measurement.
        cache_body = chat_body([
            {"role": "system", "content": "system  \n\n\n keep", "cache_control": {"type": "ephemeral"}},
            {"role": "assistant", "content": "old  \n\n\n text"},
            {"role": "user", "content": "now"},
        ], cache_control={"type": "ephemeral"})
        result = self.preview(engine_config("whitespace"), cache_body)
        self.check("cache fields preserved", result["body"].get("cache_control") == {"type": "ephemeral"} and result["body"]["messages"][0].get("cache_control") == {"type": "ephemeral"}, result["body"])
        self.check("cache prefix change is observable", result["stats"].get("first_changed_item") == 1, result["stats"])

        # Idempotency and fail-open.
        cfg = engine_config("whitespace")
        first = self.preview(cfg, ws_body())
        second = self.preview(cfg, first["body"])
        self.check("compression is idempotent", second.get("applied") is False, second["stats"])
        invalid_body = b"{not-json"
        status, _, payload = self.client.json("POST", PREVIEW_PATH, {"config": cfg, "protocol": "chat", "body": invalid_body.decode()})
        self.check("invalid JSON preview is fail-open", status == 200 and payload.get("applied") is False and payload["stats"].get("skipped_reason", "").startswith("unsupported-or-invalid-json"), payload)

        # Concurrency smoke test.
        def one(_: int) -> bool:
            status, _, payload = self.client.json("POST", PREVIEW_PATH, {"config": engine_config("whitespace"), "protocol": "chat", "body": ws_body()})
            return status == 200 and payload.get("applied") is True

        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
            outcomes = list(pool.map(one, range(24)))
        self.check("concurrent previews succeed", all(outcomes), outcomes)

        # Stats endpoint remains readable after the matrix.
        status, _, stats = self.client.json("GET", STATS_PATH)
        self.check("stats endpoint readable", status == 200 and isinstance(stats, dict) and "count" in stats, {"status": status, "stats": stats})


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--config-yaml", type=Path)
    parser.add_argument("--key-file", type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--timeout", type=float, default=20.0)
    args = parser.parse_args()

    if args.key_file:
        key = args.key_file.read_text(encoding="utf-8").strip()
    elif args.config_yaml:
        key = read_management_key(args.config_yaml)
    else:
        parser.error("provide --config-yaml or --key-file")
    if not key:
        raise SystemExit("empty management key")

    matrix = Matrix(Client(args.base_url, key, args.timeout))
    try:
        result = matrix.run()
    except Exception as error:
        result = {"ok": False, "error": str(error), "checks": matrix.checks}
        if args.output:
            args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print(json.dumps(result, ensure_ascii=False, indent=2))
        return 1
    if args.output:
        args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"ok": result["ok"], "checks": len(result["checks"]), "failed": [c for c in result["checks"] if not c["ok"]]}, ensure_ascii=False, indent=2))
    return 0 if result["ok"] else 1


if __name__ == "__main__":
    sys.exit(main())
