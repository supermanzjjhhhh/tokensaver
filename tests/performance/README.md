# Performance tests

Reproducible local benchmarks:

```bash
go run ./cmd/bench -fixtures tests/fixtures -iterations 100 \
  -output /tmp/tokensaver-core.json
TOKENSAVER_BENCH_LOSSY=1 go run ./cmd/bench -fixtures tests/fixtures \
  -iterations 30 -output /tmp/tokensaver-core-large-lossy.json
python3 scripts/run_gateway_perf.py --iterations 20
go run ./cmd/adaptercompare -fixtures tests/fixtures -iterations 10 \
  -output /tmp/tokensaver-adapter-comparison.json
go test ./pkg/core -run '^$' -bench BenchmarkSafeChat -benchmem -count=3
```

These do not call external services. The `gateway-mock` case uses a local Python
HTTP server and a static response. See [../../docs/benchmarks.md](../../docs/benchmarks.md).
