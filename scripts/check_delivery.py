#!/usr/bin/env python3
"""Static delivery gate for the public TokenSaver tree."""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
REQUIRED = [
    "README.md", "LICENSE", "go.mod", "go.sum", "Dockerfile", "Makefile",
    ".gitignore", ".dockerignore", "SECURITY.md", "CONTRIBUTING.md",
    ".github/workflows/ci.yml",
    "docs/architecture.md", "docs/configuration.md", "docs/deployment.md",
    "docs/compatibility.md", "docs/limitations.md", "docs/cache-strategy.md",
    "docs/testing.md", "docs/benchmarks.md", "docs/assets/management-ui.png",
    "config/core.example.json", "config/gateway.example.json",
    "config/cpa-plugin.example.yaml",
    "pkg/core/compress.go", "pkg/core/config.go", "pkg/core/stats.go",
    "pkg/core/cache_stability_test.go",
    "pkg/adapters/cpa_plugin.go", "pkg/gateway/server.go",
    "cmd/tokensaver/main.go", "cmd/cpaplugin/main.go",
    "cmd/cpaplugin/ui.go", "cmd/cpaplugin/assets/ui.html",
    "tests/integration/protocol_test.go", "tests/integration/gateway_e2e_test.go",
    "scripts/check_configs.sh", "scripts/final_checks.sh",
    "scripts/verify_management_matrix.py",
    "dist/linux-amd64/tokensaver", "dist/linux-amd64/tokensaver-cpa.so",
    "dist/linux-arm64/tokensaver", "dist/linux-arm64/tokensaver-cpa.so",
    "dist/SHA256SUMS",
    "licenses/THIRD_PARTY_NOTICES.md", "licenses/OmniRoute.MIT.LICENSE",
]
missing = [p for p in REQUIRED if not (ROOT / p).is_file()]
if missing:
    print("MISSING:", *missing, sep="\n  ")
    sys.exit(1)

gateway = json.loads((ROOT / "config/gateway.example.json").read_text())
core = json.loads((ROOT / "config/core.example.json").read_text())
if gateway.get("core") != core:
    print("gateway/core config examples drifted")
    sys.exit(1)

patterns = [
    re.compile(r"100\.115\.13\.68"),
    re.compile(r"100\.101\.166\.120"),
    re.compile(r"1\.12\.63\.113"),
    re.compile(r"VM-0-16-ubuntu"),
    re.compile(r"/data/workspace/cliproxyapi"),
    re.compile(r"gpt-5\.6-luna"),
    re.compile(r"BEGIN (RSA |OPENSSH |EC |DSA )?PRIVATE KEY"),
    re.compile(r"gh[pousr]_[A-Za-z0-9_]{20,}"),
    re.compile(r"github_pat_[A-Za-z0-9_]{20,}"),
    re.compile(r"AKIA[0-9A-Z]{16}"),
    re.compile(r"xox[baprs]-[A-Za-z0-9-]{10,}"),
    re.compile(r"sk-[A-Za-z0-9_-]{20,}"),
    re.compile(r"evidence/"),
]
hits: list[str] = []
for path in ROOT.rglob("*"):
    if not path.is_file() or ".git" in path.parts or path.resolve() == Path(__file__).resolve():
        continue
    if path.suffix.lower() in {".so", ".png", ".jpg", ".jpeg", ".webp", ".gif"}:
        continue
    try:
        text = path.read_text(errors="ignore")
    except Exception:
        continue
    for pattern in patterns:
        if pattern.search(text):
            hits.append(f"{path.relative_to(ROOT)}: {pattern.pattern}")
if hits:
    print("SENSITIVE PATTERN HITS:", *hits, sep="\n  ")
    sys.exit(1)

print("delivery gate OK")
