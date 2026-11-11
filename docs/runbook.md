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

# 3. 迁移（M1 起接入 goose；当前可手工执行）
psql "$AGENTHUB_DB_URL" -f migrations/0001_init.sql

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
| `AGENTHUB_DB_URL` | 空 | PostgreSQL 连接串；空则无持久化 |
| `AGENTHUB_DB_MAX_CONNS` | `10` | 连接池上限 |
| `AGENTHUB_DATA_DIR` | `./data` | 附件与产物目录 |
| `FEISHU_APP_ID` | 必填（feishu 模式） | 应用 ID |
| `FEISHU_APP_SECRET` | 必填（feishu 模式） | 应用密钥，只从环境变量读取 |
| `FEISHU_BOT_OPEN_ID` | 空 | 机器人 open_id；为空时把任意 @ 视为 @机器人 |
| `FEISHU_VERIFICATION_TOKEN` | 空 | 事件订阅校验（webhook 模式需要） |
| `FEISHU_ENCRYPT_KEY` | 空 | 事件加密密钥（如开启） |
| `FEISHU_ALLOWED_USERS` | 空 | 允许的 open_id，逗号分隔；**空 = 拒绝所有人** |
| `FEISHU_ALLOWED_CHATS` | 空 | 允许的 chat_id；空 = 不限制群 |
| `FEISHU_REQUIRE_MENTION` | `true` | 群聊是否必须 @机器人 |
| `AGENTHUB_AGENT_PROVIDER` | `openai` | 模型提供方 |
| `AGENTHUB_AGENT_MODEL` | `gpt-4.1-mini` | 默认模型 |
| `AGENTHUB_AGENT_MAX_RUN_SECONDS` | `1800` | 单次运行上限 |
| `AGENTHUB_LOG_LEVEL` | `info` | debug/info/warn/error |
| `AGENTHUB_LOG_FORMAT` | `json` | json/text |

配置校验失败以退出码 **2** 退出，日志只记录字段名与原因，不记录字段值。

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
