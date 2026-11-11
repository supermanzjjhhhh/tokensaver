#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
go run ./cmd/tokensaver -config "$root/config/gateway.example.json" -check
python3 -m json.tool "$root/config/core.example.json" >/dev/null
python3 -m json.tool "$root/config/gateway.example.json" >/dev/null
python3 - "$root" <<'PY'
import json,sys
from pathlib import Path
root=Path(sys.argv[1])
g=json.loads((root/'config/gateway.example.json').read_text())
c=json.loads((root/'config/core.example.json').read_text())
assert g['core']==c, 'gateway.example.json core and core.example.json drifted'
print('config examples OK and share one core structure')
PY
