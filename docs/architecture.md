# TokenSaver 架构说明

## 1. 目标与非目标

目标：一个共享、轻量、无数据库、默认 fail-open 的上下文压缩 Core；
CPA 与 Agent2API 共用配置和统计口径；两种平台适配可以独立启用/回滚。

非目标：重新实现 Provider/账号/额度、跨请求状态存储、模型调用型摘要、
图像编码或生产自动部署。

## 2. 组件图

```
                    +----------------------------------+
                    | TokenSaver Core (pkg/core)       |
                    | parse / policy / engines / stats |
                    +----------------+-----------------+
                                     |
              +----------------------+----------------------+
              |                                             |
   +----------v-----------+                      +----------v-----------+
   | CPA Plugin Adapter   |                      | HTTP Gateway Adapter |
   | cmd/cpaplugin (.so)  |                      | cmd/tokensaver       |
   | C ABI + JSON RPC     |                      | reverse proxy        |
   +----------+-----------+                      +----------+-----------+
              |                                             |
       CLIProxyAPI v8.x                         Agent2API-compatible upstream
       request.intercept_before/after              /v1/{chat,responses,messages}
```

## 3. Core 数据流

```
raw request bytes
  -> protocol detect (chat | anthropic | responses)
  -> guard: disabled / model allow-deny / below threshold / invalid shape
  -> document parse (conservative JSON model; no struct round-trip)
  -> protected-zone calculation
       prefix items / system+developer / current user turn / current tool outputs
  -> engines in deterministic order
       whitespace -> json-minify -> [exact-dedup] -> [diagnostic-trim] -> [tool-truncate]
  -> re-marshal JSON
  -> min-savings check
  -> Stats {before/after tokens, savings, latency, engines, first changed item}
```

所有异常返回原 body。压缩结果必须是合法 JSON；否则 fail-open。Core 不读取
宿主数据库或运行时模块，也不访问网络。

## 4. 协议保护策略

### Chat Completions

- 保护 `system`、`developer`；保护 current user 及其后所有 item。
- 仅压缩 `content` 为 string 或已知 text part 的较早消息。
- Tool message：保护 `tool_call_id`、`name`、`tool_calls`；当前 turn 的 tool output
  不压缩；旧 tool output 允许 JSON minify/诊断缩减/截断（后两者需 lossy）。
- 顶层 `tools`、`response_format`、`reasoning_effort`、缓存字段不触碰。

### Anthropic Messages

- 顶层 `system` 不触碰。
- `tool_use` / image / document / thinking / unknown block 不触碰。
- 旧 `tool_result.content` 可做工具输出压缩；`tool_use_id` 保持原值。
- 当前 user message 及其中 tool result 不压缩。

### OpenAI Responses / Codex CLI

- `instructions` 不触碰。
- 支持 `input[]`，保护 `function_call`、`reasoning`、message role system/developer
  和当前 user turn 后的 `function_call_output`。
- 仅压缩较早 `function_call_output.output`；call_id/name/arguments 保持原值。
- `input` 为字符串或非数组时整体 pass-through。

### 多模态/未知形状

任何不是上述明确 shape 的内容都保留 JSON 原值。`type` 未知的 content block
不进入压缩。无法解析的 body 不返回 400 给上游，而是原样透传。

## 5. 引擎

| Engine | 默认 | 有损 | 作用 |
|---|---|---|---|
| `whitespace` | 开 | 否 | 去行尾空白、压缩 3+ 换行；fenced code 原样 |
| `json-minify` | 开 | 否 | 仅完整 JSON tool output |
| `exact-dedup` | 关 | 否 | 相邻同 role 完全重复消息；不碰 tool id/call 字段 |
| `diagnostic-trim` | 关 | 是 | ANSI/进度清理、重复行折叠、保留 error/warning 行 |
| `tool-truncate` | 关 | 是 | 旧 tool output 超阈值时保留头+尾 |

`mode` 预设：

- `safe`：whitespace + json-minify；
- `balanced`：safe + diagnostic-trim；
- `aggressive`：balanced + tool-truncate；
- `custom`：只用 `engines[]` 中 enabled 的项；
- `off`：整体关闭。

即使 mode 预设包含有损引擎，只要 `allow_lossy=false`，Core 也不会执行它们。

## 6. Adapter A：CPA Plugin

- 编译为 Go `c-shared`，导出宿主要求的 4 个符号：
  `cliproxy_plugin_init`、`cliproxyPluginCall`、`cliproxyPluginFree`、
  `cliproxyPluginShutdown`。
- 注册 `request_interceptor: true`、`management_api: true`，schema version 10。
- `request.intercept_before`：解析 host 传入的 `RequestInterceptRequest`，
  调用 Core，返回 base64 编码 body（Go `[]byte` JSON 默认编码）。
- `request.intercept_after`：原样返回 body/headers，不改认证。
- 配置：`config_yaml` 中的 `config_path` 指向共享 `core.example.json`；
  或使用插件 JSON `config_json` 传给本地测试。
- 管理面：注册 `/tokensaver/config`、`/tokensaver/preview`、
  `/tokensaver/stats` 管理路由，以及 `/ui` 浏览器资源；管理页嵌入在插件
  动态库中，不依赖外部静态文件。
- 持久化：管理页保存时先校验，再通过临时文件 + rename 原子写入
  `config_path`；插件目录保持只读，配置放在独立可写数据目录。
- 失败：返回 `{"ok":false,...}` 或空 result；宿主保留原 body。

## 7. Adapter B：Agent2API Forward Proxy

- Go `net/http` 独立进程，不依赖 Agent2API 内部模块。
- 路径白名单决定是否压缩；`/v1/messages/count_tokens` 与其他路径
  pass-through。
- 请求体读取有硬上限；超限时继续转发原 body（不截断流）。
- 转发时复制 end-to-end headers，剔除 hop-by-hop 和 TokenSaver 自身头。
- 响应按原 Content-Type 转发；SSE 逐块 flush。
- `http.Client` 继承 request context；取消/超时传至上游。
- 无重试、无模型路由、无账号处理、无额度逻辑。

## 8. 管理面

Agent2API 前置代理只提供：

- `GET /__tokensaver/health`：无认证但只暴露版本/模式；
- `GET /__tokensaver/config`、`PUT /__tokensaver/config`：
  查看/替换完整配置并原子写回；
- `POST /__tokensaver/reload`：从磁盘重载；
- `POST /__tokensaver/import`：导入配置；
- `POST /__tokensaver/preview`：前后 body + stats；
- `GET /__tokensaver/stats`：累计/近期统计。

默认只监听 loopback；若配置管理 token 环境变量，管理请求必须带 Bearer。
`allow_remote_management=true` 才允许非 loopback 监听，且必须有管理 token。该
配置不会自动打开生产端口。

CPA 插件管理面使用 CPA 自身的 Management API 认证：

- `GET/PUT /v0/management/tokensaver/config`；
- `POST /v0/management/tokensaver/preview`；
- `GET /v0/management/tokensaver/stats`；
- `GET /v0/resource/plugins/tokensaver-context-compression/ui`。

管理页从 CPA 管理页保存的 `localStorage['cli-proxy-auth']` 读取管理密钥，
读取失败时要求手动输入；密钥不写入 TokenSaver 配置。

## 9. 可观测性与隐私

- 统计仅保留 path、model、protocol、token 估算、耗时、引擎名；
- 不记录 body、headers、Authorization、cookie 或 API key；
- 不把请求内容写入日志或数据库；
- management token 从环境变量读取，不写配置文件；
- 文档和日志中不要包含凭据、内网地址或生产容器细节。
