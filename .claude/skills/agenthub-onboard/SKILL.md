---
name: agenthub-onboard
description: 接手 AgentHub 时的最短阅读路径与心智模型：先读哪些文件、系统怎么跑、最容易误判的几处。第一次进入本仓库、或需要快速建立全局认识时使用。
---

# AgentHub 快速接手

## 阅读顺序（约 30 分钟）

1. `AGENTS.md` —— 约定、目录地图、不变量、已知坑。**这是唯一权威摘要**。
2. `cmd/agentd/main.go` —— 装配顺序就是系统结构：配置 → 通道 → 模型路由 → pi 配置 →
   Engine → 存储/恢复 → 调度 → AIOps/Support → 管理面 → 后台循环。
3. `internal/core/engine.go` + `run.go` —— 受理、批次、串行、终态。
4. `internal/agent/pi/runtime.go` —— Go 与 pi 的进程边界。
5. `internal/store/journal.go` —— 持久化与投递状态机。
6. `docs/runbook.md` —— 配置与排障口径。

不要从 `docs/architecture.md` 的 ADR 开始：它描述的是早期设计（templ+htmx、River、
自研 Go loop、沙箱容器），与当前实现不符。

## 心智模型

```text
pi = 大脑与手（会读写文件、跑命令、查观测）
Go = 记忆与出口（落库、串行、幂等、审批、投递、审计）
PostgreSQL = 唯一事实源；卡片/表情只是投影
```

三条最容易误判的地方：

1. **`core` 不 import `store`**。持久化靠 `core.Journal` 等接口注入，`store` 反向依赖 `core`。
   想在 `core` 里直接 `store.New...` 就是设计错误。
2. **一次运行 = 一个批次**。运行期间来的消息进下一批；批次输入之后不可变。
3. **pi 是子进程，不是库**。行为经 RPC 事件回传；超时/取消先 `clear_queue` 再 `abort`，
   宽限后杀进程组。调它时看 `runtime.go` 的事件收敛逻辑，不要凭猜。

## 快速定位表

| 现象 | 先看 |
|---|---|
| 消息没回应 | `platform/feishu/dispatch.go`（白名单/@）→ `core.Submit`（去重/限流）→ `runBatch`（TTL） |
| 回复重复 / 丢回复 | `core/deliver.go`（租约、ambiguous）→ `store/journal.go` |
| pi 起不来 | `agent/pi/runtime.go`、`data/pi/agent/` 下 models.json/SYSTEM.md/mcp.json |
| 报告没落到案件 | `submit_report` 扩展 → `reports/report.json` → `core.reportSink` |
| 告警没进案件 | `aiops/http.go`（token/限流）→ `aiops.go`（聚合/去重） |
| 管理台界面旧 | `web/src` 是否 build 到 `internal/admin/static` |
| 定时任务停了 | `store/schedule.go` `ClaimDue`（`next_run_at` 置空后崩溃会静默停摆） |

## 不要做的事

- 不要为了「跑通」给 `core` 加数据库依赖或给 `platform` 加业务逻辑。
- 不要绕过 `Engine.Submit` 直接把消息塞进队列。
- 不要把业务技能（客户、集群、账号信息）提交进仓库。
- 不要在没跑 `go test ./...` 和 `-race` 的情况下声称完成。
