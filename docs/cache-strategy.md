# Prompt Cache and Multi-Turn Stability

TokenSaver is deterministic, but compression changes the request prefix. Providers that cache prompt prefixes can miss the cache when historical messages are rewritten.

## What was measured

A six-turn synthetic conversation was compressed with two policies:

| Policy | Applied | Turns with a changed prefix |
|---|---:|---:|
| default safe, `threshold_tokens=0` | every turn | 5 / 6 |
| high threshold, `threshold_tokens=1,000,000` | none | 0 / 6 |

The default policy protects the current user turn. When the next turn arrives, the previous current user message becomes historical and can be compressed. That changes the prefix again.

## Practical guidance

- For cache-sensitive workloads, keep `enabled=false` or use a high `threshold_tokens` close to the model context limit.
- Do not enable `diagnostic-trim` or `tool-truncate` without evaluating output quality.
- Watch `first_changed_item` in stats and provider `cached_tokens` when available.
- If you need compression on every turn, consider implementing a stateful compressed-prefix reuse strategy instead of recompressing history each time.

## Important caveat

A changed prefix is not automatically a billing cache miss: cache TTL, minimum token count, routing, and provider behavior vary. Measure with your own provider and traffic.
