# AgentHub

**把 AI 同事放进飞书和钉钉：能查、能做、有过程、可治理。**

AgentHub 是一个自托管的团队 AI 助手服务。消息从飞书群、钉钉群或私聊进来，
交给 [pi](https://github.com/earendil-works/pi) 在独立工作区里执行，再把结果、过程和文件送回会话；
技能、MCP、模型、定时任务、审计都在管理台统一配置。

[![CI](https://github.com/dongdonglog/Agenthub/actions/workflows/ci.yml/badge.svg)](https://github.com/dongdonglog/Agenthub/actions/workflows/ci.yml)
[![Release](https://github.com/dongdonglog/Agenthub/actions/workflows/release.yml/badge.svg)](https://github.com/dongdonglog/Agenthub/actions/workflows/release.yml)
[![Image](https://img.shields.io/badge/image-donglog%2Fagenthub-2496ED?logo=docker)](https://hub.docker.com/r/donglog/agenthub)
[![Go](https://img.shields.io/badge/go-1.23-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

> **English**: AgentHub is a self-hosted AI teammate for Feishu and DingTalk.
> It runs the pi coding agent in per-conversation workspaces, with scheduling,
> skills, MCP tools, model failover, an admin console, and audit — one Go binary
> plus PostgreSQL. MIT licensed.

![管理台](docs/images/admin-overview.png)

## 为什么是 AgentHub

企业里的 AI 常常停在演示阶段，卡在三件事：

- **进不了工作流**：要开网页、复制粘贴，同事不用；
- **看不到过程**：只有一个转圈，出错不知道卡在哪；
- **没法治理**：技能、工具、模型散在配置文件里。

AgentHub 把入口放进日常 IM，把执行放进可清理的工作区，把配置和审计放进管理台。

## 特性

- **IM 原生**：飞书长连接 / Webhook、钉钉 Stream；支持 @、引用消息、附件；
- **真执行**：pi 在会话代工作区里读写文件、跑 `bash`、搜索、调 MCP；
- **技能可运营**：`SKILL.md` + `references/`，管理台在线编辑、ZIP 导入、停用归档；
- **模型主备**：多供应商、有序候选、超时/429/5xx 自动切换、熔断，图片走 vision 线路；
- **调度**：跨会话并发上限、公平队列 + 等待老化、每会话限流、cron 定时任务；
- **可治理**：管理台 Token、CSRF、审计、健康检查、Prometheus 指标、备份恢复脚本。

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
AGENTHUB_TAG=0.4.0 docker compose pull
AGENTHUB_TAG=0.4.0 docker compose up -d
```

## 架构

```mermaid
flowchart LR
  U[飞书 / 钉钉] --> C[通道适配<br/>解析 · 白名单 · 附件]
  C --> E[Engine<br/>去重 · 批次 · 会话代]
  E <--> DB[(PostgreSQL)]
  E --> R[pi 运行时]
  R --> W[会话工作区<br/>配置 · 附件 · outbox]
  R --> MR[模型路由]
  MR --> P1[主模型]
  MR --> P2[备选模型]
  E --> O[Outbox<br/>重试 · 补偿]
  O --> U
  A[管理台 / API] --> E
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
| 运行概览 | 通道状态、模型、计数、队列、在线试跑 |
| 在线会话 | 用户/群聊/私聊/定时任务筛选与搜索、ID 复制、优先级 |
| 历史会话 | 按会话代聚合，查看消息详情 |
| Skill | 文件树编辑、ZIP 导入、停用/归档 |
| MCP | 新增/编辑，保存后热更新到 pi |
| 定时任务 | cron、启停、立即运行、上次状态 |
| 审计 | 管理面写操作记录 |
| 设置 | 供应商 / 模型 / 线路 / 候选健康 |

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
| `FEISHU_ALLOWED_USERS` / `FEISHU_ALLOWED_GROUPS` | 空 | 白名单；留空表示拒绝所有人 |
| `AGENTHUB_AGENT_BASE_URL` / `AGENTHUB_AGENT_API_KEY` | 空 | 模型网关地址与 Key |
| `AGENTHUB_AGENT_MODEL` | `gpt-4.1-mini` | 默认主模型 |
| `AGENTHUB_ADMIN_TOKEN` | 空 | 管理台访问令牌 |
| `AGENTHUB_MAX_CONCURRENT_RUNS` | `4` | 跨会话并发上限 |
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
- [部署指南](docs/deploy.md)
- [架构设计](docs/architecture.md)
- [运维手册](docs/runbook.md)
- [飞书能力实测](docs/feishu-capabilities.md)
- [与 AA 对比 / v1.0 清单](docs/comparison.md)
- [路线图](docs/roadmap.md)
- [PRD](docs/PRD.md)
- [CHANGELOG](CHANGELOG.md)

## 状态与边界

AgentHub 目前是 **v0.4.x**，适合内网或受控环境的小团队使用。

- **单实例**：飞书/钉钉长连接目前一个应用只跑一个实例；
- **钉钉**：回复依赖 `sessionWebhook`（约 1.5 小时），卡片和附件下载未接入；
- **资源**：镜像约 430MB；每个并发 pi 进程约 100MB，按 `AGENTHUB_MAX_CONCURRENT_RUNS` 估算；
- **成本**：已记录 token，尚未按单价换算金额；
- **pi**：镜像固定 1.4.0、会话文件 v3，升级需要回归；
- **管理台**：没有内建用户体系，建议只在内网或网关后暴露。

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
