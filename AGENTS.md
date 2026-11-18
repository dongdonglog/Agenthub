# AGENTS.md — AgentHub 开发约定

面向在本仓库工作的编码 agent，人类开发者同样适用。**先读完本文件再改代码**；
它描述的是当前代码的真实行为，不是路线图。

---

## 1. 项目定位

飞书 / 钉钉里的 AI SRE 数字同事：

```text
告警 → 案件 → 证据 → 诊断 → 处置报告 → 审批动作 → 群
```

分工只有一句：

> **pi 负责「想」和「写」，Go 负责「记住」和「送出去」。**

- **事实源**：PostgreSQL。卡片、表情、文本都只是投影，投影失败不回写业务状态。
- **执行器**：`pi --mode rpc` 子进程（镜像内置 Node 22 + pi **1.4.0**，版本写死在 `Dockerfile`）。
- **Go 的职责**：受理、去重、批次、会话代、调度、投递、审批、审计、管理面。

三个「不是」：不是 AIOps 平台（不替代 Prometheus/Grafana）、不是无人运维（默认只读，
写操作要审批）、不是通用 Agent 平台（只做 SRE / 技术支持）。

---

## 2. 常用命令

```bash
go build ./...          # 构建（不需要数据库）
go test ./...           # 全部测试；数据库用例自动 t.Skip
go vet ./...
gofmt -l -w .

# 前端：源码 web/，构建产物写入 internal/admin/static（已入库，见 §7）
cd web && npm ci && npm test && npm run build

# 本地整体部署（app + postgres）
docker compose up -d --build

# 只起骨架、不接飞书
COMPOSE_CHANNEL=off docker compose up -d
```

真实 PostgreSQL 用例：

```bash
AGENTHUB_TEST_DATABASE_URL='postgres://postgres:pass@127.0.0.1:5432/agenthub_test?sslmode=disable' \
  go test -p 1 ./internal/core ./internal/store ./internal/aiops ./internal/support ./internal/ha ./internal/scheduler -count=1
```

- 必须用**独立测试库**：用例会 `TRUNCATE`；指向 `postgres` 库会 `t.Skip`。
- 多个包共用一个测试库时用 `-p 1` 串行，避免互相清表。
- 新增表时要同步改用到它的 `TRUNCATE` 列表，否则清理不彻底。

提交前必须全绿：`gofmt -l` 无输出、`go vet`、`go test ./...`、`cd web && npm test`。
CI（`.github/workflows/ci.yml`）另外跑 `go test -race ./...` 与 `CGO_ENABLED=0 go build`，
改动并发代码时本地也跑一次 `-race`。

---

## 3. 目录地图

### 顶层

| 路径 | 作用 |
|---|---|
| `cmd/agentd/` | 唯一服务入口：配置加载、依赖装配、后台循环、优雅退出（约 1000 行，读它是理解全局的最快方式） |
| `cmd/agenthub-ops-mcp/` | 独立二进制：MCP stdio 工具网关（内置 Prometheus/Loki/Tempo/K8s 只读工具 + 聚合上游 MCP） |
| `internal/` | Go 业务代码，见下表 |
| `migrations/` | PostgreSQL 迁移，**只前进**，启动时按文件名顺序执行 |
| `web/` | React 18 + TypeScript + Vite 管理台，构建产物写入 `internal/admin/static` |
| `pi-package/` | 随代码分发的 pi 资源：`extensions/`、`skills/`、`prompts/`、`package.json` |
| `examples/mcp/` | 推荐 MCP 配方与 `time_server.py` 冒烟示例 |
| `examples/skills/` | 技能格式模板（不含业务信息） |
| `deploy/prometheus/alerts.yml` | AgentHub 自身的 Prometheus 告警规则 |
| `scripts/` | `backup.sh` / `restore.sh`（基于 docker compose） |
| `docs/` | 文档，见 §9 |
| `data/` | **运行时数据，不入库**：skills、mcp.yaml、profiles.yaml、settings.yaml、pi/、artifacts/ |
| `tmp/` | 本地临时归档，不入库 |

### internal/ 各包

