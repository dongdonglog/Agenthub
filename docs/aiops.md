# 运维告警与 AIOps 升级方案

AgentHub 的定位不是替换 Prometheus/Grafana，而是做**事故入口与诊断协作层**：
告警进来 → 归一/去重/聚合 → 建事故 → 拉起只读诊断 → 在飞书里给出证据、根因候选和建议动作。

## 1. 业界最佳实践

### 1.1 告警质量优先

AI 不能修复垃圾告警。上线 AIOps 前先做：

- 以 SLO / 错误预算燃烧率为主，而不是堆阈值告警；
- 每条告警必须有 `severity`、`service`、`owner`、`runbook_url`；
- Alertmanager 做 `group_by`、`group_wait`、`group_interval`、`repeat_interval` 和 inhibition；
- 区分 `critical`（要人立即处理）与 `warning`（工单/次日处理）；
- 每月复盘告警量、误报率、可执行率；目标是把噪声降下来，而不是让 AI 解释噪声。

### 1.2 分层架构

```text
Prometheus / Grafana / Cloud / APM
        │  Alertmanager webhook
        ▼
归一化 → 指纹/去重 → 事故聚合 → 富化（服务目录/变更/历史事故/运行手册）
        │
        ▼
只读诊断 agent（指标、日志、链路、K8s、Git、CMDB）
        │
        ├─ 证据与根因候选 → 飞书事故群/时间线
        ├─ 建议动作 → 人工审批
        └─ 受控执行（可选）→ 验证/回滚
```

### 1.3 诊断必须“有证据”

- 只读工具优先：PromQL、LogQL、trace 查询、K8s describe/logs、Git diff、部署记录；
- 每个结论标注证据来源和时间窗；
- 输出按“影响面 → 证据 → 根因候选（概率）→ 建议动作（是否需审批）”组织；
- 明确说“不确定”，禁止编造不存在的指标/日志；
- 运行手册作为技能/RAG 注入，不让模型自由发挥。

### 1.4 动作必须有人把关

- 默认只读；写操作走审批，记录操作者、目标、回滚方案；
- 高风险动作（重启、扩容、回滚、改配置）需要二次确认和爆炸半径检查；
- 用策略引擎限制命名空间、服务、时间窗；
- 所有动作写入事故时间线和审计。

### 1.5 可观测性与评估

- 指标：告警接收量、去重量、事故数、MTTA、MTTR、诊断耗时、token 成本、误报率、人工采纳率；
- 离线评估集：历史事故 + 标准根因 + 期望证据；
- 在线反馈：值班人“有用/无用/根因正确”按钮，进入下一轮提示词和 runbook；
- 定期回放，不让模型版本变化悄悄降低质量。

## 2. AgentHub 当前实现

### 2.1 告警入口

`POST /api/v1/alerts/alertmanager`，兼容 Alertmanager webhook v4。

```bash
curl -X POST http://127.0.0.1:8787/api/v1/alerts/alertmanager \
  -H "Authorization: Bearer $AGENTHUB_AIOPS_TOKEN" \
  -H "Content-Type: application/json" \
  --data @alert.json
```

配置：

```bash
AGENTHUB_AIOPS_ENABLED=true
AGENTHUB_AIOPS_TOKEN=换成随机长 token
AGENTHUB_AIOPS_CHAT_ID=oc_运维群
AGENTHUB_AIOPS_RUNBOOK_DIR=/data/runbooks
AGENTHUB_AIOPS_MAX_ALERTS=20
```

### 2.2 事故模型

- `incident`：按 Alertmanager `groupKey` 聚合，含状态、级别、服务、环境、告警数、认领人；
- `alert_event`：单条告警原始标签/注解，按 `fingerprint + status + startsAt` 去重；
- `incident_event`：时间线（告警、认领、恢复、人工操作）；
- 管理台「运维事故」可查看列表、时间线、认领。

### 2.3 诊断运行

首次出现 firing 事故时，把告警摘要 + 公共标签 + 运行手册（`<runbook_dir>/<alertname>.md` 或 `<service>.md`）作为一条消息投给 agent。重复 webhook 只更新时间线，不重复拉起诊断。

### 2.4 服务目录

`services.yaml` 把告警服务映射到 owner/team/runbook/dashboard/repo/namespace：

```yaml
services:
  checkout-api:
    owner: checkout-team
    team: payments
    runbook: https://wiki.example.com/runbooks/checkout
    dashboard: https://grafana.example.com/d/checkout
    repo: git@gitlab.example.com:payments/checkout.git
    namespace: prod
```

诊断 prompt 会自动带上这些上下文。

### 2.5 运行手册

建议按告警名或服务名放 Markdown：

