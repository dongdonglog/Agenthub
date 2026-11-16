# MCP 推荐配置

MCP 通过 stdio 启动，配置保存到管理台或 `data/mcp.yaml`。以下服务需要 Node/Python 运行时和对应凭据；镜像已内置 `node`、`npx`、`python3`，但**不会自动启用**，避免无意中把外部网络和密钥暴露给所有会话。

## 文件与浏览器

```yaml
- name: filesystem
  transport: stdio
  command: npx
  args: ["-y", "@modelcontextprotocol/server-filesystem", "/data"]
  enabled: true

- name: playwright
  transport: stdio
  command: npx
  args: ["-y", "@playwright/mcp@latest", "--headless"]
  enabled: true
```

Playwright 需要 Chromium；镜像未内置浏览器，首次使用需在可写目录安装，或改用部署方提供的浏览器 MCP。

## 搜索与网页

```yaml
- name: fetch
  transport: stdio
  command: python3
  args: ["-m", "mcp_server_fetch"]
  enabled: true

- name: brave-search
  transport: stdio
  command: npx
  args: ["-y", "@modelcontextprotocol/server-brave-search"]
  enabled: true
```

`brave-search` 通过 `BRAVE_API_KEY` 环境变量认证；不要把 Key 写进技能或仓库。

## GitHub / GitLab

```yaml
- name: github
  transport: stdio
  command: npx
  args: ["-y", "@modelcontextprotocol/server-github"]
  enabled: true

- name: gitlab
  transport: stdio
  command: npx
  args: ["-y", "@modelcontextprotocol/server-gitlab"]
  enabled: true
```

需要 `GITHUB_PERSONAL_ACCESS_TOKEN` 或 `GITLAB_PERSONAL_ACCESS_TOKEN`；建议使用只读、最小权限的专用 token。

## 数据与记忆

```yaml
- name: postgres
  transport: stdio
  command: npx
  args: ["-y", "@modelcontextprotocol/server-postgres", "postgresql://user:pass@host/db"]
  enabled: false

- name: memory
  transport: stdio
  command: npx
  args: ["-y", "@modelcontextprotocol/server-memory"]
  enabled: true
```

数据库 URL 含密码，务必只放在受控环境变量/密钥卷，不要提交到仓库。