| 包 | 职责 | 关键文件 |
|---|---|---|
| `config` | 配置加载与校验；`.env` 解析 | `config.go`（`Load` → `defaults` → YAML → `applyEnv` → `Validate`） |
| `obs` | 结构化日志 + 极简 Prometheus 指标注册表 | `log.go`、`metrics.go`（只有 `Inc`/`Add`/`Set`） |
| `store` | PostgreSQL：连接池、迁移、Journal、CaseStore、IncidentStore、KnowledgeStore、ProfileStore、ScheduledTask | `journal.go` 是投递/运行/消息的持久化主力 |
| `core` | 受理、去重、批次、会话代、调度、运行编排、投递（含租约）、产物 | `engine.go`、`run.go`、`deliver.go`、`dispatch.go`、`control.go`、`restore.go`、`prune.go`、`artifact.go` |
| `agent` | 执行器契约（`Agent` 接口、`Input`/`Output`）+ `Echo` 替身 + 技能解析 + 种子复制 | `agent.go`、`skills.go`、`seed.go` |
| `agent/pi` | pi 运行时：RPC、会话文件 v3、工作区、Profile 裁剪、配置生成 | `runtime.go`、`workspace.go`、`session.go`、`config.go`、`profile.go` |
| `channel` | 与平台无关的入站消息、出站请求与能力声明（含 `Reactor`、`ArtifactSender`） | `channel.go` |
| `platform/feishu` | 飞书长连接/webhook、事件解析、卡片、表情投影、附件下载、管理员干预、审批卡片 | `client.go`、`dispatch.go`、`event.go`、`presenter.go`、`card_action.go`、`approval_card.go` |
| `platform/dingtalk` | 钉钉 Stream、命令、附件、纯文本投递（**无卡片**） | `channel.go`、`commands.go`、`attachments.go` |
| `aiops` | Alertmanager/Grafana/通用/PagerDuty webhook → 告警聚合 → 案件 → 只读诊断运行 | `http.go`、`aiops.go`、`catalog.go` |
| `alerting` | 各告警平台 webhook 归一化 | `event.go`、`adapters.go` |
| `support` | 工单 webhook → 案件 → 技术支持员工运行，带知识库检索 | `http.go`、`support.go` |
| `knowledge` | 导入 Markdown 知识库（`<team>/` 子目录做团队隔离） | `ingest.go` |
| `action` | 审批后受控动作：只支持 allowlist namespace 内的 K8s scale/restart | `action.go` |
| `scheduler` | 5 段 cron 定时任务，20s 轮询、`FOR UPDATE SKIP LOCKED` 抢占 | `scheduler.go` |
| `ha` | PostgreSQL advisory lock 单活入口选主（多实例只有一个收长连接） | `leader.go` |
| `httpapi` | `/healthz`、`/metrics`、`/api/v1/alerts/{source}`、`/api/v1/tickets` | `server.go`、`health.go`、`serve.go` |
| `admin` | 管理台静态资源（`embed static`）+ `/admin/api/v1/*`（60 余条路由）+ RBAC | `admin.go`（装配与鉴权）、`routes.go`（**路由表**）、`handlers_*.go`（按资源拆分）、`store.go`、`import.go` |
| `modelcfg` | 多供应商模型配置 v2（providers/models/routes）与 v1 迁移 | `config.go` |
| `modelrouter` | 回环 OpenAI 兼容主备路由：候选选择、重试、熔断、流式 | `router.go`、`circuit.go` |
| `mcp` | **目前未被主程序引用**：通用 MCP stdio 客户端（pi 已原生支持 MCP）。改主链路时不用管它 | `client.go`、`manager.go` |
| `version` | `Version` / `Commit`，发布时用 `-ldflags` 覆盖 | `version.go` |

### 依赖方向（以编译期 import 为准）

```text
channel                     叶子，无内部依赖
  ↑
agent        → channel
  ↑
agent/pi     → agent, channel
  ↑
core         → agent, channel          （core 不 import store）
  ↑
store        → core                    （实现 core.Journal 等接口）
platform/*   → core, (channel, agent)  （feishu 额外用 store.CaseStore）
scheduler    → core, store
aiops        → core, store, alerting
support      → core, store, channel
admin        → 几乎所有包（装配层，允许）
httpapi      → config, obs, store
modelrouter  → modelcfg
```

要点：

- **`core` 不能 import `store`**。持久化通过 `core.Journal` 等接口注入，`store` 反向依赖 `core` 的类型。
  想把数据库细节塞进 `core`，就是设计错误。
- `platform/*` 不得把平台字段（`chat_id`、`parent_id` 等）泄漏到 `core`；
  `core` 里出现平台概念即视为回归。
- `agent` 不依赖 `core`。
- 新增跨层依赖时，先更新这一节再写代码。

---

## 4. 一次请求的完整链路