```text
/data/runbooks/HighErrorRate.md
/data/runbooks/checkout-api.md
/data/runbooks/default.md
```

内容至少包含：影响面、快速检查、常见根因、回滚/降级、升级路径、禁止动作。

### 2.6 数字员工与工作台

- 默认员工 `sre-oncall`：SRE 值班员；
- Alertmanager 事故自动映射为统一案件 `case_item`；
- 案件时间线 `case_event`；
- 管理台「工作台」：员工、案件、时间线、生成交接、关闭案件、生成复盘草稿；
- 案件反馈：有用/无用 + CSAT 1–5；员工绩效：案件数、解决数、采纳数、平均解决时长、CSAT；
- SLA：critical/P1 1h、high/P2 4h、medium/P3 24h、其他 72h；工作台显示截止时间和超时标记；
- 每分钟扫描超时未结案件，自动升级为 `waiting_human` 并写时间线；
- 员工级工具授权：`employee.allowed_tools`，SRE/技术支持默认只授予 `read/bash/grep/find/ls/tool_search`；`PUT /admin/api/v1/employees/{id}` 可调整；
- 服务目录 `services.yaml`：owner/team/runbook/dashboard/repo/namespace，自动注入诊断 prompt。

### 2.7 pi 原生能力和只读约束

`pi-package/` 随镜像加载：

- `extensions/agenthub-guard.ts`：`tool_call` 拦截 `edit/write` 和高风险 bash；`before_agent_start` 注入只读诊断约束；
- `extensions/agenthub-redact.ts`：`tool_result` 脱敏 token/密码/私钥并截断超大输出；
- `extensions/agenthub-compaction.ts`：事故上下文压缩保留影响面、证据、假设和未闭环动作；
- `extensions/agenthub-subagent.ts`：`ops_investigate` 并行启动 metrics/logs/changes/traces 只读调查，每个子调查独立上下文；
- guard 的 `agent_before_settle`：只读诊断未调用任何观测工具时，最多追加一次“先查证据”的提示，避免纯猜测结论。
- `skills/sre-oncall`：SRE 诊断流程；
- `skills/tech-support`：技术支持流程；
- `prompts/diagnose.md`、`handoff.md`、`postmortem.md`。

AIOps 诊断消息带 `ReadOnly=true`，pi 进程收到 `AGENTHUB_READONLY=1`，从工具层阻断写操作。

### 2.8 只读观测 MCP

`agenthub-ops-mcp` 随镜像构建，配置 Prometheus/Loki 地址后自动注册给 pi：

- `prom_query`、`prom_query_range`、`prom_alerts`；
- `loki_query`、`loki_labels`、`loki_label_values`；
- `tempo_search`、`tempo_trace`；
- `k8s_get`、`k8s_logs`、`k8s_events`（资源/命名空间 allowlist，只读 kubectl）；
- 只读 GET、超时 20s、响应截断、认证走环境变量。

配置：

```bash
AGENTHUB_PROMETHEUS_URL=http://prometheus:9090
AGENTHUB_PROMETHEUS_TOKEN=
AGENTHUB_LOKI_URL=http://loki:3100
AGENTHUB_LOKI_TOKEN=
```

没有配置 URL 时不会注册 ops MCP，避免给模型空工具。

### 2.9 技术支持工单入口

`POST /api/v1/tickets`：

```json
{"id":"T-1","title":"无法登录","description":"登录 500","requester":"ou_1","priority":"P1","service":"account","environment":"prod"}
```

- 鉴权：`Authorization: Bearer <AGENTHUB_SUPPORT_TOKEN>`；
- 自动创建 `tech-support` 员工案件；
- 从 `<data>/knowledge/*.md` 检索知识库并注入 prompt（带来源）；
- 按 `<data>/knowledge/<team>/` 做团队隔离，全局文档始终可见；
- 只读拉起技术支持诊断；
- 管理台工作台统一查看。

### 2.10 验证样例

2026-11-17 用真实模型 + 本地 fake Prometheus/Loki HTTP 服务跑通：

- Alertmanager webhook → 案件 → `run_4`；
- 运行状态 `completed`，输入约 33k tokens，输出约 1.7k tokens；
- 诊断输出包含：影响面、证据表、根因候选、建议动作；
- 工具查询到 `up{job="checkout"}=0` 和 `checkout 500` 日志，并指出时间窗/指标不匹配；
- 全程只读，未执行变更。

这是第一个“真实模型 + 告警 + 案件 + 观测工具”的端到端诊断样例。

随后用真实 Prometheus 2.54 + Loki 3.2 容器复跑：

