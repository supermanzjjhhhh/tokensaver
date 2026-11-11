# Limitations and Risks

## Token estimates

Token counts use a deterministic heuristic. They are not provider billing values and can differ from the model tokenizer.

## Prompt cache

Compression changes the request prefix. This can cause prompt-cache misses, especially in multi-turn conversations. See [cache-strategy.md](cache-strategy.md).

## Lossy engines

`diagnostic-trim` and `tool-truncate` remove content. They are disabled by default and require `allow_lossy=true`. Validate output quality on your own workloads before enabling them.

## Provider validation

A request can be valid JSON but still be rejected by a provider-specific schema. TokenSaver preserves unknown fields and fails open, but it does not validate provider-specific request semantics.

## Streaming

The gateway supports SSE passthrough. The CPA plugin is a request interceptor; response behavior is controlled by the host.

## Long-running behavior

The default test suite covers functional behavior, race detection, and short benchmarks. It does not provide long-duration load, memory-leak, or failure-injection guarantees.

## Platform support

Prebuilt artifacts target Linux AMD64/ARM64. macOS and Windows plugin builds are not included.
