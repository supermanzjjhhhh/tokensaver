# Generated performance fixtures

These are deterministic generated inputs used by `cmd/bench` and the local
mock gateway benchmark. They are not copied from production logs.

- `large-test.log`: 1,200 synthetic test-run lines with periodic errors/warnings.
- `large-server.log`: 2,499 synthetic request log lines with status/latency fields.
- `large.diff`: 800 synthetic paired diff hunks for head/tail truncation tests.

Generation is reproducible with the Python snippet in the task evidence; the
files are checked in to keep benchmark inputs byte-stable.
