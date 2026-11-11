.PHONY: test race config bench build check-delivery fmt clean-check

test:
	go test ./... -count=1

race:
	go test ./... -count=1 -race -timeout=90s

config:
	scripts/check_configs.sh

bench:
	go run ./cmd/bench -fixtures tests/fixtures -iterations 100 -output /tmp/tokensaver-core.json
	TOKENSAVER_BENCH_LOSSY=1 go run ./cmd/bench -fixtures tests/fixtures -iterations 30 -output /tmp/tokensaver-core-large-lossy.json
	python3 scripts/run_gateway_perf.py --iterations 20

build:
	scripts/build_artifacts.sh

check-delivery:
	python3 scripts/check_delivery.py

fmt:
	gofmt -w cmd pkg tests scripts/cpa_abi_probe.go

clean-check:
	@find . \( -name '*.pyc' -o -name '*.tmp' \) -print | head
