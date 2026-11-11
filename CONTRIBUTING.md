# Contributing

Thanks for considering a contribution.

## Development setup

Requirements:

- Go 1.22 or newer;
- a C toolchain for the CPA plugin (`CGO_ENABLED=1`);
- Python 3 for configuration and management-matrix checks.

Run the local checks:

```bash
make test
make race
make config
make check-delivery
```

Build the plugin:

```bash
CGO_ENABLED=1 go build -buildmode=c-shared -o /tmp/tokensaver-cpa.so ./cmd/cpaplugin
```

## Pull requests

- Keep changes focused and explain the behavior being changed.
- Preserve fail-open behavior: invalid or unsupported input must pass through unchanged.
- Add tests for new protocol shapes, engines, management behavior, or cache-sensitive behavior.
- Run `go test ./...`, `go test -race ./...`, and `go vet ./...`.
- Do not commit credentials, generated caches, local configuration, or deployment evidence.
- Update `docs/` and `README.md` when behavior or configuration changes.

## Design constraints

TokenSaver intentionally avoids:

- model calls;
- a database;
- provider account or quota management;
- rewriting tool-call identifiers or schema fields;
- silent lossy compression.

If a change weakens one of these constraints, explain why in the pull request.