```text
飞书事件（长连接 / webhook）
  → platform/feishu.Dispatcher          白名单、@ 判定、引用解析、管理员干预
  → core.Engine.Submit                  去重 + 落库 + 入队（先落库、后 ACK）
  → core.Engine.Flush → runBatch        批次快照、TTL 过期、并发名额、会话串行
  → agent/pi.Runtime.Run                会话代工作区、Profile 裁剪、预写会话文件、启 pi
  → pi（read/bash/edit/write/grep/find/ls/tool_search + MCP + extensions）
  → submit_report 扩展 → reports/report.json → core.reportSink → case_report（draft）
  → core.Engine 终态 + Delivery（幂等键 run_<id>:result）
  → core.Engine.send                   领取租约 → calling → 通道发送
```

关键路径与对应文件：

| 环节 | 文件 |
|---|---|
| 去重与入队 | `internal/core/engine.go` `Submit` |
| 批次冻结 | `internal/core/run.go` `runBatch` |
| 停止 / 清空 / 换代 | `internal/core/control.go` |
| 投递租约与重试 | `internal/core/deliver.go`、`internal/store/journal.go` |
| 重启恢复 | `internal/core/restore.go`（最近 1000 条，processing→queued） |
| 内存裁剪 | `internal/core/prune.go`（100 条/会话、200 会话、200 运行、500 投递） |
| 工作区与会话文件 | `internal/agent/pi/{runtime,workspace,session}.go` |

---

## 5. 必须保持的不变量

| 编号 | 不变量 | 落点 |
|---|---|---|
| I1 | 同一会话最多一个非终态运行 | `runBatch` 的 `active[conv]` 守卫；`queued`→运行→终态 |
| I2 | 批次输入创建后不可变 | `runBatch` 先快照再清空 `order[conv]`，新消息进下一批 |
| I3 | 先落库、后确认 | Dispatcher 等 `Engine.Submit` 返回才 ACK 事件 |
| I4 | 投影失败不回写业务状态 | 卡片/表情失败只降级并记日志 |
| I5 | 投递幂等 | `delivery.IdempotencyKey` 唯一 + `claimed/calling/ambiguous` 状态机 + 租约令牌 |
| I6 | 迟到事件丢弃 | 事件按 `run_id` 归属；终态不可逆 |

改动涉及以上任一语义时：**必须同时补测试并更新 `docs/architecture.md`**。
并发相关用例要能在 `-count=1` 下重复跑稳，并通过 `-race`。

补充语义（容易被忽略）：

- 停止：先写 `stopping` → 中止 → 最终 `interrupted`；`failed`/`interrupted` 都要有说明投递。
- 结果不明（网络超时）→ `ambiguous`，**不自动重发**，只在管理台人工重试。
- 排队消息超过 `AGENTHUB_QUEUE_TTL_SECONDS` → `expired`，投递「请重新发送」。
- `/reset` 递增会话代；旧代消息不进入新代上下文，旧代工作区可被清理。
- 群聊按群（`chat_id`）统一轮次编号。

---

## 6. 配置系统

优先级：**环境变量 > `config.yaml`（`AGENTHUB_CONFIG` 指定）> 默认值**；
启动前先读 `.env`（`AGENTHUB_ENV_FILE`，默认 `./.env`，**已存在的环境变量优先**）。

- 唯一配置结构在 `internal/config/config.go`；新增字段必须同时在
  `defaults()`、`applyEnv()`、`Validate()` 三处登记。
- 同步文档：`.env.example` 与 `docs/runbook.md`（提交前检查）。
- 配置错误以**退出码 2** 退出，错误信息只含字段名与原因，不含值。
- 端口约定：健康/指标 `8787`，管理面 `8788`，飞书 webhook `8789`。
- 管理面令牌三档：`AGENTHUB_ADMIN_TOKEN`（admin）、`AGENTHUB_ADMIN_OPERATOR_TOKEN`（可写）、
  `AGENTHUB_ADMIN_VIEW_TOKEN`（只读）。全空 = 不鉴权，只能靠网关保护。
- 写请求额外要求 `X-Requested-With: agenthub` 头（CSRF 兜底，前端已带）。

已知配置债（改动时注意，不要照抄文档）：

- 单次运行超时只有 `PI_TIMEOUT_SECONDS`（默认 600s），没有独立的 agent 侧上限。
- `PI_ISOLATION` 代码默认 `namespace`（仅 Linux 生效），compose 默认 `process_group`。
- `AGENTHUB_SEED_SKILLS_DIR` 默认值 `skills`（仓库根目录），但仓库里**没有**这个目录，
  所以默认种子实际为空；见 §7。

---

## 7. 技能（三种目录，别混）

