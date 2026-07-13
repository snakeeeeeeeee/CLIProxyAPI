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
http://127.0.0.1:28317/account-pool.html#/
http://127.0.0.1:28317/account-pool.html#/pools
http://127.0.0.1:28317/account-pool.html#/api-keys
http://127.0.0.1:28317/account-pool.html#/proxies
http://127.0.0.1:28317/account-pool.html#/models
http://127.0.0.1:28317/account-pool.html#/settings
```

登录说明：

- 使用 `config.yaml` 里的 `remote-management.secret-key` 对应的管理密钥登录。
- 资源池控制台有独立登录态。
- 不和旧的 `management.html` 共享登录状态。

### 4.1 页面布局

控制台包含六个主入口：

- `总览`：查看所有账号池汇总、综合健康度、模型相对余量、请求、Tokens、估算成本、成功率和 Key 数量。
- `账号池`：创建池并进入“概览 / 账号 / API Keys / 调度事件 / 策略”详情页。
- `API Keys`：生成和管理绑定账号池的 `sk-cap-...` 凭据。
- `代理 IP`：通过统一表格完成搜索、状态筛选、单条和批量操作。
- `模型与价格`：维护模型映射和版本化标准价格。
- `系统设置`：维护全局调度、默认 1h 缓存策略、纯净模式、Profile、日志和高级参数。

账号页支持：

- 搜索邮箱、Auth ID 和绑定代理。
- 筛选可调度、检查中、冷却中、有错误和暂停调度账号。
- 在卡片视图和表格视图之间切换。
- 点击账号卡片或表格行，从右侧抽屉查看完整详情。
- 在卡片溢出菜单、详情抽屉或批量工具栏中执行管理操作。
- 卡片、表格和详情中的可用性统一统计最近 1 小时：底层按分钟记录，界面每格合并 2 分钟，共 30 格。
- 可用性灰色表示没有请求；有请求时根据成功率显示绿色、黄色或红色，悬停可查看该时间段的请求和成功数量。
- 详情抽屉中的身份、Token 过期时间和更新时间默认折叠在“身份与时间信息”中，需要时再展开。
- “参与调度”是人工开关；暂停后账号仍会刷新 Token、额度和代理状态。
- 新账号注册后先显示“检查中”，首次额度探测通过后自动变为“可调度”。“需要处理”必须使用“重新检查并恢复”或成功刷新 Token。
- 活跃会话默认空闲 5 分钟后释放会话容量；亲和绑定继续保留 1 小时，所以会话返回时仍优先原账号。
- 额度详情会标记数据来自 OAuth usage 还是推理响应 Header；超过 15 分钟的快照只作为中性信息，不再被当作充足额度参与新会话排序。
- 额度窗口分为共享 5h/7d 和模型级 Sonnet、Opus、Fable 周窗口。Fable 主动字段 `seven_day_overage_included` 与响应 Header `7d_oi` 在页面统一显示为 `7d F`。
- 未耗尽额度只参与新会话排序，不提前停号。新会话在相同压力档内优先当前请求模型 headroom 更高的账号；100%、remaining 为 0、上游 `rejected/exhausted` 或真实 429 后才完全摘除对应范围。
- Fable、Sonnet、Opus 的模型周窗口耗尽只影响对应模型；共享 5h/7d 耗尽才会影响整个账号。额度 reset 到期或新快照恢复后会自动重新参与调度。

代理页支持：

- 搜索代理名称、出口 IP 和绑定账号。
- 筛选健康、异常、可用、已绑定、登录预留和已禁用代理。
- 选择代理后显示批量操作栏。
- 测试结果直接显示在表格“最近结果”列。
- 通过行尾溢出菜单编辑、启停、解绑或删除代理。

移动端不会产生页面级横向滚动；账号表格和代理表格在各自容器内横向滚动。

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
/account-pool.html#/pools/default/accounts
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
- 在无在途请求时移动到其他账号池。
- 批量操作账号。

账号测试只用于验证所选模型、OAuth 和绑定代理的连通性，使用最小 Claude Code 兼容提示词，不携带生产稳定提示词中的安全规则。该测试不会改变实际推理请求的 system prompt；纯净计费校准仍按完整生产 profile 计算。

账号卡片展示：

- 账号与是否可调度。
- 最近 1 小时可用性。
- 共享 5 小时/7 天额度，以及 Sonnet、Opus、Fable 模型窗口摘要。
- 并发、RPM、最近 30 天请求数、原始 Tokens 和估算成本。
- 绑定出口 IP 或代理名称，以及测试和更多操作。

额度百分比只在 OAuth usage 或响应 Header 明确返回 utilization 时展示。模型窗口未独立返回但存在共享 7 天额度时显示“共享”；Header 只有 status/reset 时显示“已观察”；完全没有数据时显示“未知”。`rejected/exhausted` 或 remaining 为 0 仍会显示为已耗尽并参与调度摘除。

每个账号响应中的 `quota_window_states` 固定包含 5h、7d、Sonnet、Opus 和 Fable。详情抽屉会分别展示：

- `精确百分比`：OAuth usage 或响应 Header 明确返回 utilization。
- `共享 7 天`：模型没有独立窗口，展示共享 7 天证据，但不伪造模型独立额度。
- `仅观察`：响应 Header 只有状态、remaining 或 reset，没有可计算百分比。
- `数据过期`：窗口采集超过 15 分钟或 reset 已到期；保留最后值用于诊断，但不当作当前余额。
- `未知`：尚无该窗口及可用的共享证据。

每个窗口独立显示来源、采集时间和重置时间。额度采集仍只使用 `/api/oauth/usage` 和真实推理响应 Header，不发送额外推理心跳。

账号表格使用更紧凑的单行摘要；点击卡片或表格行后，右侧详情抽屉会展示 Sonnet/Opus/Fable 额度、Token 构成、活跃会话、价格覆盖率、模型级健康、代理详情和最近错误。

配置页中路由参数名称后的问号为帮助入口。悬停、键盘聚焦或点击后会显示该参数的用途、单位以及调大或调小的主要影响；点击空白处或按 `Esc` 可关闭固定提示。

### 6.1 多账号池

- 内置池 ID 和名称均为 `default`，不可删除、归档或改名。
- 旧单池 SQLite 升级时会先补齐账号、routing event 和 usage ledger 的 `pool_id/api_key_id` 列，再创建多池索引并回填 `default`；迁移可重复启动，不会覆盖 OAuth、代理、额度或历史用量。
- 一个账号只能属于一个账号池；重复导入已属于其他池的账号会返回 `account_in_other_pool`，不会静默迁移。
- OAuth、SessionKey 批量登录和认证导入均可指定 `pool_id`；未提供时兼容进入 `default`。
- 自定义池删除采用归档语义，必须先移走账号并撤销该池全部未撤销 Key；历史统计继续保留。
- 账号移动只允许在没有在途请求时执行。OAuth、代理、额度和健康状态保留，旧池亲和和活跃会话会清除，历史 usage 不迁移。
- 同一个 Session ID 在不同池中使用独立亲和 namespace，任何情况下都不会跨池选号或候补。

池详情深链格式：

```text
/account-pool.html#/pools/<pool-id>/overview
/account-pool.html#/pools/<pool-id>/accounts
/account-pool.html#/pools/<pool-id>/api-keys
/account-pool.html#/pools/<pool-id>/events
/account-pool.html#/pools/<pool-id>/strategy
```

调度事件右上角的 `事件范围` 按事件发生时间筛选；表格每页固定 20 条，并通过服务端分页查看更早记录，事件增加不会继续把单页无限向下撑开。

### 6.2 池级策略与健康度

池级策略采用三级优先级：

```text
全局默认策略 < 账号池覆盖 < 单账号容量覆盖
```

- 系统设置中的“全局默认策略”是所有池的默认值。
- 池详情“策略”Tab 只保存显式覆盖字段；未覆盖字段会继续跟随全局变化。
- 字段后的来源标记区分“池覆盖”和“继承全局”，并同时显示当前全局值。
- 单项“恢复继承”会在保存后删除该字段覆盖；“全部继承”清除该池全部策略覆盖。
- 池可覆盖纯净计费、RPM、并发、活跃会话、亲和额外并发、等待/换号/重试/冷却和 Session/cache 亲和参数。
- Profile、TLS、请求指纹、模型价格、代理健康、额度维护、Trace、日志和客户端缓存 TTL 权限保持全局。
- 池策略更新只影响后续请求，不中断在途请求，也不清除健康亲和。

总览、池列表和池详情会显示综合健康度。固定组成如下：

- 账号就绪度 40%。
- 最近 1 小时请求可靠性 30%；少于 10 条时按样本量降低权重。
- Sonnet、Opus、Fable 最弱可信余量 20%；按额度覆盖率降低权重。
- 当前并发、RPM、活跃会话中的最高压力对应的负载余量 10%。

缺少流量或额度时不按 0 扣分，而是降低 `confidence` 并对已知组件重新归一化。池状态分为健康、关注、异常、不可用、已暂停和空池；健康问题会列出认证、代理、模型耗尽、高负载、低成功率、额度过期和单点风险。健康分数只用于观测，不参与选号或自动停池。

三模型容量使用账号调度相同的相对 headroom 口径：

```text
headroom = 1 - max(5h 使用率, 7d 使用率, 当前模型窗口使用率)
```

新鲜的精确/共享百分比和明确耗尽参与平均；仅 Header、过期和未知数据不虚构余量。`账号当量` 是已测可调度账号 headroom 之和，例如 20% + 70% = `0.9`。这些是内部相对指标，不是 Anthropic 官方余额或 SLA，也不会产生额外 Anthropic 请求。

### 6.3 SessionKey 批量登录

入口：

```text
/account-pool.html#/pools/<pool-id>/accounts
新增账号 -> SessionKey 批量
```

使用步骤：

1. 在代理 IP 池中准备健康且未绑定的代理，每个 SessionKey 需要一个独立代理。
2. 在输入框中一行填写一个自有 SessionKey，单批最多 100 条。
3. 设置登录并发，范围 1-5，默认 2。
4. 点击“开始批量登录”，输入框会在后端接受任务后立即清空。
5. 在弹窗中查看新增、更新、失败、无代理和逐条结果。
6. 关闭弹窗不会取消任务；重新打开或刷新页面后会恢复当前任务状态。

代理规则：

- 只选择 `enabled + healthy + 未绑定 + 未预留` 的代理。
- 按输入顺序预留，每个 SessionKey 固定一个代理。
- 代理不足时，已有代理的条目继续执行，其余显示 `no_proxy`。
- 失败不会自动更换代理。
- 登录成功后账号绑定本次代理；已有账号更新 token 并换绑代理，旧代理自动释放。
- 代理页面中的“登录预留”表示该代理正在用于 OAuth 凭据获取，暂时不能手动绑定或删除。

任务规则：

- 同一时间只允许一个批量任务。
- 点击“停止任务”会取消尚未开始的条目；已经运行的条目会完成当前授权流程。
- 完成结果在内存中保留 30 分钟。
- 服务重启会中断内存任务；未消费的代理预留会在 TTL 到期后恢复。

结果状态：

- `success`：新增账号。
- `updated`：更新已有账号 token 并换绑代理。
- `duplicate_input`：同一批输入重复。
- `duplicate_account`：不同 SessionKey 最终对应同一账号。
- `no_proxy`：没有可分配的健康空闲代理。
- `invalid_session`：网页会话失效或无权授权。
- `authorize_failed` / `token_exchange_failed`：网页授权或 token 换取失败。
- `missing_refresh_token`：上游未返回标准 OAuth refresh token，不注册账号。

安全说明：

- 只提交原始 `sk-ant-sid...` 值，不提交账号密码、2FA、邮箱验证码或完整 Cookie Header。
- SessionKey 只在任务运行期保存在服务内存中，不写数据库、auth 文件、日志、SSE 或浏览器本地存储。
- 管理接口应通过 HTTPS 暴露到生产环境。
- 当前只换取标准 OAuth，不支持 Setup Token。
- 该流程依赖 Claude 网页内部接口，上游改版后可能需要重新适配。

此功能不需要新增 `config.yaml` 或 `resource-pools.yaml` 配置。SQLite 会在启动时自动创建代理预留表。

### 6.3 OAuth 凭据存储与旧文件迁移

Claude Code Account Pool 的 OAuth 凭据以 `resource-pools.db` 为主存储。账号池 Auth 会加载到内部 Auth Manager，但不会显示在管理中心的“认证文件”页面，也不会参与普通 `/v1/*` Claude 路由。

“认证文件”数量不包含 SQLite 账号池凭据，这是正常现象。账号是否成功加载应以资源池控制台账号卡片中的运行状态为准。

升级后首次启动会检查 `auth-dir` 中的旧 Claude OAuth JSON：

- 只迁移同时包含 access token 和 refresh token 的标准 Claude OAuth。
- 先写入 SQLite 并回读验证，再删除旧文件。
- 迁移失败时保留原文件，日志只记录 Auth ID 和错误，不记录 token。
- API Key、Setup Token、插件虚拟凭据和 Claude API Pool 凭据不会自动迁移。

迁移完成后，账号只在以下页面管理：

```text
/account-pool.html#/pools/default/accounts
```

不要在迁移完成前手动删除 `auth-dir` 中的旧 Claude JSON。

## 7. 专属公开 API

Claude Code Account Pool 使用独立 API 前缀，不影响原 `/v1/*`。

推荐在控制台 `API Keys` 页面生成绑定目标池的 Key。格式为 `sk-cap-...`，创建后可在该页面随时查看和复制，仅支持：

```text
Authorization: Bearer sk-cap-...
x-api-key: sk-cap-...
```

不支持 URL 参数，也不能用于普通 `/v1/*` 或 Management API。`config.yaml` 中的旧 Key 继续兼容，但固定映射到 `default`。

生成的池 Key 永久有效，不设置到期时间。只有管理员停用、轮换或撤销后才会失效；升级时历史 `expires_at` 会自动清空。API Key 页面和账号池列表固定展示全部历史用量，不提供时间范围切换；总览和池详情仍可按时间范围查看运营数据。

API Key 列表只返回前缀和 `secret_available`，完整值仅在管理员点击查看时通过专用 Management API 返回。升级前创建的历史 Key 只有 SHA-256，不能恢复原文；它们继续有效，但需要轮换一次才能在页面查看。撤销 Key 时会清除保存的完整值。

Claude-native 客户端：

```text
Base URL: http://127.0.0.1:28317/claude-acc-pool
Path: /v1/messages
API Key: 目标账号池绑定的 sk-cap-... Key
```

OpenAI-compatible 客户端：

```text
Base URL: http://127.0.0.1:28317/claude-acc-pool/v1
API Key: 目标账号池绑定的 sk-cap-... Key
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

## 8. 模型与价格

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
- 全局价格单位为 USD / 百万 Tokens，分别配置输入、输出、缓存写入 5m、缓存写入 1h 和缓存读取。
- 修改价格会创建不可变 Revision；请求开始时固定价格版本，后续改价不会重算历史请求。
- 成本使用 Anthropic 原始上游 usage，并包含重试和换号产生的真实 attempts；纯净模式不改变成本。
- 缺少缓存时长明细时按 5m 写入价估算并标记为“估算”；未知模型保留 Tokens，并计入“未计价”和价格覆盖率。
- 内置价格覆盖 Sonnet、Opus 和 Haiku。Fable 未配置前保持未计价，可在页面新增匹配价格。

## 9. 调度和容量

账号池调度只选择 `claude_oauth_pool=true` 且 `claude_code_pool_id` 与请求 Key 所属池一致的 OAuth 账号。

不会混用：

- 原 `/v1/*`
- Claude API Pool
- 普通 Claude auth
- 其他 Claude Code 账号池

账号内部保护包含：

- `base_rpm`
- `concurrency_limit`
- `max_sessions`
- `sticky_concurrency_reserve`

账号卡片和主表格只展示直接请求压力：

```text
并发 in_flight / concurrency_limit
RPM rpm_used / rpm_limit
```

账号详情和高级设置继续展示活跃会话软上限、亲和绑定、亲和会话额外并发和等待者。这些字段保留调度作用，但不占用主列表空间。

默认保守配置：

```text
每账号 RPM: 6
基础并发: 1
亲和会话额外并发: 1
每账号活跃会话: 30
最大换号次数: 2
Sticky 等待: 2000ms
普通等待: 500ms
单账号等待者: 5
全局等待者: 200
会话亲和 TTL: 3600000ms
```

会话亲和规则：

- 只识别显式 session ID，不使用消息正文 hash。
- 同一 session 跨模型保持一个主账号。
- 长上下文缓存前缀会维护稳定备用 lane。
- 主账号模型冷却时使用备用账号，但不会删除主绑定。
- 账号级认证失败时清除该账号的全部绑定。
- 备用账号完成成功请求后会提升为新主账号。

缓存统计规则：

- 账号池不使用虚拟缓存账本，不修改 Anthropic 返回的缓存创建/读取数据。
- 运行指标中的缓存率来自真实 `cache_read_input_tokens`、`cache_creation_input_tokens` 和输入 token。
- 普通 API 客户端要获得稳定亲和，应传递 `X-Session-ID`、`Session-Id` 或 `conversation_id`。
- 没有显式 session 的普通请求使用请求级随机 Session，不根据消息正文推断会话。

本地容量满时返回：

```text
HTTP 429
error.type = rate_limit_error
Retry-After: <seconds>
```

旧版本的 `sticky_buffer` 同时影响并发和 RPM，已经废弃。新版本的 `sticky_concurrency_reserve` 只允许已有 Sticky 会话临时使用额外并发，RPM 始终使用基础限制。

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

错误归属：

- 400/422：请求语义错误，直接返回，不影响账号健康。
- 401：同账号 refresh 一次；失败后账号冷却 30 分钟。
- 403：按 Cloudflare、人工恢复和未知 Forbidden 分类。
- 429/529：模型级冷却，不把整个账号显示为不可用。
- transport/proxy：连续失败达到阈值后临时摘除账号和代理。

`/claude-acc-pool/v1/messages` 的 JSON 错误使用 Anthropic envelope：顶层 `type=error`，包含 `error.type`、`error.message` 和 `request_id`；`request_id` 与响应的 `request-id` 或 `x-request-id` Header 保持一致。HTTP 400 中上游泛化的 `upstream_error` 会规范为 `invalid_request_error`。

注意：

- 账号池调度状态保存在当前服务进程内，服务仍按单实例运行。
- routing v2 首次迁移会把旧容量设置重置为保守默认值，但不会删除账号、OAuth、代理、usage 或 routing events。
- 配置页默认只显示承载档位、RPM、并发、换号和亲和开关；活跃会话软上限、亲和会话额外并发、等待队列、冷却、重试和 lanes 位于“高级路由设置”。

## 10. Prompt Cache TTL

账号池请求默认按 Claude 订阅主会话使用 1 小时缓存：

- 对现有 `cache_control: {"type":"ephemeral"}` 断点补齐 `ttl: "1h"`。
- 默认忽略客户端传入的 5m/1h TTL，并统一使用 1h。
- 在系统设置开启“允许请求参数控制缓存 TTL”后，显式 `ttl: "5m"` 或 `ttl: "1h"` 会被保留；没有显式 TTL 的断点仍使用 1h。
- 不凭空给 tools 或 messages 增加缓存断点，只处理请求中已有断点和账号池 profile 自带断点。
- 普通 API mimic 会把客户端 system 语义迁移到首条 user reminder；原 system 文本块显式声明的 `cache_control` 会随对应 reminder 块一起迁移，不会因伪装改写丢失。
- 同时适用于非流式、流式、真实 Claude Code passthrough、普通 API mimic、`count_tokens` 和管理账号测试。
- 普通 `/v1/*`、Claude API Pool 和其他 provider 不受影响。

混用 5m 和 1h 时仍需满足 Anthropic 的顺序约束：按 tools、system、messages 的计算顺序，1h 断点不能出现在 5m 断点之后。账号池会在最终请求构造完成后自动整理顺序并限制为最多四个断点。

## 11. 纯净计费模式

配置位置：

```text
Claude Code 账号池 -> 配置 -> 基础配置 -> 纯净计费模式
```

作用：

- 只改写返回给下游的 usage，不修改上游请求。
- 扣除账号池 api-mimic 自动注入的 Claude Code 输入、缓存创建和缓存读取开销。
- 同步处理非流式、流式、`iterations[]`、缓存 5m/1h 明细和 `count_tokens`。
- 真实 Claude Code passthrough 不进行扣减。
- 客户端未声明缓存时，mimic 产生的缓存创建和读取对下游归零。
- 网关自有缓存归零不依赖 profile 开销估算值；只使用“总注入开销减去已观测缓存 Token”的剩余部分扣减未缓存输入，避免估算偏低时把缓存差额转入 `input_tokens`。
- 如果已观测网关缓存 Token 超过 profile 总开销校准，说明单一校准值无法继续识别非缓存 profile 残余；此时下游 `input_tokens` 回退为客户端原始请求的可见输入估算。
- `count_tokens` 响应没有缓存桶可供拆分；客户端未声明缓存时直接使用原始请求的可见输入估算，避免 profile 总开销估算误差形成固定偏移。
- 客户端自带缓存控制时，只扣除校准或估算的账号池注入开销，保留剩余的客户端缓存 usage。
- 不影响 Anthropic 真实请求。
- 不影响 Anthropic 真实消耗。
- 不影响额度和限速判断。

校准机制：

- 按 `model + profile_fingerprint` 保存系统提示词 overhead。
- fingerprint 变化后旧校准不会继续命中。
- 未校准时根据当前 profile 实际注入的 billing、Agent SDK identity 和稳定 prompt 动态估算。
- 客户端原始 system-reminder 属于可见输入，不计入隐藏开销。

注意：

- 真实 usage ledger、账号池日志和真实 Token 指标仍保留 Anthropic 原始 usage。
- `usage.clean_input_tokens` 仅作为旧配置兼容字段，实际开关统一由 `pure_mode` 控制。
- 这个能力只用于下游展示和内部计费口径。

## 12. 账号池日志

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

## 13. 运行指标和事件流

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

## 14. Trace 工具链

Trace 工具用于对齐真实 Claude Code 请求形态。

当前生产基线为 Claude Code `2.1.207` / profile revision `2.1.207-r3`。后续版本必须先录制真实 trace 并完成 diff，不能只根据 Phistory 自动升级生产 profile。

### 录制真实 Claude Code trace

```bash
go run ./cmd/claude_trace_recorder \
  --listen 127.0.0.1:39001 \
  --upstream https://api.anthropic.com \
  --out traces/real
```

只抓请求形态、不访问真实 Anthropic 时使用：

```bash
go run ./cmd/claude_trace_recorder \
  --listen 127.0.0.1:39001 \
  --mode record-only \
  --out traces/real
```

record-only 会返回合法的最小 Anthropic JSON/SSE/count_tokens 响应，避免 Claude Code 自动重试。recorder 同时从原始 TCP 请求中只提取 Header 名称的顺序与大小写，不保存 Header 值。

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
- diff 按 method、path、model、stream 和 request kind 配对，避免标题/结构化 helper 与主交互请求错配。
- request kind 包含 `interactive`、`structured-helper`、`count_tokens`、`tool-followup` 和 `other`。

## 15. Profile 基线

账号池页面支持拉取 Phistory 的 Claude Code profile 快照。

用途：

- 查看真实 Claude Code 版本。
- 对比 headers、beta、system prompt。
- 生成差异报告。
- 分别查看稳定提示词与完整动态 Prompt 的 hash/长度。
- 查看 trace request-kind 汇总。

限制：

- 只做参考。
- 不自动应用到生产请求。
- 不把 Phistory 的完整超长 prompt 自动注入账号池业务。
- 不把 Phistory 的工具定义、Memory、Skills、环境路径、日期或模型清单应用到生产请求。

最新版本发现直接解析 `phistory.cc` 首页 manifest；指定版本的 `meta.json`、`trace.jsonl`、`prompt.md`、`static-prompts.md/json` 从 raw 文件读取。

## 16. 请求形态规则

真实 Claude Code passthrough：

- UA、billing 版本、metadata、identity/system 和必要 headers 必须相互一致。
- 2.1.207 无 CCH billing 与旧版带 CCH billing 都能识别。
- body 中的 system、tools、tool history、thinking、context management、output config 和 cache control 保持客户端语义。
- 账号池会替换 Device ID 和 Account UUID，但保留客户端 Session ID。

普通 API mimic：

- 使用 `2.1.207-r3` UA、billing、Agent SDK identity、工具无关稳定核心、metadata 和请求能力对应 beta。
- 使用 OAuth access token 时，最终 beta 为客户端/请求能力 beta 加凭证必需的 `oauth-2025-04-20`；真实 Claude Code passthrough 保留客户端顺序并只补充凭证必需项。
- Header Session 与 `metadata.user_id.session_id` 来自同一个最终请求 Session；trace 只记录二者是否一致，不保存 Session 原值。
- Stainless 平台固定为已验证的 `MacOS/arm64`，并与 Node HTTP/1.1 uTLS、Header 顺序、JA3/JA4 一起作为同一 profile 管理。
- 不伪造 Claude Code 自带工具，不自动增加 thinking 或 output config。
- 客户端自带 tools/tool_use/tool_result 时保持原结构，也不会自动给最后一个 tool 增加 cache control。
- 客户端 system 文本放入首条 user message 的 system-reminder，原始语义保留。
- 显式 session/conversation 按 `pool_id + api_key_id + conversation` 生成稳定 Session；不使用请求级 `X-Client-Request-Id`。
- 无显式 session 时按账号池 Key 和选中账号复用 1 小时临时 Session；该值不参与调度亲和，池、Key 或账号变化时自动隔离。

TLS 和有序 HTTP/1.1 serializer 只对账号池访问 Anthropic 官方域名生效，自定义 base URL 和普通 `/v1/*` 不使用该 profile。外部普通 API 请求不会注入完整 Claude Code 动态 prompt 或内置工具；需要完整工具运行时的请求应由真实 Claude Code passthrough。

账号池路由 scope 由 `/claude-acc-pool/v1` middleware 写入请求上下文，并在 handler 创建可取消执行上下文时显式继承。额度 worker 发生 OAuth token 轮换后会同步更新 SQLite 与 Auth Manager runtime auth，因此业务请求和 `/api/oauth/usage` 不会使用不同代的 access token。

## 17. 运行一致性诊断

页面入口：

```text
/account-pool.html#/settings
```

Management API：

```text
GET /v0/management/claude-code-account-pool/diagnostics
```

诊断只读取本地配置和 SQLite，不发送 Anthropic 请求，也不持续轮询。页面可见、手动刷新或账号/代理/池/配置 SSE 变化后才重新读取。

主要信息：

- 构建版本、Commit、构建时间和 Go 平台；使用 `scripts/build-all.sh` 可注入版本信息，普通 `go build` 可能显示“Commit 未注入”。
- 当前数据库路径和实例短指纹。复制同一数据库指纹保持不变，重建数据库会变化，可用于识别 Docker 挂载错误。
- Profile revision/fingerprint、Header 数量/顺序摘要、TLS profile 和 ALPN。
- 额度维护开关、配置间隔、15 秒扫描 tick、并发和全局代理模式。
- 每个账号的短账号/设备指纹、池 ID、代理资源 ID、最近观测出口、Token/额度时间、采集传输类别和标准化问题码。

诊断不会返回邮箱、Auth ID、access/refresh token、Session、API Key、完整代理 URL/凭据或原始错误正文。接口响应带 `Cache-Control: no-store`。

额度 worker 的 15 秒 tick 只扫描到期账号，实际成功采集间隔由 `account-quota.interval` 控制。手动刷新会绕过到期判断，并与后台同账号请求 singleflight 去重。配置了无效代理时，登录、Token 刷新、额度和推理都会返回代理错误，不会静默直连。

## 18. 常见排障

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

## 19. 模型额度显示与账号测试

- 额度窗口以 Anthropic 实际返回为准。共享窗口为 `5h` 和 `7d`，模型独立窗口为可选数据。
- `7d S / 7d O / 7d F` 中，“共享”表示上游没有返回该模型的独立周窗口，调度使用共享 `7d` 约束。
- Fable 可能通过 OAuth usage 的 `limits[].weekly_scoped` 或响应 Header 的 `7d_oi` 返回。
- `weekly_scoped` 只有活跃或带 reset 时才视为独立额度；非活跃条目按共享 `7d` 展示。
- `overage=rejected` 只表示额外用量不可用，不代表 Sonnet、Opus 或 Fable 额度耗尽。
- 账号测试收到 HTTP 200 但没有文本时，会显示响应类型、停止原因或 SSE 事件摘要；`stop_reason=refusal` 表示模型拒绝生成，不是网络连接失败。
- 账号测试是真实上游请求，会写入 `1h 可用性`和真实 token 用量；HTTP 2xx 算可用，非 2xx、代理或传输失败算不可用。
