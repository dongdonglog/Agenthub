---
name: agenthub-dev-loop
description: AgentHub 改动后的验证闭环：格式化、vet、Go/前端测试、静态资源入库检查、并发用例。改完代码准备收尾、提交前、或怀疑构建产物过期时使用。
---

# AgentHub 验证闭环

## 何时使用

- 改完任意 Go / React 代码，准备说「做完了」之前
- CI 失败需要本地复现
- 怀疑管理台是旧版（二进制里的 `static` 与 `web/src` 不一致）

## 步骤（按顺序执行，失败即停并报告原始输出）

### 1. 格式

```bash
gofmt -l -w .
gofmt -l .            # 必须无输出
```

### 2. 静态检查与测试

```bash
go vet ./...
go test ./...          # 数据库用例会自动 t.Skip，这是预期的
go build ./...
```
并发相关包额外跑：

```bash
go test -race ./internal/core ./internal/admin ./internal/agent/pi ./internal/store ./internal/mcp
```

### 3. 前端

```bash
cd web && npm test && npm run build
```

### 4. 产物入库（最容易漏）

```bash
git status --short internal/admin/static
```

有变化就必须与源码一起提交。`internal/admin/admin.go` 用 `//go:embed static` 把管理台打进二进制，
`go run ./cmd/agentd` **不会**重建前端；漏提交会让本地和发布版都停在旧界面。

### 5. 数据库用例（有独立测试库时）

```bash
AGENTHUB_TEST_DATABASE_URL='postgres://postgres:pass@127.0.0.1:5432/agenthub_test?sslmode=disable' \
  go test -p 1 ./internal/core ./internal/store ./internal/aiops ./internal/support ./internal/ha -count=1
```

- 只能指向独立测试库；指向 `postgres` 库会 `t.Skip`。
- 多包共用一个库必须 `-p 1`。

## 收尾判定

全部满足才算完成：

1. `gofmt -l .` 无输出；
2. `go vet ./...`、`go test ./...`、`go build ./...` 通过；
3. `cd web && npm test && npm run build` 通过；
4. `internal/admin/static` 与 `web/src` 同步（或本次没改前端）；
5. 改过并发代码时 `-race` 通过。

## 报告格式

用表格列出：检查项 / 命令 / 结果。失败时贴原始错误，不要概括成「有个小问题」。
