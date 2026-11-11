# Testing

## Local test suite

```bash
go test ./... -count=1
go test ./... -count=1 -race -timeout=90s
go vet ./...
scripts/check_configs.sh
```

## What is covered

- OpenAI Chat Completions, Responses/Codex, and Anthropic Messages request shapes;
- tool-call and tool-result linkage preservation;
- system/developer and current-turn protection;
- JSON schema, reasoning, encrypted, cache-control, and unknown multimodal fields;
- safe and lossy engine behavior;
- deterministic/idempotent compression;
- invalid and unsupported input pass-through;
- gateway SSE passthrough and cancellation;
- management API authentication and configuration persistence;
- CPA plugin ABI registration and request interception;
- multi-turn prefix-stability analysis.

## CPA ABI probe

On a matching architecture:

```bash
go run scripts/cpa_abi_probe.go dist/linux-amd64/tokensaver-cpa.so
```

or:

```bash
go run scripts/cpa_abi_probe.go dist/linux-arm64/tokensaver-cpa.so
```

The probe loads the shared library, calls `cliproxy_plugin_init`, registers the plugin, and sends a sample `request.intercept_before` request.

## Management matrix

`scripts/verify_management_matrix.py` exercises every Core switch and compression setting against a live CPA management API. It requires a running CPA instance and a management key file:

```bash
python3 scripts/verify_management_matrix.py \
  --base-url http://127.0.0.1:8317 \
  --key-file /path/to/management.key \
  --output /tmp/management-matrix.json
```

Use it against a test instance first. It restores the original configuration when finished.

## Benchmarks

```bash
go run ./cmd/bench -fixtures tests/fixtures -iterations 100 \
  -output /tmp/tokensaver-core.json
python3 scripts/run_gateway_perf.py --iterations 20
```

Benchmark numbers are local and environment-dependent. They are not provider billing measurements.
