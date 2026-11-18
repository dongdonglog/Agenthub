---
name: agenthub-change-map
description: 决定一处改动到底该放 pi 侧还是 Go 侧：技能/提示词/MCP/扩展 vs 受理/存储/权限/投递。接到需求但不确定落点时使用。
---

# 改动落点决策

## 一句话判据

> **「知道得更多、查得更多」放 pi；「触发、保存、权限、投递」放 AgentHub（Go）。**

| 需求 | 落点 | 具体做法 |
|---|---|---|
| 新 SOP、排查步骤、领域知识 | pi skill | 新增 `pi-package/skills/<name>/SKILL.md`（随代码）或管理台导入（部署方） |
| 新输出格式 / 报告结构 | pi prompt | `pi-package/prompts/<name>.md`；结构化输出见 `extensions/agenthub-report.ts` |
| 新只读数据源 | MCP | 首选挂到 `AGENTHUB_MCP_UPSTREAMS`；内置工具加在 `cmd/agenthub-ops-mcp` |
| 上下文/安全钩子 | pi extension | `pi-package/extensions/*.ts`，在 `package.json` 的 `pi.extensions` 登记 |
| 新告警来源 | Go | `internal/alerting/adapters.go` + `httpapi` 路由 |
| 案件字段 / 状态 / 页面 | Go + React | `internal/store/case.go`（迁移）→ `internal/admin/admin.go` → `web/src/pages` |
| 新权限、配额、审批策略 | Go | `core`/`admin`；写操作必须走 `approval_request` → `internal/action` |
| 新通道（企微等） | Go | 实现 `internal/channel` 接口，装配在 `cmd/agentd` |
| 新模型/线路 | 配置 | 管理台「模型」或 `settings.yaml`（v2：providers/models/routes） |
| 定时任务 | 已有能力 | 管理台创建，勿新增调度实现 |

## 判断分支

**「pi 能不能自己做到？」** 能 → 写技能/提示词，别改 Go。
**「需要持久化、幂等或跨进程一致？」** 是 → Go + 迁移。
**「需要平台 API（发消息、建卡、下文件）？」** → `internal/platform/*`。
**「只是让模型多看一份数据？」** → MCP，不要往提示词里塞数据。

## 硬约束（违反即返工）

- 新增 pi 能力必须同时考虑只读运行：`AGENTHUB_READONLY=1` 时 guard 会拦 `edit/write` 与破坏性 bash。
- 新增写操作必须：入库 `approval_request` → 审批 → `internal/action` 执行 → 写案件时间线 + 审计。
  仅支持 allowlist 内的 K8s scale/restart；扩类型要同步扩 `action.Execute` 的参数校验。
- 新增配置项：`internal/config/config.go` 的 `defaults()+applyEnv()+Validate()` 三处 +
  `.env.example` + `docs/runbook.md`。
- 新增表：新增迁移文件（不改旧文件）+ 同步相关测试的 `TRUNCATE` 列表。
- 改了 `web/src`：`npm run build`，提交 `internal/admin/static`。

## 反模式

- 把业务知识写进 Go 常量或系统提示词 —— 难维护、难审计。
- 给 pi 直接挂可写 kubectl —— 绕过了审批。
- 让平台适配层（`platform/*`）负责排队/重试 —— 那是 `core` 的职责。
- 为了一个页面去改 `core` 的数据结构 —— 先看 `admin` 能否只读组合出来。
