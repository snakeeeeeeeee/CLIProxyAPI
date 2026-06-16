# 二次开发功能使用说明

本文档说明本仓库二次开发的 Claude Code Account Pool、代理 IP 池、观测日志和 Trace 工具链如何使用。

## 1. 构建

推荐使用统一脚本：

```bash
./scripts/build-all.sh
```

脚本会执行：

```bash
npm run build --prefix web/resource-console
go build -o cli-proxy-api ./cmd/server
```

也可以手动构建：

```bash
npm run type-check --prefix web/resource-console
npm run build --prefix web/resource-console
go build -o cli-proxy-api ./cmd/server
```

## 2. 配置入口

`config.yaml` 中开启资源池：

```yaml
resource-pools:
  enabled: true
  config-file: "resource-pools.yaml"
```

首次部署可以复制示例：

```bash
cp resource-pools.example.yaml resource-pools.yaml
```

默认数据会写入：

```text
resource-pools.db
```

注意：

- `resource-pools.yaml` 只作为首次初始化和默认配置入口。
- 运行时变更以 SQLite 为准。
- `resource-pools.db` 不要提交到 Git。

## 3. 启动服务

本地启动：

```bash
./cli-proxy-api --config config.yaml
```

开发时也可以直接：

```bash
go run ./cmd/server --config config.yaml
```

默认端口按 `config.yaml` 的 `port` 字段，例如：

```text
http://127.0.0.1:28317
```

## 4. 资源池控制台

入口：

```text
http://127.0.0.1:28317/account-pool.html
```

常用页面：

```text
http://127.0.0.1:28317/account-pool.html#/accounts
http://127.0.0.1:28317/account-pool.html#/proxies
```

登录说明：

- 使用 `config.yaml` 里的 `remote-management.secret-key` 对应的管理密钥登录。
- 资源池控制台有独立登录态。
- 不和旧的 `management.html` 共享登录状态。

## 5. 代理 IP 池

页面：

```text
/account-pool.html#/proxies
```

支持能力：

- 新增代理。
- 编辑代理。
- 删除代理。
- 批量导入。
- 批量测试。
- 批量启用。
- 批量禁用。
- 批量解绑。
- 批量删除。
- 查看健康状态、延迟、连续失败次数、最近错误、绑定账号。

代理健康检查由后端 worker 定时执行。

默认配置示例：

```yaml
proxy-health:
  enabled: true
  interval: "5m"
  timeout: "10s"
  concurrency: 8
  failure-threshold: 3
  test-url: "https://api.anthropic.com/"
  optional-exit-ip-url: "https://api.ipify.org?format=json"
```

绑定规则：

- 一个代理最多绑定一个 Claude Code 账号。
- 一个 Claude Code 账号最多绑定一个代理。
- 账号可以不绑定代理。
- 账号绑定代理后，OAuth refresh 和推理请求都走该代理。

## 6. Claude Code 账号池

页面：

```text
/account-pool.html#/accounts
```

支持能力：

- OAuth 登录 Anthropic 账号。
- 选择登录出口：
  - 直连
  - 指定代理
  - 自动选择空闲代理
- 绑定或解绑代理 IP。
- 启用、禁用、删除账号。
- 测试账号。
- 刷新额度。
- 清除冷却。
- 批量操作账号。

账号卡片展示：

- 健康度。
- 请求成功率。
- 5 小时额度。
- 7 天额度。
- 代理绑定状态。
- 并发容量。
- RPM。
- Sticky buffer。

## 7. 专属公开 API

Claude Code Account Pool 使用独立 API 前缀，不影响原 `/v1/*`。

Claude-native 客户端：

```text
Base URL: http://127.0.0.1:28317/claude-acc-pool
Path: /v1/messages
API Key: config.yaml 里的 api-keys
```

OpenAI-compatible 客户端：

```text
Base URL: http://127.0.0.1:28317/claude-acc-pool/v1
API Key: config.yaml 里的 api-keys
Model: 账号池模型表里的对外模型名
```

示例请求：

```bash
curl -sS http://127.0.0.1:28317/claude-acc-pool/v1/messages \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "claude-opus-4-8",
    "max_tokens": 128,
    "messages": [
      {
        "role": "user",
        "content": "hi"
      }
    ]
  }'
```

## 8. 模型管理

账号池支持独立模型表。

字段：

- 真实模型名。
- 对外模型名 alias。
- 启用状态。
- 来源。
- 备注。

说明：

- `/claude-acc-pool/v1/models` 只返回启用模型。
- 请求进入账号池 API 后，会把 alias 翻译成真实模型名。
- 可以手动新增模型。
- 可以选择某个 OAuth 账号拉取模型。

## 9. 调度和容量

