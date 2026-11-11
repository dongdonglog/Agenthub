# AgentHub

飞书优先的团队 AI 助手：把"描述问题 → 补材料 → 要结论 → 交付文件"做成可追踪、可复用、可治理的对话服务。

Go 实现，单二进制，PostgreSQL 为唯一事实源。

## 快速开始

```bash
# 只起骨架（不接通道、不需要数据库）
make run

curl -s localhost:8787/healthz
# {"status":"ok","version":"0.1.0-dev","checks":{"config":"ok","db":"skipped"}}
```

接飞书（默认长连接，不需要公网入口）：

```bash
cp .env.example .env      # 填 FEISHU_APP_ID / FEISHU_APP_SECRET / FEISHU_BOT_OPEN_ID
set -a && . ./.env && set +a
go run ./cmd/agentd
```

事件接入方式：

| 方式 | 配置 | 说明 |
|---|---|---|
| WebSocket 长连接（默认） | `FEISHU_TRANSPORT=websocket` | 无需公网入口与回调地址；SDK 负责握手、心跳与自动重连；断线按指数退避重试 |
| 事件回调 | `FEISHU_TRANSPORT=webhook` + `AGENTHUB_WEBHOOK_ADDR` | 需要飞书能访问到该地址，适合已有网关的部署 |

## 本地模拟（不需要真实飞书应用）

`go test ./...` 覆盖了完整链路，不访问外网：

| 用例 | 验证点 |
|---|---|
| `TestMessageEndToEnd` | 事件 → 解析 → 批次 → 执行 → 回复（fake 飞书 API） |
| `TestDuplicateEventDeliversOnce` | 重复推送只投递一次 |
| `TestGroupWithoutMentionIsIgnored` | 群聊未 @机器人 不处理 |
| `TestRapidMessagesMergeIntoOneBatch` | 3 条连发合并为 1 次运行 |
| `TestStopInterruptsRun` | 停止后运行中断且仍投递说明 |
| `TestRejectsWrongToken` / `TestChatAllowlist` | 来源校验与白名单 |
| `TestRawPayloadPrefersRawBody` | 长连接事件走与 webhook 相同的解析路径 |
| `TestDispatcherHandlesWebsocketPayload` | 长连接报文 → 解析 → 批次 → 投递 |
| `TestConnectorContract` | Connector 契约：多事件、多会话分发 |

手动起服务验证事件入口：

```bash
AGENTHUB_CHANNEL=feishu \
FEISHU_APP_ID=cli_sim FEISHU_APP_SECRET=sim \
FEISHU_VERIFICATION_TOKEN=vt \
AGENTHUB_WEBHOOK_ADDR=127.0.0.1:8789 \
go run ./cmd/agentd

curl -s -X POST localhost:8789/feishu/events \
  -H 'Content-Type: application/json' \
  -d '{"type":"url_verification","challenge":"c-123","token":"vt"}'
# {"challenge":"c-123"}
```

## 目录

| 路径 | 说明 |
|---|---|
| `cmd/agentd` | 服务入口：装配、信号、退出码 |
| `internal/channel` | 与平台无关的入站消息与发送接口 |
| `internal/core` | 受理、去重、批次快照、会话串行、投递（当前内存实现） |
| `internal/agent` | 执行器契约（当前为回显实现，M2 接模型） |
| `internal/platform/feishu` | 飞书事件解析、OpenAPI 客户端、回调入口 |
| `internal/config` | 配置加载与校验（环境变量 > YAML > 默认值） |
| `internal/obs` | 结构化日志与 Prometheus 指标 |
| `internal/store` | PostgreSQL 连接与数据访问 |
| `internal/httpapi` | `/healthz`、`/metrics` 与管理面 |
| `migrations` | SQL 迁移（只前进；goose 在 M1 接入） |
| `docs` | PRD、架构、飞书能力、运维、路线图 |

## 文档

- [PRD](docs/PRD.md)
- [架构设计](docs/architecture.md)
- [飞书能力矩阵](docs/feishu-capabilities.md)
- [运维手册](docs/runbook.md)
- [路线图](docs/roadmap.md)

## 常用命令

```bash
make run          # 本地运行
make test         # 单元测试
make build        # 产出 bin/agentd
make fmt          # gofmt
make vet          # go vet
```

## 状态

- **M0 骨架**：配置校验、日志、指标、健康检查、迁移脚本、优雅退出。
- **M1 飞书闭环（进行中）**：事件解析（文本/富文本/图片/文件/音视频）、去重、白名单、
  群聊 @ 判定、批次合并、会话串行、停止、投递（回复原消息）已打通，并有 fake API 全链路测试。
- **长连接（已完成）**：`FEISHU_TRANSPORT=websocket` 为默认；SDK 负责握手与重连，
  服务侧带指数退避重试；`/healthz` 暴露 `feishu: connecting|connected|reconnecting`。
- **M1 待补**：附件流式下载、真实模型接入。
- **M2+**：PostgreSQL 持久化、outbox 重试、卡片与表情。

详细排期见 [路线图](docs/roadmap.md)。
