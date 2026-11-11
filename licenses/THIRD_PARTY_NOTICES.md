# Third-Party Notices

TokenSaver Core is an independent implementation. It does not copy OmniRoute
runtime code. OmniRoute source was used only as behavioral evidence and for
fixture-derived comparisons, with attribution below.

| Component | Version / revision | License | Use in this repository |
|---|---|---|---|
| OmniRoute | source 68225f5 (v3.8.52), runtime 048d559 (v3.8.53) | MIT | Read-only behavioral reference; several test fixtures are copied verbatim into `tests/fixtures/` |
| CLIProxyAPI | v8.0.60, commit 91e0dee | MIT | Plugin ABI and request-interceptor protocol reference only |
| Agent2API | runtime image 3.4.2, source revision f821c109 | AGPL-3.0 | Route/plugin-host boundary analysis reference only; no source copied |
| gopkg.in/yaml.v3 | v3.0.1 | MIT / Apache-2.0 | Runtime dependency for CLIProxyAPI plugin configuration |
| gopkg.in/check.v1 | v0.0.0-20161208181325 | BSD-3-Clause | Indirect test-only dependency of yaml.v3 |

## OmniRoute fixture attribution

The following files were copied byte-for-byte from OmniRoute
`tests/unit/compression/` for local behavioral comparison:

- `tests/fixtures/long-coding-session.json`
- `tests/fixtures/rtk/git-diff-sample.txt`
- `tests/fixtures/rtk/eslint-output-sample.txt`
- `tests/fixtures/rtk/docker-logs-sample.txt`
- `tests/fixtures/rtk/build-vite-sample.txt`

Upstream project: https://github.com/cheaper-inference/OmniRoute
Local source revision: `68225f5933c5b4d30ca7eeb7a71bdd07b95ddd92`
License: MIT (see OmniRoute repository `LICENSE`).

## CLIProxyAPI

Upstream project: https://github.com/router-for-me/CLIProxyAPI
Reference revision: `91e0dee827abf476f9f86246f68c500a112f7155`
License: MIT. The plugin uses the documented C ABI and JSON RPC method names
(`plugin.register`, `request.intercept_before`, `request.intercept_after`) from
that revision. No upstream Go source is imported or compiled into TokenSaver.

## Agent2API

Upstream project: https://github.com/aimod-cc/Agent2API
Runtime image labels revision: `f821c1096bab3ffe30443465e8fa090706c13c6c`
(public source history for that exact commit was not available; the nearest
public `main` tree was used only to confirm plugin-pack provider boundaries).
License: AGPL-3.0. No Agent2API code is linked or copied. The forward proxy is
an independent HTTP process and communicates only over documented HTTP.

## Distribution boundary

- The TokenSaver Core and forward proxy binaries contain no OmniRoute,
  CLIProxyAPI, or Agent2API source.
- The CPA plugin links `gopkg.in/yaml.v3` only.
- Distribution of the CPA plugin alongside CLIProxyAPI does not relicense
  TokenSaver; the plugin remains under this repository's license.