账号池调度只选择 `claude_oauth_pool=true` 的 OAuth 账号。

不会混用：

- 原 `/v1/*`
- Claude API Pool
- 普通 Claude auth

账号容量包含：

- `base_rpm`
- `concurrency_limit`
- `max_sessions`
- `sticky_buffer`

卡片展示：

```text
并发 in_flight / concurrency_limit
RPM rpm_used / rpm_limit
Sticky buffer buffer_used / sticky_buffer
```

调度会考虑：

- 账号是否启用。
- 账号健康度。
- 模型可用性。
- 额度状态。
- 代理健康度。
- 当前并发。
- RPM 压力。
- 冷却状态。
- 最近错误。
- session affinity。

## 10. 纯净 input_tokens

配置位置：

```text
Claude Code 账号池 -> 配置 -> 净输入用量
```

作用：

- 只改写对外返回和 UI 展示的 `input_tokens`。
- 不影响 Anthropic 真实请求。
- 不影响 Anthropic 真实消耗。
- 不影响额度和限速判断。

校准机制：

- 按 `model + profile_fingerprint` 保存系统提示词 overhead。
- fingerprint 变化后旧校准不会继续命中。
- 未校准时使用默认估算值。

注意：

- 真实 usage ledger 仍统计 Anthropic 原始 usage。
- 这个能力只用于对外展示和内部计费口径。

## 11. 账号池日志

默认日志文件：

```text
acc-pool-logs/account-pool.log
```

配置位置：

```text
Claude Code 账号池 -> 配置 -> 账号池日志
```

支持：

- 启用或禁用。
- 日志等级：debug、info、warn、error。
- 单文件大小。
- 保留文件数。
- 脱敏开关。
- 清空日志。
- 下载日志。

日志记录：

- 选号。
- 成功。
- 上游错误。
- 本地拒绝。
- 冷却。
- 重试。
- 换号。
- usage 写入。

不会记录：

- 用户正文。
- 完整 API key。
- 完整 OAuth token。
- 完整代理 URL。

## 12. 运行指标和事件流

账号池页面通过 SSE 刷新。

事件流接口：

```text
/v0/management/resource-pools/events
```

刷新内容：

- 运行指标。
- usage。
- 调度事件。
- 日志。
- 账号卡片。
- 代理状态。

如果一次请求触发多次刷新，这是正常现象，因为请求会分别写入 usage、账号结果和 routing event。

## 13. Trace 工具链

Trace 工具用于对齐真实 Claude Code 请求形态。

### 录制真实 Claude Code trace

```bash
go run ./cmd/claude_trace_recorder \
  --listen 127.0.0.1:39001 \
  --upstream https://api.anthropic.com \
  --out traces/real
```

然后让 Claude Code CLI 指向 recorder：

```bash
ANTHROPIC_BASE_URL=http://127.0.0.1:39001 claude
```

### 开启账号池 outbound dump

在 `resource-pools.yaml` 中配置：

```yaml
trace:
  enabled: true
  dump-dir: "traces/ours"
  redact-user-content: true
```

只对 `/claude-acc-pool/v1` 生效。

### 对比 trace

```bash
go run ./cmd/claude_trace_diff \
  --real traces/real \
  --ours traces/ours \
  --out traces/report.md
```

注意：

- `traces/` 是本地运行产物，已经加入 `.gitignore`。
- trace 文件默认不提交。
- 只应把严格脱敏后的最小 fixture 放进测试目录。

## 14. Profile 基线

账号池页面支持拉取 Phistory 的 Claude Code profile 快照。

用途：

- 查看真实 Claude Code 版本。
- 对比 headers、beta、system prompt。
- 生成差异报告。

限制：

- 只做参考。
- 不自动应用到生产请求。
- 不把 Phistory 的完整超长 prompt 自动注入账号池业务。

## 15. 常见排障

### OAuth 失效

现象：

```text
invalid_grant
Invalid authentication credentials
```

处理：

- 在账号池页面重新 OAuth 登录。
- 确认绑定代理可用。
- 重新测试账号。

### 本地端口占用

查看端口：

```bash
lsof -iTCP:28317 -sTCP:LISTEN -n -P
```

如果是 Docker 占用：

```bash
docker ps
docker stop <container>
```

### 运行指标不刷新

检查：

- 页面是否已登录。
- `/v0/management/resource-pools/events` 是否能连上。
- 后端是否有写入 `claude_code_routing_events`。
- `acc-pool-logs/account-pool.log` 是否有新日志。

### 不要提交的文件

以下文件或目录不应提交：

```text
config.yaml
resource-pools.yaml
resource-pools.db*
claude-api-pool.db*
acc-pool-logs/
traces/
*.nohup.log
web/resource-console/dist/
web/resource-console/node_modules/
```

