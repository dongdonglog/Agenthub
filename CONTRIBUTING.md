# Contributing

感谢参与 AgentHub。提交前请先确认你的改动符合下面的约定。

## 开发环境

- Go 1.23+
- Node.js 22+（仅管理台）
- Docker 与 Docker Compose（可选，用于集成测试）

```bash
make web              # 构建管理台
go run ./cmd/agentd   # 本地启动
go test ./...         # 后端测试
cd web && npm test    # 前端测试
```

## 提交流程

1. 先开 Issue 描述问题或方案，避免大改后才发现方向不一致；
2. 从 `main` 切分支：`feat/...`、`fix/...`、`docs/...`；
3. 保持改动聚焦，一个 PR 只做一件事；
4. 提交前运行：

```bash
gofmt -l -w .
go test ./...
cd web && npm test && npm run build
```

5. PR 描述写清「改了什么、为什么、怎么验证」，关联 Issue。

## 代码约定

- Go 代码遵循 `gofmt`，导出符号写注释；
- 前端在 `web/`，TypeScript，组件与页面分开；
- 新增行为需要测试；修复 bug 优先补回归测试；
- 数据库迁移只前进，不修改已发布迁移；
- 不提交密钥、真实客户数据、业务技能包。

## 文档

- 用户可见行为变化同步更新 README 或 `docs/`；
- 配置项以 `docs/runbook.md` 为准，部署步骤以 `docs/deploy.md` 为准。

## 安全问题

不要公开提交安全 Issue，见 [SECURITY.md](SECURITY.md)。
