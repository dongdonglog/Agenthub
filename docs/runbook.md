# 运维手册

## 1. 前置条件

| 项 | 要求 |
|---|---|
| 运行环境 | Linux x86_64/arm64，2C4G 起步 |
| 数据库 | PostgreSQL 14+（推荐 16） |
| 存储 | 本地持久卷（默认）或 S3 兼容对象存储 |
| 网络 | 可访问飞书开放平台（长连接与 OpenAPI）、模型服务 |
| 时间 | 开启 NTP（令牌与卡片对时间敏感） |

## 2. 部署

```bash
# 1. 准备数据库
createdb agenthub

# 2. 配置
cp .env.example .env    # 填 FEISHU_APP_ID/SECRET、AGENTHUB_DB_URL

# 3. 迁移：启动时自动执行 migrations/ 下未应用的 SQL（schema_migrations 记录版本）

# 4. 运行
set -a && . ./.env && set +a
./bin/agentd
```

Docker：

```bash
docker build -t agenthub:dev .
docker run --rm -p 127.0.0.1:8787:8787 --env-file .env agenthub:dev
```

## 3. 飞书应用配置

1. 飞书开放平台 → 创建**企业自建应用**；
2. 添加「机器人」能力；
3. 事件订阅 → 选择**使用长连接接收事件**（默认，无需公网）；若必须用回调，改为「将事件发送至开发者服务器」，
   地址填 `https://<网关>/feishu/events`，并设置 `FEISHU_TRANSPORT=webhook`；
   订阅 `im.message.receive_v1`；
4. 权限：按 [feishu-capabilities.md](feishu-capabilities.md) §7 申请；
5. 卡片：配置回调地址或确认长连接可用（见 Spike S1）；
6. 发布应用，把机器人加入目标群；
7. 记录 App ID / App Secret，写入 `FEISHU_APP_ID` / `FEISHU_APP_SECRET`。

### 3.1 只给 App ID / App Secret 够吗

够用来**建立长连接**（启动日志出现 `feishu websocket ready`），但要真正收发消息还差四件事：

| 项 | 在哪配 | 不配的后果 |
|---|---|---|
| 机器人能力 | 应用功能 → 机器人 | 没有 bot 身份，收不到也发不出 |
| 事件订阅 | 事件与回调 → **使用长连接** 接收事件，订阅 `im.message.receive_v1` | 连接成功但收不到消息 |
| 权限 | 权限管理 → `im:message`、`im:message:send_as_bot` 等 | 收发被拒（如 99991672 无权限） |
| 发布应用 + 可见范围 | 版本管理与发布 | 仅创建者可用，或完全不可用 |

群聊还需要把机器人拉进群，并确认 `FEISHU_ALLOWED_USERS` / `FEISHU_ALLOWED_CHATS` 白名单。

### 3.2 取机器人 open_id

```bash
TOKEN=$(curl -s -X POST https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal \
  -H 'Content-Type: application/json' \
  -d '{"app_id":"<APP_ID>","app_secret":"<APP_SECRET>"}' | jq -r .tenant_access_token)

curl -s https://open.feishu.cn/open-apis/bot/v3/info -H "Authorization: Bearer $TOKEN" | jq .bot
```

把返回的 `bot.open_id` 写入 `FEISHU_BOT_OPEN_ID`。不填时服务把任意 @ 都当成 @机器人，
多机器人同群会误触发。

## 4. 配置参考

