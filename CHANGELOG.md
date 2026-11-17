# Changelog

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)；
版本号遵循 [Semantic Versioning](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

- pi 原生 SRE 包：guard（含证据门禁）/redact/compaction/subagent 扩展、sre/tech-support skills、diagnose/handoff/postmortem prompts；
- Tempo trace 查询、K8s 只读工具与受控 scale/restart 真实环境验证；员工级工具授权与知识团队隔离；PostgreSQL advisory lock 多实例单活入口；管理台 admin/operator/view RBAC；审批与受控 K8s scale/restart 动作；技术支持工单入口与 tech-support 员工；案件反馈与员工绩效；Tempo trace 与 K8s 只读工具；数字员工/统一案件/工作台/交接/复盘草稿；服务目录；Prometheus/Loki 只读 MCP 自动注册并接入 pi；
- AIOps：Alertmanager webhook、事故/告警事件/时间线、只读诊断、管理台事故页；
- 镜像工具链：git/git-lfs/gh/glab/ssh/jq/yq/rg/fd/tree/sqlite3/zip 等；pi 增加 `edit`/`write` 工具；
- 投递租约：`claimed/calling` 阶段、租约令牌、过期恢复扫描；结果不明标记 `ambiguous` 并停止自动重试；
- 队列 TTL：过期排队消息跳过并提示重新发送；
- 跨会话共享出站 QPS 令牌桶；
- 钉钉群内免用户白名单 `DINGTALK_GROUP_OPEN`；
- 飞书管理员引用机器人消息干预他人会话；
- Profile 真正裁剪技能/MCP；Skill 可执行文件进入会话 PATH；
- 群聊按群统一轮次编号；钉钉合并转发/富文本解析；
- 历史会话执行轨迹；管理台投递人工重试；批次发送者身份；进度卡展示工具阶段；
- 钉钉入站文件/图片下载；出站文件/图片通过 sessionWebhook 交付；
- 飞书消息状态表情投影（queued/processing/completed/failed/interrupted）；
- 管理台“应用当前配置”：同步工作区并把 Profile 绑定刷新到已有会话代；
- 产物交付：工作区 `submit/` → 文件/图片回传；多文档 ZIP（UTF-8 文件名）；
- 单文件/数量/总量上限与超限说明；飞书文件与图片上传；
- 管理台运行详情查看、下载产物。

## [0.4.0] - 2026-11-13

### Added

- 多供应商模型主备：`providers` / `models` / `routes` 三级配置，有序候选、重试、
  熔断、vision 能力路由与候选健康面板；
- 内置回环 OpenAI 兼容模型路由，pi 指向 `127.0.0.1` 随机端口，无需外部网关；
- 管理台 React + TypeScript + Vite 重构，浅色/深色主题；
- 会话类型（群聊/私聊/定时任务）、搜索、筛选与 ID 复制；
- 路由指标 `model_router_*`。

### Changed

- 旧版单供应商模型配置启动时自动迁移为 v2 线路；
- 定时任务会话不再混入用户会话列表，显示任务名。

### Fixed

- 容器 `HEALTHCHECK` 误启完整实例并覆盖 `models.json` 路由端口；
- 工作区复制使用硬链接导致技能/配置被截断为 0 字节；
- MCP 在 pi 下不可见（exposure、相对路径、保存后未重载）；
- 扁平技能无法编辑、归档后从列表消失；
- 定时任务「立即运行」不更新状态与上次运行时间。

## [0.3.0] - 2026-11-13

### Added

- 管理台 React 版本；
- 会话语义：kind、搜索、筛选、内部会话默认隐藏。

## [0.2.0] - 2026-11-11

### Added

- pi 运行时（RPC、会话文件 v3、工作区、附件）；
- 飞书卡片与停止按钮、定时任务、调度公平队列；
- 管理台 Skill / MCP / 审计 / 队列；
- Docker 多架构镜像与 tag 自动发布。

[Unreleased]: https://github.com/dongdonglog/Agenthub/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/dongdonglog/Agenthub/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/dongdonglog/Agenthub/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/dongdonglog/Agenthub/releases/tag/v0.2.0
