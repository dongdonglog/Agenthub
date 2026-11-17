# AgentHub

**把 AI SRE 数字同事放进飞书和钉钉：接警、查证据、做处置、写报告。**

AgentHub 是一个自托管的 AI 运维数字员工。告警或工单进入后，交给 [pi](https://github.com/earendil-works/pi) 在独立工作区里查指标、日志、trace 和 K8s，
把诊断、处置报告、审批和结果送回飞书/钉钉。技能、MCP、模型、审计和案件都在管理台统一管理。

[![CI](https://github.com/dongdonglog/Agenthub/actions/workflows/ci.yml/badge.svg)](https://github.com/dongdonglog/Agenthub/actions/workflows/ci.yml)
[![Release](https://github.com/dongdonglog/Agenthub/actions/workflows/release.yml/badge.svg)](https://github.com/dongdonglog/Agenthub/actions/workflows/release.yml)
[![Image](https://img.shields.io/badge/image-donglog%2Fagenthub-2496ED?logo=docker)](https://hub.docker.com/r/donglog/agenthub)
[![Go](https://img.shields.io/badge/go-1.23-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

> **English**: AgentHub is a self-hosted AI SRE teammate for Feishu and DingTalk.
> It ingests alerts, runs the pi agent against observability tools, and produces
> evidence-backed cases and incident reports with human-approved actions — one Go
> binary plus PostgreSQL. MIT licensed.

![管理台](docs/images/admin-overview.png)

## 为什么是 AgentHub

企业里的 AI 常常停在演示阶段，卡在三件事：

- **进不了工作流**：要开网页、复制粘贴，同事不用；
- **看不到过程**：只有一个转圈，出错不知道卡在哪；
- **没法治理**：技能、工具、模型散在配置文件里。

AgentHub 把入口放进日常 IM，把执行放进可清理的工作区，把配置和审计放进管理台。

## 特性

- **IM 原生**：飞书长连接 / Webhook、钉钉 Stream；支持 @、引用消息、附件、消息状态表情；
- **告警接入**：`/api/v1/alerts/{source}` 支持 Alertmanager、Grafana、通用 webhook、PagerDuty；统一事件模型、去重、恢复、按来源 token 与限流；
- **真执行**：pi 在会话代工作区里读写文件、跑 `bash`、搜索、调 MCP；镜像内置 git/gh/glab/ssh/jq/yq/rg/fd/kubectl 等常用 CLI；
- **AI 诊断**：pi 原生 skills、prompts、extensions、subagent、compaction；Prometheus/Loki/Tempo/K8s 通过 MCP 查证据；只读 guard + tool_result 脱敏 + 证据门禁；
- **处置报告**：pi 通过 `submit_report` 提交结构化报告；Go 保存草稿，人工补根因/验证后定稿、关闭案件、发飞书；
- **案件/事故/报告**：告警聚合、时间线、SLA、责任员工、交接、复盘、手动恢复；列表支持时间范围筛选；
- **MCP 工具网关**：一个 `agenthub-ops-mcp` 聚合 Prom/Loki/Tempo/K8s 与外部 MCP；工具前缀、只读策略、allow/deny、JSONL 审计、超时隔离；
- **模型主备**：多供应商、有序候选、超时/429/5xx 自动切换、熔断，图片走 vision 线路；
- **调度**：跨会话并发上限、公平队列 + 等待老化、每会话限流、cron 定时任务；
- **可治理**：多实例单活入口、管理台 admin/operator/view 令牌、CSRF、审计、审批、受控 K8s 动作、在线/批量重置、投递人工重试、健康检查、Prometheus 指标、备份恢复脚本；
- **可靠投递**：领取租约 + `claimed/calling` 阶段；结果不明只标歧义、不盲目重发；排队消息有 TTL。

## 快速开始

前置：Docker 24+、Docker Compose v2，以及一个飞书企业自建应用。

```bash
git clone https://github.com/dongdonglog/Agenthub.git
cd Agenthub
cp .env.example .env
```

编辑 `.env`，至少填这些：

```dotenv
AGENTHUB_CHANNEL=feishu
POSTGRES_PASSWORD=change-me
FEISHU_APP_ID=cli_xxx
FEISHU_APP_SECRET=xxx
FEISHU_BOT_OPEN_ID=ou_xxx
AGENTHUB_AGENT_API_KEY=sk-xxx
```

模型默认走 OpenAI；如果使用其他 OpenAI 兼容网关，再设置
`AGENTHUB_AGENT_BASE_URL` 与 `AGENTHUB_AGENT_MODEL`。
钉钉部署把 `AGENTHUB_CHANNEL` 改为 `dingtalk`，并填 `DINGTALK_CLIENT_ID` /
`DINGTALK_CLIENT_SECRET`。

启动：

```bash
docker compose up -d
curl -s 127.0.0.1:8787/healthz
```

启用 AIOps 至少再配置：

```dotenv
AGENTHUB_AIOPS_ENABLED=true
AGENTHUB_AIOPS_TOKEN=change-me
AGENTHUB_AIOPS_CHAT_ID=oc_xxx
AGENTHUB_PROMETHEUS_URL=http://prometheus:9090
AGENTHUB_LOKI_URL=http://loki:3100
```

然后把 Alertmanager webhook 指向：

```text
http://<agenthub>:8787/api/v1/alerts/alertmanager
```

默认端口只绑回环：

- `127.0.0.1:8787`：`/healthz`、`/metrics`
- `127.0.0.1:8788`：管理台

远端服务器用 SSH 隧道打开管理台：

```bash
ssh -L 8788:127.0.0.1:8788 user@your-server
# 然后访问 http://127.0.0.1:8788/admin/
```

飞书应用配置（长连接、事件、权限、白名单）见
[部署指南 · 首次部署](docs/deploy.md#首次部署)。把机器人拉进群，@ 它即可。

使用已发布镜像：

```bash
AGENTHUB_TAG=latest docker compose pull
AGENTHUB_TAG=latest docker compose up -d
```

## 架构

```mermaid
flowchart LR
  U[飞书 / 钉钉] --> C[通道适配<br/>解析 · 白名单 · 附件]
  A[Alertmanager / Grafana / 通用 / PagerDuty] --> C
  C --> E[Engine<br/>去重 · 批次 · 会话代]
  E <--> DB[(PostgreSQL)]
  E --> R[pi 运行时]
  R --> W[会话工作区<br/>配置 · 附件 · outbox]
  R --> G[MCP 工具网关<br/>只读策略 · 审计]
  G --> OBS[Prometheus / Loki / Tempo / K8s]
  R --> MR[模型路由]
  MR --> P1[主模型]
  MR --> P2[备选模型]
  R --> REP[submit_report<br/>报告草稿]
  REP --> DB
  E --> O[Outbox<br/>重试 · 补偿]
  O --> U
  ADM[管理台 / API] --> E
```

一次运行：

```
消息 → 解析/策略 → 落库 → 批次快照 → 历史上下文 → pi（技能 + 工具）
     → 文本/卡片 → 投递（幂等、失败重试）→ 事件与 token 落库
```

关键不变量（有测试守护）：

- 同一会话同时最多一个运行；
- 批次创建后输入冻结，新消息进入下一批；
- 先落库再 ACK，投递使用幂等键；
- 卡片/表情投影失败不影响业务状态；
- `/reset` 递增会话代，旧代历史不进入提示词。

## 管理台

React + TypeScript + Vite，构建产物嵌入 Go 二进制；浅色/深色切换。

![技能管理](docs/images/admin-skills.png)

| 页面 | 内容 |
|---|---|
| 工作台 | 处理中案件、等待人工、firing 事故、报告数量 |
| 案件 | 按状态/类型/时间/关键词筛选；点开诊断、证据、审批、报告 |
| 事故 | 告警聚合、认领、手动恢复、关联案件与诊断 |
| 报告 | 处置报告列表；pi 自动草稿，人工定稿、下载、发飞书 |
| 系统 | 运行概览、在线/历史会话、Skill、MCP、定时任务、审计、设置 |

## 模型主备

设置页支持多供应商、多模型和有序线路：

```yaml
providers:
  - name: default
    base_url: https://llm-gateway.example.com/v1
    api_key: sk-...
models:
  - id: primary
    provider: default
    model: deepseek-chat
    capabilities: [text, tools]
routes:
  - name: main
    candidates: [primary, fallback]
```

- 网络错误、超时、408、429、5xx、401/403 自动切到下一个候选；400/422 不切；
- 流式输出开始后不切换；
- 候选连续失败进入熔断冷却，冷却后半开探测；
- 图片请求只路由到声明 `vision` 的候选；
- pi 指向内置回环路由，不需要额外网关进程。

## 配置

完整环境变量见 [运维手册](docs/runbook.md)，最常用的：

| 变量 | 默认 | 说明 |
|---|---|---|
| `AGENTHUB_CHANNEL` | `off` | `off` / `feishu` / `dingtalk` |
| `AGENTHUB_DB_URL` | 空 | 空为内存模式；compose 会自动注入 |
| `FEISHU_APP_ID` / `FEISHU_APP_SECRET` / `FEISHU_BOT_OPEN_ID` | 空 | 飞书应用凭证 |
| `FEISHU_ALLOWED_USERS` / `FEISHU_ALLOWED_CHATS` | 空 | 白名单；留空表示拒绝所有人 |
| `AGENTHUB_AGENT_BASE_URL` / `AGENTHUB_AGENT_API_KEY` | 空 | 模型网关地址与 Key |
| `AGENTHUB_AGENT_MODEL` | `gpt-4.1-mini` | 默认主模型 |
| `AGENTHUB_ADMIN_TOKEN` | 空 | 管理台访问令牌 |
| `AGENTHUB_MAX_CONCURRENT_RUNS` | `4` | 跨会话并发上限 |
| `AGENTHUB_QUEUE_TTL_SECONDS` | `21600` | 排队消息最长等待；0 关闭 |
| `AGENTHUB_DELIVERY_LEASE_SECONDS` | `120` | 投递租约；应大于单次通道调用耗时 |
| `AGENTHUB_OUTBOUND_QPS` | `0` | 跨会话共享出站速率；0 不限 |
| `FEISHU_ADMIN_OPEN_IDS` | 空 | 可引用机器人消息干预他人会话的管理员 |
| `DINGTALK_GROUP_OPEN` | `false` | 钉钉群内免用户白名单 |
| `AGENTHUB_ARTIFACT_MAX_BYTES` | `20MB` | 单个交付物上限 |
| `AGENTHUB_ARTIFACT_MAX_COUNT` | `10` | 单次运行交付物数量上限 |
| `AGENTHUB_ARTIFACT_MAX_TOTAL` | `50MB` | 单次运行交付物总量上限 |
| `PI_ISOLATION` | `namespace` | 容器受限时用 `process_group` |

> 安全提示：管理台默认只监听回环。暴露到公网前必须设置 `AGENTHUB_ADMIN_TOKEN`，
> 并在反向代理层加 TLS 与访问控制。

## 开发

需要 Go 1.23+、Node 22+、Docker（集成测试可选）。

```bash
make web            # 构建管理台到 internal/admin/static
go run ./cmd/agentd # 本地启动
go test ./...       # 后端测试
cd web && npm test  # 前端测试
```

前端开发：

```bash
cd web && npm ci && npm run dev   # 代理 /admin/api 到 127.0.0.1:18888
```

数据库集成测试：

```bash
AGENTHUB_TEST_DATABASE_URL='postgres://postgres:agenthub@127.0.0.1:55433/agenthub_test?sslmode=disable' \
  go test -p 1 ./internal/core ./internal/store
```

## 部署

- 镜像：`donglog/agenthub`，`linux/amd64` + `linux/arm64`，标签 `latest` / `0.4` / `0.4.0`；
- 发版：`git tag v0.4.1 && git push origin v0.4.1`，Actions 跑测试后构建并推送；
- 数据：PostgreSQL + `/data` 卷（pi 配置、会话、工作区）；
- 备份/恢复：`./scripts/backup.sh`、`./scripts/restore.sh`。

细节见 [部署指南](docs/deploy.md) 与 [运维手册](docs/runbook.md)。

## 文档

- [项目介绍](docs/intro.md)
- [AIOps / 数字员工](docs/aiops.md)
- [工具链与 MCP 网关](docs/tools.md)
- [部署指南](docs/deploy.md)
- [架构设计](docs/architecture.md)
- [运维手册](docs/runbook.md)
- [飞书能力实测](docs/feishu-capabilities.md)
- [与 AA 对比 / v1.0 清单](docs/comparison.md)
- [路线图](docs/roadmap.md)
- [PRD](docs/PRD.md)
- [CHANGELOG](CHANGELOG.md)

## 状态与边界

当前主线是 **v0.8.x 开发版**，适合内网或受控环境的小团队试用；生产发布以 GitHub Releases 为准。

- **定位**：Agentic AIOps / AI SRE 数字同事，不是通用 AIOps 平台，也不替代 Prometheus、Grafana、PagerDuty、ITSM；
- **只读默认**：诊断只读；写操作必须审批；K8s 动作仅支持 allowlist 内的 scale/restart；
- **真实验收**：已跑通真实 Alertmanager + Prometheus/Loki + 真实模型的 20 条独立告警；但仍是合成事件，不等于 20 个生产事故；
- **HA**：飞书/钉钉长连接由 PostgreSQL advisory lock 选主；单活入口，不是无状态多活；
- **钉钉**：回复依赖 `sessionWebhook`（约 1.5 小时），卡片未接入；入站文件/图片已下载；
- **成本**：已记录 token，尚未按单价换算金额；
- **pi**：镜像固定 1.4.0、会话文件 v3；升级需要回归；
- **管理台**：没有内建用户体系，建议只在内网或网关后暴露；
- **未完成**：生产长跑、压测、灾备演练、多租户、SLO/错误预算、真实钉钉双实例接管。
## 最佳实践

### 1. 先接告警，再接工具

最小上线顺序：

1. 接 Alertmanager/Grafana webhook；
2. 只读接 Prometheus/Loki；
3. 验证告警 → 案件 → pi 诊断 → 飞书；
4. 再加 Tempo/K8s；
5. 最后才加审批动作。

不要一开始就把所有 MCP、所有写工具全打开。

### 2. 只读是默认值

- 诊断运行默认 `ReadOnly`；
- 写操作必须走 `approval_request`；
- K8s 动作必须配置 namespace allowlist；
- 不允许模型直接执行 shell 写操作；
- 所有动作写审计和案件时间线。

### 3. 新能力优先加在 pi 侧

| 需求 | 放哪里 |
|---|---|
| 新 SOP、排查步骤、领域知识 | `pi-package/skills/<name>/SKILL.md` |
| 新输出格式 | `pi-package/prompts/<name>.md` |
| 新只读数据源 | 新增 MCP，挂到 `AGENTHUB_MCP_UPSTREAMS` |
| 安全/上下文钩子 | pi extension |
| 新告警来源、案件字段、页面、权限 | Go / React |

判断标准：

> **“知道更多、查得更多”放 pi；“触发、保存、权限、投递”放 AgentHub。**

### 4. 报告必须可验证

- 没有证据写“未验证”，不要写“服务正常”；
- 根因和验证结果必须由人确认后才能定稿；
- 定稿前检查：问题、影响、证据、根因、动作、验证、改进；
- 报告发飞书后保留 Markdown 归档。

### 5. 时间窗要对齐

- 告警触发时间、查询窗口、日志时间戳必须是同一个时区；
- 列表筛选使用 `from` / `to`（RFC3339 或 `YYYY-MM-DD`）；
- 没有触发时间时，先查最近 1 小时，并在报告里写明假设。

### 6. 用 20 个事件验收，不要用感觉验收

固定样本，至少记录：

- 证据覆盖率；
- 有用率；
- 根因正确/可验证比例；
- 未授权动作数（必须为 0）；
- 平均首响时间；
- token/成本。

如果观测数据缺失，先修数据接入，不要先改模型。

### 7. 密钥与权限

- `FEISHU_APP_SECRET`、模型 Key、MCP 凭据只放环境变量或密钥管理；
- 管理台只监听回环，公网必须加 TLS 与网关；
- 管理面使用 admin/operator/view 分权；
- MCP 上游配置优先用 `AGENTHUB_MCP_UPSTREAMS_FILE`，不要把凭据长期放在命令行历史里。

### 8. 生产部署

- 多实例至少两个，开启 `AGENTHUB_HA_ENABLED=true`；
- 数据库和 `/data` 卷定期备份；
- 单实例并发从 2–4 开始，观察 pi 进程内存和模型限流；
- 先做一次“备份 → 恢复 → 验证案件/报告”演练；
- 升级 pi 前跑完整 `go test -race ./...` 和一次真实告警回归。

### 9. 不要做的事

- 不要用 AI 评分替代真实值班验收；
- 不要让模型无审批执行写操作；
- 不要把“查无数据”当成“服务正常”；
- 不要把所有工具塞进一个无策略的 MCP；
- 不要用 20 条合成事件宣称生产就绪。

## 贡献

欢迎 Issue 和 PR。提交前请跑 `gofmt`、`go test ./...`、`cd web && npm test`；
不要提交密钥、业务技能或客户数据。详见 [CONTRIBUTING.md](CONTRIBUTING.md)。

安全问题请走私密渠道，见 [SECURITY.md](SECURITY.md)。

## License

[MIT](LICENSE) © 2026 dongdonglog

## 致谢

- [pi](https://github.com/earendil-works/pi)：agent 运行时；
- [AA](https://github.com/dongdonglog/aa)：同源的飞书/钉钉 agent 实践；
- [Lark OpenAPI SDK](https://github.com/larksuite/oapi-sdk-go)、
  [DingTalk Stream SDK](https://github.com/open-dingtalk/dingtalk-stream-sdk-go)。
