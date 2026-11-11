# Deployment

TokenSaver has two deployment modes:

1. a standalone HTTP gateway;
2. a native CLIProxyAPI plugin.

They share the same Core configuration and can be enabled independently.

## Artifacts

| File | Purpose |
|---|---|
| `dist/linux-amd64/tokensaver` | standalone gateway, Linux AMD64 |
| `dist/linux-amd64/tokensaver-cpa.so` | CPA plugin, Linux AMD64 |
| `dist/linux-arm64/tokensaver` | standalone gateway, Linux ARM64 |
| `dist/linux-arm64/tokensaver-cpa.so` | CPA plugin, Linux ARM64 |
| `config/gateway.example.json` | gateway example |
| `config/core.example.json` | shared Core example |
| `config/cpa-plugin.example.yaml` | CPA plugin configuration fragment |

Verify artifact hashes with `dist/SHA256SUMS`.

## Standalone gateway

```bash
./tokensaver -config /etc/tokensaver/gateway.json -check
./tokensaver -config /etc/tokensaver/gateway.json
```

Default behavior:

- listen on `127.0.0.1:9231`;
- forward to the configured upstream;
- expose management endpoints only on loopback unless explicitly configured otherwise;
- never write request bodies or credentials to disk.

Example container:

```yaml
services:
  tokensaver:
    image: scratch
    command: ["/tokensaver", "-config", "/etc/tokensaver/gateway.json"]
    volumes:
      - /opt/tokensaver/tokensaver:/tokensaver:ro
      - /etc/tokensaver/gateway.json:/etc/tokensaver/gateway.json:ro
    ports:
      - "127.0.0.1:9231:9231"
    mem_limit: 128m
    memswap_limit: 128m
    cpus: 0.5
    pids_limit: 64
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    restart: unless-stopped
```

For container deployment, set the gateway listener to `0.0.0.0:9231` inside the container while keeping the host port bound to loopback or a private interface.

## CPA plugin

### Layout

```text
/CLIProxyAPI/plugins/linux/amd64/tokensaver-context-compression.so
/CLIProxyAPI/tokensaver-data/core.json
```

Keep `plugins/` read-only. Mount `tokensaver-data/` separately as writable so the management page can persist configuration.

### Compose mount

```yaml
volumes:
  - ./plugins:/CLIProxyAPI/plugins:ro
  - ./tokensaver-data:/CLIProxyAPI/tokensaver-data:rw
```

### CPA configuration

```yaml
plugins:
  enabled: true
  dir: plugins
  configs:
    tokensaver-context-compression:
      enabled: true
      priority: 100
      config_path: /CLIProxyAPI/tokensaver-data/core.json
```

`config_path` must be absolute and writable.

### Recreate after mount changes

```bash
docker compose up -d --force-recreate cli-proxy-api
```

A restart is not enough when the container mount list changes.

### Verify

```bash
docker inspect cli-proxy-api --format '{{json .Mounts}}'
docker logs --since 5m cli-proxy-api 2>&1 | grep -Ei 'tokensaver|pluginhost'
```

The host should report the plugin as registered and effective.

## Management UI

```text
/v0/resource/plugins/tokensaver-context-compression/ui
```

The page uses CPA management authentication. It can read the saved CPA management key from the management UI when possible, or accept it manually. The key is never written into TokenSaver configuration or logs.

## Resource planning

Start with conservative limits and measure actual usage:

- gateway: 128 MiB memory, 0.5 CPU, 64 PIDs;
- CPA plugin: keep the host’s existing limits and leave headroom for the host process;
- never rely on swap to cover sustained memory pressure.

## Rollback

1. restore the previous CPA configuration and Compose file;
2. restore the previous plugin binary;
3. recreate the container;
4. confirm the plugin is no longer registered or is back on the previous version;
5. keep the writable data directory until you no longer need rollback.

## Security

- Do not expose management endpoints to untrusted networks.
- Keep plugin directories read-only.
- Do not commit management keys, provider credentials, or production configuration.
- Use a reverse proxy with TLS if management or inference endpoints leave loopback.
