# Compatibility

## Supported request families

- OpenAI Chat Completions (`messages[]`)
- OpenAI Responses / Codex (`input[]`)
- Anthropic Messages (`system` + `messages[]`)

## CPA plugin

The plugin targets CLIProxyAPI plugin schema version 10 and declares:

```json
{
  "request_interceptor": true,
  "management_api": true
}
```

It was exercised against CLIProxyAPI v8.0.60 at the C ABI, registration, request interception, management API, and management UI levels. Newer or older host versions may change the plugin schema or management route contract; verify against your version.

## Standalone gateway

The gateway is a plain HTTP reverse proxy for `/v1/chat/completions`, `/v1/responses`, and `/v1/messages`. It does not depend on a specific Agent2API runtime and can be placed in front of any compatible upstream.

## Provider behavior

Token estimates are heuristic. Provider tokenizers, context limits, cache behavior, and request validation are provider-specific. Always validate with the provider and model you intend to use.

## Platforms

Prebuilt artifacts are provided for Linux AMD64 and ARM64. The CPA plugin is a native shared library and must match the host architecture and libc environment.