| 位置 | 谁写 | 谁读 | 说明 |
|---|---|---|---|
| `pi-package/skills/<name>/SKILL.md` | 随代码提交 | `cmd/agentd` 启动时以 `--skill <dir>` 直接传给 pi | 目前有 `sre-oncall`、`tech-support`。**改这两个文件即改生产行为** |
| `<data>/pi/agent/skills` | 管理台（含 ZIP 导入） | pi 扫描该目录 | 部署方业务技能；`.disabled/`、`.archived/` 子目录表示停用/归档 |
| `<data>/skills` | `agent.SeedSkills` 从种子目录复制 | 仅作为 `PrepareAgentDir` 的 `SkillsSrc` | 目标为空时才复制一次，不覆盖管理台改动 |

`PrepareAgentDir` 在 `<data>/pi/agent/skills` 为空时把 `<data>/skills` 整树复制过去，
之后管理台就在 `pi/agent/skills` 上读写。

技能格式：

- 目录式 `<name>/SKILL.md`（frontmatter 必须有 `name`、`description`）——**推荐**；
- 扁平式 `<name>.md`（管理台兼容旧格式）；
- 目录式只认 `SKILL.md`，`references/` 下的文档不会被当成技能；
- `README.md` 与 `_`/`.` 开头的文件被忽略；
- 技能根目录的可执行文件会被链接到会话 `.agenthub-bin/` 并加入 `PATH`；
- **业务技能（客户名称、集群名、账号名）绝不入库**，`.gitignore` 的 `/skills/` 只管仓库根目录。

每次运行前按 Profile 重新裁剪 `skills/` 与 `mcp.json`（`internal/agent/pi/profile.go`），
空列表表示不限制。

---

## 8. 数据与运行时目录

`AGENTHUB_DATA_DIR`（默认 `./data`，容器 `/data`）下的布局：

```text
data/
├── skills/                   种子技能（见 §7）
├── mcp.yaml                  管理台维护的 MCP（YAML 数组）
├── profiles.yaml             资源配置档案（技能 + MCP 选择）
├── active_profile.yaml       当前生效档案
├── settings.yaml             模型配置 v2（providers/models/routes）
├── services.yaml             服务目录（owner/team/runbook/dashboard/repo/namespace）
├── knowledge/                知识库 Markdown（<team>/ 子目录）
├── attachments/<msg_id>/     入站附件
├── artifacts/<run_id>/       出站产物（导入后清理工作区原件）
└── pi/
    ├── agent/                部署级 pi 配置：models.json、SYSTEM.md、mcp.json、skills/
    ├── sessions/<key>.jsonl  pi 会话文件 v3（key = sha1(会话)[:16]-g<代>）
    └── workspaces/<key>/     会话代工作区：.agenthub-agent/、AGENTS.md、attachments/、outbox/<run_id>/、submit→
```

工作区规则（`internal/agent/pi/workspace.go`）：

- `.agenthub-agent/` 是部署配置的副本，Profile 裁剪落在这里；**不要**让技能读写它以外的绝对路径。
- `submit` 是指向 `outbox/<run_id>/` 的符号链接；运行结束收集普通文件 → `data/artifacts/<run_id>/`。
- 产物超限或通道不支持时追加可读说明，产物仍保留供管理台下载。

数据库在用表（`migrations/` 里 `0009` 已删掉 0001 的多租户草案，别再引用那些表）：

`conversation_state`、`message_state`、`run_state`、`delivery_state`、`run_artifact`、
`run_trace`、`conversation_profile`、`scheduled_task`、`audit_log`、`reaction_projection`、
`incident`、`alert_event`、`incident_event`、`employee`、`case_item`、`case_event`、
`case_feedback`、`case_review`、`case_report`、`approval_request`、`knowledge_doc`、
`schema_migrations`。

迁移规则：

- 已应用的 `migrations/NNNN_*.sql` **不可修改**，只能新增编号（编号有跳号也照常新增）；
- 每个文件在单事务内执行，`store.Migrate` 用 advisory lock（key 727272）防并发；
- 迁移只在有数据库时执行；无 `AGENTHUB_DB_URL` 时服务退化为内存态。

---

## 9. 文档现状（哪些能信）

| 文档 | 状态 |
|---|---|
| `README.md` | 对外定位与快速开始，大致可用 |
| `docs/runbook.md` | 配置表与排障，**较准** |
| `docs/aiops.md` | AIOps 设计与接口，可用 |
| `docs/tools.md` | 镜像工具链与 MCP 网关，可用 |
| `docs/best-practices.md` | 上线顺序与验收口径，可用 |
| `docs/deploy.md` | 部署与飞书配置 |
| `docs/architecture.md` | 已按当前实现重写；与代码冲突时以代码为准 |
| `docs/openapi.yaml` | HTTP 接口规范；**有测试守着**（`internal/admin/routes_test.go`） |
| `CHANGELOG.md` | 到 `0.4.0`，`Unreleased` 段落后于代码 |

