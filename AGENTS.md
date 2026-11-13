# AGENTS.md — AgentHub 开发约定

面向在本仓库工作的编码 agent。人类开发者同样适用。

## 1. 项目定位

飞书/钉钉团队 AI 助手：消息 → 去重落库 → 批次 → pi 运行时（工作区 + 会话文件）→ 卡片/文本投递。

Go 服务 + PostgreSQL 为事实源；agent 由 pi 子进程执行（镜像内置 Node 22 + pi 1.4.0）。

## 2. 常用命令

```bash
go build ./...          # 构建
go test ./...           # 全部测试（数据库相关用例会自动跳过）
go vet ./...            # 静态检查
gofmt -l -w .           # 格式化
docker compose up -d --build   # 本地部署（app + postgres）
```

真实 PostgreSQL 用例：

```bash
AGENTHUB_TEST_DATABASE_URL='postgres://postgres:pass@127.0.0.1:5432/agenthub_test?sslmode=disable' \
  go test -p 1 ./internal/core ./internal/store -count=1
```

- 必须用**独立测试库**：测试会 `TRUNCATE` 表；指向 `postgres` 库会自动跳过。
- 多个包共用一个测试库时用 `-p 1` 串行执行，避免互相清表。

CI 执行 `gofmt -l` / `go vet` / `go test` / `go build`，提交前本地跑一遍。

## 3. 目录与依赖方向

```
cmd/agentd                 入口与装配
internal/config            配置加载与校验
internal/obs               日志、指标
internal/store             PostgreSQL、迁移、Journal
internal/core              受理、批次、会话代、投递、事件
internal/agent             运行接口与模型设置
internal/agent/pi          pi RPC、会话文件、工作区
internal/mcp               MCP stdio 客户端
internal/platform/feishu   通道实现
internal/admin             管理台与 API
migrations/                迁移（只前进）
examples/skills/           技能模板（业务技能由部署方提供，不入仓库）
```

依赖方向：`platform → core → store`，`core` 不依赖 `platform/feishu`；`agent` 不依赖 `core`。新增跨层依赖先改本节。

## 4. 硬性约定

- Go 1.23，标准库优先；新增第三方依赖需要说明理由并更新 `go.mod`。
- **迁移只前进**：已应用的 `migrations/NNNN_*.sql` 不允许修改，只能新增编号；启动时 `store.Migrate` 按文件名顺序执行，并用 advisory lock 防并发。
- 配置优先级 `环境变量 > config.yaml > 默认值`；新增配置必须同步 `.env.example` 与 `docs/runbook.md`。
- 日志不得包含密钥、临时下载地址、完整敏感载荷；错误信息只给字段名与原因。
- 技能格式：目录 `<name>/SKILL.md`（frontmatter `name`/`description`）或扁平 `<name>.md`；运行时目录为 `<data>/pi/agent/skills`，业务技能不入仓库。
- MCP 只支持 stdio；新增传输方式需同时补充测试与文档。
- 管理面没有内置认证，任何新增路由都假定前置网关。

## 5. 必须保持的不变量

| 编号 | 不变量 | 落点 |
|---|---|---|
| I1 | 同一会话最多一个非终态运行 | `Engine.runBatch` 以 `active[conv]` 保证 |
| I2 | 批次输入创建后不可变 | 批次快照后清空队列，新消息进下一批 |
| I3 | 先落库、后确认 | Dispatcher 等 `Submit` 返回才 ACK |
| I4 | 投影失败不回写业务状态 | 卡片/表情失败只降级 |
| I5 | 投递幂等 | `delivery.IdempotencyKey` + 状态机 |
| I6 | 迟到事件丢弃 | 按运行标识隔离 |

改动涉及以上语义时，必须同时补测试并更新 `docs/architecture.md`。

## 6. 测试要求

- 新增行为至少一个用例；涉及并发的必须能在 `-count=1` 多跑几遍仍稳定。
- 需要数据库的用例读 `AGENTHUB_TEST_DATABASE_URL`，未设置时 `t.Skip` 并写明原因，**不得**用内存实现替代 PostgreSQL 断言。
- 测试替身只能替代外部依赖（飞书 API、模型网关），不能替代被测逻辑。
- 提交前：`gofmt`、`go vet`、`go test ./...` 全绿。

## 7. 提交流程

- 一个主题一个提交，信息写清「做了什么 + 怎么验证」；
- 改动配置/接口/迁移时，同一提交内更新 README 或 docs；
- 不提交 `.env`、`data/`、`bin/`、日志。

## 8. 已知边界

- 卡片停止按钮需要飞书能访问 `/feishu/card`，无公网入口时用 `/stop`；
- 多实例未支持（长连接集群模式只有一个实例收到事件）；
- 管理台只能读配置，修改仍走 `.env` 并重启；
- 技能一次性全部注入提示词，技能很多时需要改为检索式加载。