- `run_2` completed，输入约 30k tokens，输出约 1.6k tokens；
- 真实查到 Loki 的 `checkout 500 path=/pay` 日志；
- 真实查到 Prometheus 当前告警为空、业务指标缺失，并在诊断中明确区分“观测缺口”和“业务恢复”；
- 未执行任何变更。

真实 Prom/Loki 已跑通；K8s 已在本地 minikube 验证：

- `k8s_get` 读取 `agenthub-test` Pod；
- `k8s_logs` 读取 nginx 日志；
- 受控动作 `k8s_scale` 把副本从 1 扩到 2；
- `k8s_restart` 触发滚动重启；
- 全部经 kubectl，只读/动作边界和审批由 AgentHub 控制。

Tempo 已在本地 2.6 容器验证：

- OTLP HTTP 推送 trace；
- `tempo_search` 返回 `checkout` 的 `GET /pay`；
- `tempo_trace` 返回完整 span；
- 查询链路与只读边界正常。

### 2.11 批量/负载验证

2026-11-17 用桩模型对同一事故群发送 20 条唯一告警：

- 20 个案件全部创建；
- 引擎按同会话批次合并为 3 个 run（1 + 11 + 8 输入）；
- 3 个 run 全部 completed；
- 无失败、无重复案件；
- 这是批量语义和负载验证，不是 20 个真实生产事件。

### 2.12 审批与受控动作

- 案件详情可创建 `k8s_scale` / `k8s_restart` 审批；创建后向案件群发送飞书审批卡片（需 `CARD_ACTION_ENABLED` 和 `ADMIN_USERS`）；
- 飞书卡片批准/拒绝与工作台共用审批状态机；
- 审批通过后由 `internal/action` 执行；
- namespace allowlist（`AGENTHUB_K8S_NAMESPACES`）、replicas 1–20、只支持 scale/restart；
- 所有动作写审计和案件时间线；
- 未配置 `AGENTHUB_K8S_ENABLED` 时只记录审批，不执行。

2026-11-17 在 minikube 上验证了完整链路：

- 案件 → `POST /cases/{id}/approvals`；
- 审批人 `POST /approvals/{id}/approve`；
- 执行器把 `agenthub-test/nginx` 从 2 副本扩到 3；
- 审批状态 `approved`，结果写入案件时间线。

## 3. 距离生产级 AIOps 还缺什么

当前是**可用的 SRE 数字员工 MVP**，不是自动处置平台：

1. **Tempo/K8s/Git/Argo 只读工具**：Prometheus/Loki 已接入，链路、K8s、变更还没；
2. **告警相关性**：同一根因的多条告警仍需聚类/抑制，而不是每个 group 都开案；
3. **评估闭环**：缺历史事故回放、人工反馈和准确率看板；
4. **审批与动作执行**：目前只建议动作，不执行写操作；
5. **多租户/权限**：按团队隔离案件和工具权限；
6. **HA**：飞书长连接仍是单实例入口。

## 4. 分阶段路线

### P0 告警接入与只读诊断（当前）

- Alertmanager webhook、去重、事故、时间线；
- 飞书事故群诊断；
- 运行手册注入；
- 管理台事故列表/认领。

### P1 观测工具接入

- Prometheus/Loki/Tempo MCP 或只读 HTTP 工具；
- K8s 只读（get/describe/logs/events）；
- Git/Argo/CI 变更查询；
- 服务目录与 owner 映射；
- 诊断模板按服务/告警类型分派。

### P2 相关性 + 评估

- 告警聚类、抑制、根因候选排序；
- 历史事故回放集；
- 人工反馈（根因正确/有用/无用）；
- MTTA/MTTR/成本看板。

### P3 受控处置

- 审批流（飞书卡片/管理台）；
- 受控动作：扩容、重启、回滚、静默、工单；
- 爆炸半径与回滚验证；
- 所有动作审计。

## 5. 选型建议

| 能力 | 推荐 |
|---|---|
| 指标/告警 | Prometheus + Alertmanager |
| 可视化 | Grafana |
| 日志 | Loki（或 ELK） |
| 链路 | Tempo / Jaeger / OpenTelemetry |
| 值班/升级 | PagerDuty / Opsgenie / Grafana OnCall / 飞书值班 |
| 事故协作 | 飞书群 + AgentHub 事故时间线 |
| AIOps 平台 | 先自建“入口 + 诊断”，不要一上来买大而全平台 |
| 动作执行 | Ansible / Argo CD / K8s Job，必须有审批和回滚 |

## 6. 安全边界

- Webhook token 必须随机、只走内网/TLS；
- 诊断工具只读，禁止 `kubectl delete/apply`、数据库写、生产 SSH 写操作；
- 告警标签/日志可能含用户数据，进入模型前脱敏；
- 运行手册和事故时间线按团队隔离；
- 模型输出不得直接当命令执行。