判断「代码现在怎么做」时，**以 `cmd/agentd/main.go` + 本文件为准**。
历史上存在过 `PRD.md` / `roadmap.md` / `comparison.md` / `intro.md`，描述的是
已废弃的设计（River、templ+htmx、自研 Go loop、沙箱容器），已删除；
不要在别处重新引入这些概念。

---

## 10. 测试要求

- 新增行为至少一个用例；并发相关必须能重复跑稳（`-count=1` 多跑几遍 + `-race`）。
- 数据库用例读 `AGENTHUB_TEST_DATABASE_URL`，未设置时 `t.Skip` 并写明原因；
  **不得**用内存实现替代 PostgreSQL 断言。
- 测试替身只能替代外部依赖（飞书 API、模型网关、pi 进程），不能替代被测逻辑：
  - pi 用 `internal/agent/pi/testdata/fakepi`（`FAKE_PI_MODE` 控制输出事件）；
  - 飞书用 `internal/platform/feishu/client_test.go` 里的 fake API；
  - 模型网关用 httptest。
- `internal/core/*_test.go` 是语义守门员：改 `engine.go`/`run.go`/`deliver.go` 前后都跑
  `go test ./internal/core -count=1`。
- `web/` 目前只有 `ui.test.tsx` 两个用例；改 UI 时至少保证 `npm run build` 通过并手动点一遍。

---

## 11. 硬性约定

- Go 1.23，标准库优先；新增第三方依赖需说明理由并更新 `go.mod`。
- 日志不得包含密钥、临时下载地址、完整敏感载荷；错误信息只给字段名与原因。
- MCP 传输：网关（`cmd/agenthub-ops-mcp`）与 pi 都走 **stdio**；`http` 只在 pi 的 `mcp.json` 里以
  `exposure: deferred` 出现。新增传输方式要同时补测试与文档。
- 写操作只有一条通路：`approval_request` → 审批 → `internal/action` 执行，且仅限 allowlist namespace。
  不要让 pi 直接拿到 kubectl 写权限。
- 管理面没有内置认证，任何新增路由都假定前置网关；新增写路由要过 RBAC 与 CSRF 头。
- `pi-package/` 下的扩展是生产代码，改 `agenthub-guard.ts` 等于改安全边界。

---

## 12. 提交流程

- 一个主题一个提交，信息写清「做了什么 + 怎么验证」。
- 改动配置 / 接口 / 迁移时，同一提交内更新 `.env.example`、`docs/runbook.md` 或 `README.md`。
- **改了 `web/src/` 必须 `npm run build` 并一起提交 `internal/admin/static/`**，否则二进制里的
  管理台仍是旧版（CI 会重新构建，但本地 `go run` 不会）。
- 不提交 `.env`、`data/`、`bin/`、日志、构建出的二进制、备份归档。

---

## 13. 已知边界与坑

- **卡片停止按钮 / 审批卡片**需要飞书能访问 `/feishu/card`（公网或隧道）；
  无公网入口时用 `/stop` 命令，审批走管理台。
- **审批闭环**：pi 通过 `request_approval` 工具提交动作申请 → Go 校验后创建 `approval_request`
  并推审批卡片 → 人批准 → `internal/action` 重新校验参数与 namespace allowlist 后执行。
  模型只能「申请」，不能执行；批准前它应明确说明动作尚未发生。
- **告警内容会进提示词**（`internal/aiops/aiops.go` `buildPrompt`）：annotation 与日志原文未经清洗，
  存在提示注入面；缓解手段是只读运行 + 工具 allowlist + guard 扩展，不要假设输入可信。
- **只读模式会拦 `request_approval` 之外的一切写路径**：guard 扩展阻断 `edit`/`write` 与破坏性 bash，
  但审批申请本身是允许的（它只写 JSON 文件）。
- 钉钉只有文本投递，没有卡片；回复依赖 `sessionWebhook`。
- 多实例只保证**入口单活**（advisory lock），不是无状态多活；定时任务与清理循环在每个实例都会跑。
  - 定时任务崩溃恢复：`ClaimDue` 记 `claimed_at`，`RecoverStaleClaims` 找回超过 30 分钟未回填的领取。
- 无 `AGENTHUB_DB_URL` 时是无持久化内存模式，仅供本地冒烟，不要用于任何长期环境。
- 管理台只读配置；改模型/飞书凭据仍走 `.env` 并重启（模型配置支持管理台热更新）。
- 技能全部按 Profile 注入，不做检索；技能很多时提示词成本会涨。
