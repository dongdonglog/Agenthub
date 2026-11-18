# AgentHub

**飞书 / 钉钉里的 AI SRE 数字同事。**

AgentHub 把告警接成案件，让 [pi](https://github.com/earendil-works/pi) 在受控工作区里查指标、日志、trace 和 K8s，产出一份带证据的处置报告；写操作必须审批，结果送回群。

[![CI](https://github.com/dongdonglog/Agenthub/actions/workflows/ci.yml/badge.svg)](https://github.com/dongdonglog/Agenthub/actions/workflows/ci.yml)
[![Release](https://github.com/dongdonglog/Agenthub/actions/workflows/release.yml/badge.svg)](https://github.com/dongdonglog/Agenthub/actions/workflows/release.yml)
[![Image](https://img.shields.io/badge/image-donglog%2Fagenthub-2496ED?logo=docker)](https://hub.docker.com/r/donglog/agenthub)
[![Go](https://img.shields.io/badge/go-1.23-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

> **English**: AgentHub is a self-hosted AI SRE teammate for Feishu and DingTalk.
> It turns alerts into evidence-backed cases and incident reports, with
> human-approved actions, one Go binary and PostgreSQL.

![工作台](docs/images/admin-workbench.png)

> 截图来自开发版，实际界面以当前版本为准。

## 它解决什么

告警本身不是问题，问题是值班人要在几个系统之间来回跳：

- 去 Prometheus 查影响面；
- 去 Loki 查错误；
- 去 Tempo 查链路；
- 去 K8s 查 Pod；
- 最后还要写一段能交接的结论。

AgentHub 把这些动作收进一个案件：

```text
告警 → 案件 → 证据 → 诊断 → 处置报告 → 审批动作 → 飞书
```

### 它不是什么

- **不是 AIOps 平台**：不替代 Prometheus、Grafana、Loki、PagerDuty、ITSM；
- **不是无人运维**：默认只读，写操作必须审批；
- **不是通用 Agent 平台**：只做 SRE / 技术支持这一个垂直场景。

## 核心能力

| 能力 | 说明 |
|---|---|
| **告警接入** | Alertmanager、Grafana、通用 webhook、PagerDuty；统一事件模型、去重、恢复、按来源 token 与限流 |
| **pi 诊断** | skills、prompts、extensions、subagent、compaction；Prometheus/Loki/Tempo/K8s 通过 MCP 查证据 |
| **处置报告** | pi 通过 `submit_report` 提交结构化报告；Go 保存草稿，人补根因/验证后定稿、关闭、发飞书 |
| **审批动作** | pi 通过 `request_approval` 申请 K8s scale/restart；Go 校验并推审批卡片，批准后由受限执行器执行 |
| **案件闭环** | 告警聚合、时间线、SLA、交接、复盘、手动恢复；案件/报告支持时间筛选 |
| **MCP 工具网关** | 一个 `agenthub-ops-mcp` 聚合内置工具与外部 MCP；前缀、只读策略、allow/deny、审计、超时隔离 |
| **可治理** | 只读 guard、tool_result 脱敏、审批、admin/operator/view RBAC、审计、HA 选主、备份恢复 |

## 一次告警怎么走

1. Alertmanager 把告警推到 `POST /api/v1/alerts/alertmanager`；
2. AgentHub 归一化、去重、聚合告警并创建案件；
3. pi 带着案件上下文、SRE skill 和 MCP 工具启动；
4. pi 查 Prometheus/Loki/Tempo/K8s，输出诊断；
5. pi 调 `submit_report` 提交报告草稿；需要变更时调 `request_approval` 申请动作；
6. 人在案件页补根因、验证，定稿后关闭案件；动作申请经审批后才由受限执行器执行；
7. 报告可以下载 Markdown，也可以发回飞书群。

## 快速开始

前置：Docker 24+、Docker Compose v2、一个飞书企业自建应用。

```bash
git clone https://github.com/dongdonglog/Agenthub.git
cd Agenthub
cp .env.example .env
```

至少填：

```dotenv
AGENTHUB_CHANNEL=feishu
POSTGRES_PASSWORD=change-me
FEISHU_APP_ID=cli_xxx
FEISHU_APP_SECRET=xxx
FEISHU_BOT_OPEN_ID=ou_xxx
AGENTHUB_AGENT_API_KEY=sk-xxx
```

启动：

```bash
docker compose up -d
curl -s 127.0.0.1:8787/healthz
```

管理台默认只监听回环：

```text
http://127.0.0.1:8788/admin/
```

远端服务器用 SSH 隧道：

```bash
ssh -L 8788:127.0.0.1:8788 user@your-server
```

### 接入 AIOps

```dotenv
AGENTHUB_AIOPS_ENABLED=true
AGENTHUB_AIOPS_TOKEN=change-me
AGENTHUB_AIOPS_CHAT_ID=oc_xxx
AGENTHUB_PROMETHEUS_URL=http://prometheus:9090
AGENTHUB_LOKI_URL=http://loki:3100
```

Alertmanager webhook：

```text
http://<agenthub>:8787/api/v1/alerts/alertmanager
```

完整飞书配置见 [部署指南](docs/deploy.md)；环境变量见 [运维手册](docs/runbook.md)。

## 架构

```mermaid
flowchart LR
  U[飞书 / 钉钉] --> C[通道适配]
  A[Alertmanager / Grafana / PagerDuty] --> C
  C --> E[Engine<br/>去重 · 批次 · 会话代]
  E <--> DB[(PostgreSQL)]
  E --> R[pi 运行时]
  R --> G[MCP 工具网关]
  G --> OBS[Prometheus / Loki / Tempo / K8s]
  R --> MR[模型路由]
  R --> REP[submit_report]
  REP --> DB
  E --> O[Outbox / 投递]
  O --> U
  ADM[管理台 / API] --> E
```

关键不变量：

- 同一会话同时最多一个运行；
- 批次创建后输入冻结；
- 先落库再 ACK，投递使用幂等键；
- 卡片/表情投影失败不影响业务状态；
- `/reset` 递增会话代，旧代历史不进入提示词。

## 管理台

React + TypeScript + Vite，构建产物嵌入 Go 二进制；浅色/深色切换。

| 页面 | 内容 |
|---|---|
| 工作台 | 处理中案件、等待人工、firing 告警组、报告数量 |
| 案件 | 状态/类型/时间/关键词筛选；告警聚合、诊断、证据、审批、报告 |
| 报告 | pi 自动草稿，人工定稿、下载、发飞书 |
| 系统 | 运行概览、会话、Skill、MCP、定时任务、审计、设置 |

![案件详情](docs/images/admin-case.png)

![处置报告](docs/images/admin-report.png)

## 扩展方式

AgentHub 的分工是：

> **pi 负责“想”和“写”，Go 负责“记住”和“送出去”。**

| 想加什么 | 放哪里 |
|---|---|
| 新 SOP、排查步骤、领域知识 | `pi-package/skills/<name>/SKILL.md` |
| 新输出格式 | `pi-package/prompts/<name>.md` |
| 新只读数据源 | 新增 MCP，挂到 `AGENTHUB_MCP_UPSTREAMS` |
| 安全 / 上下文钩子 | pi extension |
| 新告警源、案件字段、页面、权限 | Go / React |

详细规则见 [最佳实践](docs/best-practices.md)。

## 生产边界

当前主线是 **`v0.4.0` 之后的开发版**，适合内网或受控环境的小团队试用；生产发布以 GitHub Releases 为准。

- 默认只读，写操作审批；K8s 动作仅支持 allowlist 内的 scale/restart；
- 飞书/钉钉长连接由 PostgreSQL advisory lock 选主，单活入口；会话执行另有数据库租约，多实例不会重复跑同一会话；
- `AGENTHUB_ENV=production` 必须配置管理面令牌，否则拒绝启动；
- 钉钉回复依赖 `sessionWebhook`，卡片未接入；
- 没有多租户、SLO/错误预算、生产长跑/压测/灾备；
- 已跑通真实 Alertmanager + Prometheus/Loki + 真实模型的 20 条独立告警，但仍是合成事件，不等于 20 个生产事故。

## 不接飞书先看效果

不需要飞书应用、也不需要模型密钥：

```bash
./scripts/demo.sh                                        # 起一个本地实例
./scripts/verify.sh http://127.0.0.1:20901 http://127.0.0.1:20902   # 推一条告警
```

没有模型凭据时会自动改用回显执行器，链路照常跑通，管理台在
`http://127.0.0.1:20902/admin/`。

## 文档

- [最佳实践](docs/best-practices.md)
- [HTTP 接口（OpenAPI）](docs/openapi.yaml)
- [AIOps / 数字员工](docs/aiops.md)
- [工具链与 MCP 网关](docs/tools.md)
- [部署指南](docs/deploy.md)
- [架构设计](docs/architecture.md)
- [运维手册](docs/runbook.md)
- [飞书能力实测](docs/feishu-capabilities.md)
- [CHANGELOG](CHANGELOG.md)

## 开发

需要 Go 1.23+、Node 22+、Docker（集成测试可选）。

```bash
make web
go run ./cmd/agentd
go test ./...
cd web && npm test
```

数据库集成测试：

```bash
AGENTHUB_TEST_DATABASE_URL='postgres://postgres:agenthub@127.0.0.1:55433/agenthub_test?sslmode=disable' \
  go test -p 1 ./internal/core ./internal/store
```

## 贡献

欢迎 Issue 和 PR。提交前请跑 `gofmt`、`go test ./...`、`cd web && npm test`；不要提交密钥、业务技能或客户数据。详见 [CONTRIBUTING.md](CONTRIBUTING.md)。

安全问题请走私密渠道，见 [SECURITY.md](SECURITY.md)。

## License

[MIT](LICENSE) © 2026 dongdonglog

## 致谢

- [pi](https://github.com/earendil-works/pi)：agent 运行时；
- [AA](https://github.com/dongdonglog/aa)：同源的飞书/钉钉 agent 实践；
- [Lark OpenAPI SDK](https://github.com/larksuite/oapi-sdk-go)、[DingTalk Stream SDK](https://github.com/open-dingtalk/dingtalk-stream-sdk-go)。
