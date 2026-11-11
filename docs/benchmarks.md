# Benchmarks

These are local, deterministic measurements from the included fixtures. They are useful for regression tracking, not as provider billing or production SLA numbers.

## Core compression

| Input | Engine path | Approx. time |
|---|---|---:|
| small chat/tool fixture | safe | 0.02–0.03 ms |
| ~237 KB log fixture | whitespace | ~8.3 ms |
| large diff/log fixtures | lossy paths | 16–70 ms |

Lossy fixtures reduce estimated tokens substantially, but the exact ratio depends on the input shape and configuration.

## Gateway

A local mock upstream run completed 20/20 requests. End-to-end p50 was around 44 ms, dominated by the mock HTTP path; core compression p50 was around 0.04–0.40 ms.

## Reproduce

```bash
go run ./cmd/bench -fixtures tests/fixtures -iterations 100 \
  -output /tmp/tokensaver-core.json
python3 scripts/run_gateway_perf.py --iterations 20
```

Do not compare absolute numbers across different machines, Go versions, or fixture sets.
