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

### 2.4 运行手册

建议按告警名或服务名放 Markdown：

```text
/data/runbooks/HighErrorRate.md
/data/runbooks/checkout-api.md
/data/runbooks/default.md
```

内容至少包含：影响面、快速检查、常见根因、回滚/降级、升级路径、禁止动作。

## 3. 距离生产级 AIOps 还缺什么

当前是**可用 MVP**，不是自动处置平台：

1. **指标/日志/链路查询工具**：需要把 Prometheus/Loki/Tempo/K8s 以只读 MCP 或 CLI 接入；
2. **服务目录/拓扑/变更**：告警要能自动关联 owner、上下游、最近发布；
3. **告警相关性**：同一根因的多条告警需要聚类/抑制，而不是每个 group 都开事故；
4. **审批与动作执行**：目前只建议动作，不执行写操作；
5. **评估闭环**：缺历史事故回放、人工反馈和准确率看板；
6. **多租户/权限**：按团队隔离事故和工具权限；
7. **HA**：飞书长连接仍是单实例入口。

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