| 变量 | 默认 | 说明 |
|---|---|---|
| `AGENTHUB_ENV` | `development` | `development` / `production` |
| `AGENTHUB_CHANNEL` | `off` | `off` 只跑骨架；`feishu` 接飞书 |
| `AGENTHUB_HTTP_ADDR` | `127.0.0.1:8787` | 健康检查与指标 |
| `AGENTHUB_ADMIN_ADDR` | `127.0.0.1:8788` | 管理面（无内置登录） |
| `AGENTHUB_WEBHOOK_ADDR` | `127.0.0.1:8789` | 飞书事件回调监听（仅 webhook 模式） |
| `FEISHU_TRANSPORT` | `websocket` | `websocket` 长连接（无需公网入口）/ `webhook` 回调地址 |
| `FEISHU_DOMAIN` | `https://open.feishu.cn` | 开放平台地址，Lark 海外改为 `https://open.larksuite.com` |
| `AGENTHUB_DB_URL` | 空 | PostgreSQL 连接串；空则无持久化（会话/消息重启即丢） |
| `AGENTHUB_MIGRATIONS_DIR` | `migrations` | 迁移目录；启动时按文件名顺序应用 |
| `AGENTHUB_RETENTION_DAYS` | `90` | 历史数据保留天数；0 关闭清理 |
| `AGENTHUB_MAX_CONCURRENT_RUNS` | `4` | 跨会话并发运行上限 |
| `AGENTHUB_RATE_LIMIT_PER_MIN` | `0` | 每会话每分钟消息上限；0 关闭 |
| `AGENTHUB_QUEUE_TTL_SECONDS` | `21600` | 排队消息最长等待秒数；0 关闭 |
| `AGENTHUB_DELIVERY_LEASE_SECONDS` | `120` | 投递领取租约，应大于单次通道调用耗时 |
| `AGENTHUB_OUTBOUND_QPS` | `0` | 跨会话共享出站消息速率；0 不限 |
| `AGENTHUB_AIOPS_ENABLED` | `false` | 启用 Alertmanager 告警入口 |
| `AGENTHUB_AIOPS_TOKEN` | 空 | 告警 webhook Bearer token |
| `AGENTHUB_AIOPS_CHAT_ID` | 空 | 事故诊断投递的飞书群 ID |
| `AGENTHUB_AIOPS_RUNBOOK_DIR` | `<data>/runbooks` | 告警运行手册目录 |
| `AGENTHUB_SERVICE_CATALOG` | `<data>/services.yaml` | 服务目录（owner/team/runbook） |
| `AGENTHUB_SUPPORT_ENABLED` | `false` | 启用技术支持工单入口 |
| `AGENTHUB_SUPPORT_TOKEN` | 空 | 工单 webhook token |
| `AGENTHUB_SUPPORT_CHAT_ID` | 空 | 支持案件投递群 |
| `AGENTHUB_SUPPORT_EMPLOYEE_ID` | `tech-support` | 支持数字员工 ID |
| `AGENTHUB_KNOWLEDGE_DIR` | `<data>/knowledge` | 技术支持知识库目录 |
| `AGENTHUB_AIOPS_MAX_ALERTS` | `20` | 单次 webhook 最多处理告警数 |
| `AGENTHUB_PROMETHEUS_URL` | 空 | Prometheus 只读地址；配置后启用 ops MCP |
| `AGENTHUB_PROMETHEUS_TOKEN/USER/PASSWORD` | 空 | Prometheus 认证 |
| `AGENTHUB_LOKI_URL` | 空 | Loki 只读地址 |
| `AGENTHUB_LOKI_TOKEN/USER/PASSWORD` | 空 | Loki 认证 |
| `AGENTHUB_TEMPO_URL` | 空 | Tempo 只读地址 |
| `AGENTHUB_TEMPO_TOKEN/USER/PASSWORD` | 空 | Tempo 认证 |
| `AGENTHUB_K8S_ENABLED` | `false` | 启用 K8s 只读工具 |
| `AGENTHUB_KUBECTL_BIN` | `kubectl` | kubectl 路径 |
| `AGENTHUB_K8S_NAMESPACES` | 空 | 受控动作允许的 namespace |
| `AGENTHUB_OPS_MCP_BIN` | 同目录 | agenthub-ops-mcp 路径 |
| `AGENTHUB_PI_PACKAGE` | 同目录/当前目录 | pi-package 资源目录 |
| `DINGTALK_GROUP_OPEN` | `false` | 钉钉群内免用户白名单；仍受群白名单限制 |
| `FEISHU_ADMIN_OPEN_IDS` | 空 | 允许引用机器人消息干预他人会话的管理员 open_id |
| `PI_BIN` | `pi` | pi 可执行文件 |
| `PI_TOOLS` | `read,bash,edit,write,grep,find,ls,tool_search` | 暴露给 pi 的工具 |
| `PI_AGENT_DIR` | `<data>/pi/agent` | 部署配置目录 |
| `PI_SESSION_DIR` | `<data>/pi/sessions` | 会话文件目录 |
| `PI_WORKSPACE_DIR` | `<data>/pi/workspaces` | 会话工作区 |
| `PI_ISOLATION` | `namespace` | `namespace` / `process_group` |
| `PI_TIMEOUT_SECONDS` | `600` | 单次运行时限 |
| `DINGTALK_CLIENT_ID` | 空 | 钉钉应用 Client ID（channel=dingtalk 必填） |
| `DINGTALK_CLIENT_SECRET` | 空 | 钉钉应用 Client Secret |
| `DINGTALK_REQUIRE_MENTION` | `true` | 钉钉群聊是否必须 @机器人 |
| `DINGTALK_ALLOWED_USERS` | 空 | staffId 白名单，空表示不限 |
| `DINGTALK_ALLOWED_CHATS` | 空 | 会话白名单，空表示不限 |
| `DINGTALK_BOT_NAME` | 空 | 去掉文本开头的 @机器人 |
| `AGENTHUB_DB_MAX_CONNS` | `10` | 连接池上限 |
| `AGENTHUB_DATA_DIR` | `./data` | 附件、产物与工作区目录 |
| `AGENTHUB_ARTIFACT_MAX_BYTES` | `20971520` | 单个交付物大小上限 |
| `AGENTHUB_ARTIFACT_MAX_COUNT` | `10` | 单次运行交付物数量上限 |
| `AGENTHUB_ARTIFACT_MAX_TOTAL` | `52428800` | 单次运行交付物总量上限 |
| `FEISHU_APP_ID` | 必填（feishu 模式） | 应用 ID |
| `FEISHU_APP_SECRET` | 必填（feishu 模式） | 应用密钥，只从环境变量读取 |
| `FEISHU_BOT_OPEN_ID` | 空 | 机器人 open_id；为空时把任意 @ 视为 @机器人 |
| `FEISHU_VERIFICATION_TOKEN` | 空 | 事件订阅校验（webhook 模式需要） |
| `FEISHU_ENCRYPT_KEY` | 空 | 事件加密密钥（如开启） |
| `FEISHU_ALLOWED_USERS` | 空 | 允许的 open_id，逗号分隔；**空 = 拒绝所有人** |
| `FEISHU_ALLOWED_CHATS` | 空 | 允许的 chat_id；空 = 不限制群 |
| `FEISHU_REQUIRE_MENTION` | `true` | 群聊是否必须 @机器人 |
| `AGENTHUB_AGENT_PROVIDER` | `openai` | 模型提供方 |
| `AGENTHUB_AGENT_BASE_URL` | `https://api.openai.com/v1` | OpenAI 兼容接口地址 |
| `AGENTHUB_AGENT_API_KEY` | 空 | 为空时退回回显执行器 |
| `AGENTHUB_AGENT_MODEL` | `gpt-4.1-mini` | 默认模型 |
| `AGENTHUB_AGENT_SYSTEM_PROMPT` | 内置 | 追加技能之前的系统提示词 |
| `AGENTHUB_AGENT_SKILLS_DIR` | `<数据目录>/skills` | Markdown 技能目录 |
| `AGENTHUB_AGENT_MAX_RUN_SECONDS` | `1800` | 单次运行上限 |
| `AGENTHUB_LOG_LEVEL` | `info` | debug/info/warn/error |
| `AGENTHUB_LOG_FORMAT` | `json` | json/text |

