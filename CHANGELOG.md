# Changelog

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)；
版本号遵循 [Semantic Versioning](https://semver.org/lang/zh-CN/)。

## [Unreleased]

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
