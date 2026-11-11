# 统一配置说明

两个平台共享 `config/core.example.json`，代理配置文件里的 `core` 字段嵌入
同一结构。修改任一平台时只改 Core JSON；CPA 插件通过 `config_path` 读取它，
代理通过 `core` 或管理 API 读取它。

## 1. Core

```json
{
  "enabled": true,
  "mode": "safe",
  "threshold_tokens": 512,
  "min_savings_tokens": 16,
  "preserve_prefix_items": 1,
  "preserve_last_user": true,
  "protect_system": true,
  "allow_lossy": false,
  "model_allow": ["*"],
  "model_deny": [],
  "engines": [
    {"id": "whitespace", "enabled": true},
    {"id": "json-minify", "enabled": true},
    {"id": "exact-dedup", "enabled": false},
    {"id": "diagnostic-trim", "enabled": false},
    {"id": "tool-truncate", "enabled": false}
  ],
  "tool_truncate": {"max_chars": 8000, "head_pct": 0.6, "tail_pct": 0.3, "min_chars": 9000},
  "diagnostic_trim": {"max_lines": 140, "min_chars": 12000, "preserve_error_lines": true},
  "logging": {"enabled": true, "max_entries": 500, "include_model": true, "include_engine": true}
}
```

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `enabled` | bool | true | 全局硬开关；false 时任何 header/模式都不能开启 |
| `mode` | enum | safe | off/safe/balanced/aggressive/custom |
| `threshold_tokens` | int | 512 | 估算 token 低于该值跳过 |
| `min_savings_tokens` | int | 16 | 节省不足则回退原 body |
| `preserve_prefix_items` | int | 1 | 永远不改的前缀 item 数 |
| `preserve_last_user` | bool | true | 保护最后一个 user turn 及之后 |
| `protect_system` | bool | true | system/developer 永不压缩 |
| `allow_lossy` | bool | false | 有损引擎总开关 |
| `model_allow` | []string | ["*"] | `*` 唯一通配符；空模型默认允许 |
| `model_deny` | []string | [] | 拒绝优先于 allow |
| `engines` | [] | 见上 | custom 模式的顺序/开关 |
| `tool_truncate` | object | 见上 | 仅 lossy |
| `diagnostic_trim` | object | 见上 | 仅 lossy |
| `logging` | object | 见上 | 进程内统计最近记录数 |

`logging.enabled=false` 会停止保留最近记录，但累计计数仍继续；`max_entries`
控制最近记录上限；`include_model` / `include_engine` 控制条目中是否保留对应
字段。

预设 `mode` 的引擎：

- `safe`：whitespace + json-minify；
- `balanced`：safe + diagnostic-trim；
- `aggressive`：balanced + tool-truncate；
- `custom`：只使用 `engines[]` 中 enabled=true 的项。

有损引擎在 preset 中也会被 `allow_lossy=false` 拦下；不能只靠切换 mode 打开
lossy。

## 2. Gateway

| 字段 | 默认 | 说明 |
|---|---|---|
| `adapter` | generic | generic/cpa/agent2api；只影响路径/profile 文案 |
| `listen` | 127.0.0.1:9231 | 不要默认改为 0.0.0.0 |
| `upstream` | 必填 | 不得带用户名/密码 |
| `management_enabled` | true | 管理 API 总开关 |
| `management_token_env` | TOKENSAVER_MANAGEMENT_TOKEN | token 环境变量名，不是 token 值 |
| `allow_remote_management` | false | 非 loopback 管理 API 必须显式开启 |
| `max_request_body_bytes` | 32 MiB | 超限原样转发 |
| `response_header_timeout_ms` | 120000 | 上游响应头超时 |
| `idle_conn_timeout_ms` | 90000 | 空闲连接回收 |
| `max_idle_conns` | 128 | 连接池 |
| `max_idle_conns_per_host` | 64 | 连接池 |
| `shutdown_timeout_ms` | 10000 | 优雅关闭 |

## 3. CPA 插件配置

`config/cpa-plugin.example.yaml` 是片段，不直接覆盖已有 config。插件注册字段：

```yaml
config_path: "/CLIProxyAPI/tokensaver-data/core.json"
```

如果宿主只传递 YAML 字符串而不暴露 `config_path`，使用插件 JSON 配置的
`config_json` 字段（测试路径）；推荐挂载共享文件。插件目录应保持只读，
`tokensaver-data/` 单独可写，管理页才能原子保存配置。

### 3.1 CPA 管理页与管理 API

管理页：

```text
/v0/resource/plugins/tokensaver-context-compression/ui
```

管理 API：

```text
GET  /v0/management/tokensaver/config
PUT  /v0/management/tokensaver/config
POST /v0/management/tokensaver/preview
GET  /v0/management/tokensaver/stats
```

使用 CPA 管理密钥认证，支持 `Authorization: Bearer <key>` 或
`X-Management-Key: <key>`。管理页会自动解码 CPA 管理页保存在
`localStorage['cli-proxy-auth']` 的密钥；无法自动读取时由用户手动输入，密钥
不写入 TokenSaver 配置或日志。

保存成功时返回 `X-TokenSaver-Persisted: true`。如果 `config_path` 不可写，
保存返回错误，不会只改内存却报告成功。预览和统计不会写入配置文件。

### 3.2 缓存影响

`protect_system=true` 会保护 System/Developer 内容，`preserve_last_user=true`
会保护当前用户请求及其后的工具输出；但压缩历史消息仍会改变 provider 可缓存的
前缀。支持缓存统计的 provider 可能在压缩后首次请求或前缀变化时出现 cache miss；
多轮对话中，上一轮受保护的当前消息可能在下一轮变成历史消息并被压缩，从而再次
改变前缀。

因此默认建议先用较高 `threshold_tokens` 或保持 `enabled=false`，并监控
`first_changed_item`、provider `cached_tokens`、错误率和费用后再开启。
多轮模拟结果与策略比较见 [cache-strategy.md](cache-strategy.md)。

## 4. 导入/导出

代理管理 API 直接接受完整 Gateway JSON：

```bash
curl -fsS -X PUT \
  -H "Authorization: Bearer $TOKENSAVER_MANAGEMENT_TOKEN" \
  -H "Content-Type: application/json" \
  --data-binary @config/gateway.example.json \
  http://127.0.0.1:9231/__tokensaver/config
```

`GET /__tokensaver/config` 导出。不要将管理 token 或上游凭据写入配置文件；
配置校验会拒绝 upstream URL 内的凭据。

## 5. 配置校验

```bash
scripts/check_configs.sh
```

会验证：

- gateway JSON 可解析且通过 Go `Validate()`；
- core JSON 可解析；
- gateway 的 `core` 与独立 core 文件完全一致；
- 监听地址、upstream scheme、有损阈值等边界。