配置校验失败以退出码 **2** 退出，日志只记录字段名与原因，不记录字段值。

## 4.1 管理台

默认监听 `127.0.0.1:8788`，页面入口 `/admin/`，API 前缀 `/admin/api/v1`。

- 技能与 MCP 配置分别保存到 `<数据目录>/skills/*.md` 与 `<数据目录>/mcp.yaml`；
- 管理面没有内置登录，远端访问必须经网关或 SSH 隧道（`ssh -L 8788:127.0.0.1:8788`）；
- 修改模型与飞书配置仍走 `.env`，重启生效。

### 4.2 产物交付

- 工作区 `submit/` 指向本次运行 `outbox/<run_id>/`；运行成功后导入 `data/artifacts`；
- 文本先投递，再投递图片与文档；多个文档打 ZIP（UTF-8 文件名）；
- 超限不调用上传接口，只投递说明；通道不支持文件时明确提示；
- 管理台运行详情可查看并下载产物；保留期与数据库清理策略一致。

### 4.3 投递与队列

- 发送前原子领取投递，写入 `lease_owner` / `lease_token` / `lease_expires_at`，阶段为 `claimed`；
- 调用通道前推进到 `calling`；只有通道明确未接受（如限流）才退避重试；
- 网络/超时等结果不明进入 `ambiguous`，不自动重发，管理台运行详情可见；
- 恢复扫描器回收过期租约：`claimed` 安全重试，`calling` 标歧义；租约由 `AGENTHUB_DELIVERY_LEASE_SECONDS` 控制；
- 管理台运行详情可对 `ambiguous` / `failed` 投递人工重试；人工确认后才会重发；
- 队列中的消息超过 `AGENTHUB_QUEUE_TTL_SECONDS` 后不再执行，并投递“请重新发送”的说明。

### 4.4 附件

- 保存目录：`<AGENTHUB_DATA_DIR>/attachments/<message_id>/`，文件名做了字符清洗；
- 飞书与钉钉入站文件/图片都会下载；钉钉通过 `downloadCode` 换签名地址后流式落盘；
- 下载在事件确认之后异步进行，完成才入队；单个附件失败只影响该附件；
- 模型侧：小文本内联，其余通过 `read_file` 读取，路径被限制在附件目录内。

### 4.5 持久化

- 消息、运行、投递写入 `message_state` / `run_state` / `delivery_state`；
- 启动时 `Restore` 恢复最近 1000 条消息与运行，`processing` 状态的消息重新排队；
- 集成测试：`AGENTHUB_TEST_DATABASE_URL=... go test ./internal/core -run TestJournalPersistenceAndRestore`。

## 5. 健康检查与指标

**`GET /healthz`**

```json
{
  "status": "ok",
  "version": "0.1.0-dev",
  "checks": { "config": "ok", "db": "ok" }
}
```

- `db=skipped` 表示未配置数据库（骨架模式）；
- `feishu` 在长连接模式下为 `connecting` / `connected` / `reconnecting`，三者都返回 200，断线不触发重启；
- 任一 `error` 返回 503。

**`GET /metrics`**（Prometheus 文本）

| 指标 | 类型 | 说明 |
|---|---|---|
| `agenthub_uptime_seconds` | gauge | 运行时长 |
| `agenthub_http_requests_total` | counter | 按 method/path |
| `agenthub_messages_accepted_total` | counter | 受理消息数 |
| `agenthub_runs_total` | counter | 按 state（completed/failed/interrupted） |
| `agenthub_deliveries_total` | counter | 按 result（sent/retrying/failed） |

日志字段：`trace_id`（请求级）、`run_id`（运行级）、`server`（MCP 服务名）、`tool`（工具名）。
| `agenthub_runs_total` | counter | 按 state |
| `agenthub_delivery_total` | counter | 按 result |
| `agenthub_message_accepted_total` | counter | 受理消息数 |

## 6. 故障处置

| 现象 | 排查 | 处置 |
|---|---|---|
| 启动退出码 2 | 看日志字段名 | 补配置 |
| 收不到消息 | 长连接是否建立、机器人是否发布、白名单 | 看日志 `feishu websocket ready`；确认应用已发布、机器人已进群 |
| 长连接反复重连 | 应用凭据、网络、出口防火墙 | 日志出现 `app_id is invalid` 说明凭据错；否则检查 443 出网 |
| 卡片不出现 | 卡片权限/模板、更新频率 | 降级文本，查 `card` 日志 |
| 重复回复 | 检查幂等键与投递状态 | 查 `delivery` 表 |
| 消息处理超时 | 模型/工具耗时 | 调小批次或超时，看 trace |
| 内存上涨 | 附件是否流式、上下文是否过大 | 检查配额与压缩策略 |

## 6.1 停机

收到 SIGTERM 后：停止接收新事件 → 最多等 20s 让在途运行收敛 → 取消工作上下文 → 关闭 HTTP 与数据库。超时仍在运行的会按中断处理并投递中断说明。

## 7. 升级与回滚

1. 备份数据库与数据目录；
2. 停服务（SIGTERM，最多等 30s 让在途批次结束）；
3. 换二进制/镜像，执行迁移；
4. 看 `/healthz` 与启动日志；
5. 回滚以备份恢复为准（迁移只前进，不写回滚脚本）。

## 8. 备份

| 对象 | 频率 | 方式 |
|---|---|---|
| PostgreSQL | 每日 | `pg_dump` 或物理备份 |
| 附件目录 | 每日 | 增量同步到对象存储 |
| 配置 | 变更时 | 版本库 |

## 9. 安全注意

- `FEISHU_APP_SECRET` 只放环境变量或密钥管理，不写入镜像；
- 管理面默认只监听回环，对外必须前置网关或 OIDC；
- 工具沙箱只读挂载、无宿主 socket、出网白名单；
- 日志不记录完整消息正文与下载地址。
