# 工具链

镜像在 Node 22 + pi 1.4.0 基础上内置常用 CLI，Agent 通过 `bash` 直接调用；不需要额外安装。

## 文件与文本

| 工具 | 用途 |
|---|---|
| `rg` | 快速内容搜索 |
| `fd` / `fdfind` | 快速文件查找 |
| `tree` | 目录树 |
| `file` | 文件类型识别 |
| `less` | 分页查看 |
| `xxd` | 十六进制查看 |
| `jq` / `yq` | JSON / YAML 处理 |
| `zip` / `unzip` | 压缩与解压 |
| `sqlite3` | 本地 SQLite 查询 |

## 开发与协作

| 工具 | 用途 |
|---|---|
| `git` / `git-lfs` | 版本控制与大文件 |
| `gh` | GitHub CLI |
| `glab` | GitLab CLI（1.128.0） |
| `openssh-client` | SSH 远程执行 |
| `curl` / `wget` | HTTP 请求 |
| `node` / `npm` / `npx` | Node 工具链 |
| `python3` / `pip3` | Python 工具链 |

pi 自身暴露的工具为：

```text
read,bash,edit,write,grep,find,ls,tool_search
```

其中 `edit` / `write` 直接修改工作区文件，`bash` 可调用上面的 CLI；MCP 工具通过管理台配置。

## 认证与可写目录

容器根文件系统只读，`/data` 是可写卷，工具配置已经指向该卷：

| 变量 | 默认 | 用途 |
|---|---|---|
| `HOME` | `/data/home` | 通用用户配置 |
| `GH_CONFIG_DIR` | `/data/gh` | `gh auth login` |
| `GLAB_CONFIG_DIR` | `/data/glab` | `glab auth login` |
| `XDG_CONFIG_HOME` | `/data/home/.config` | 其他 CLI 配置 |
| `GIT_CONFIG_GLOBAL` | `/data/home/.gitconfig` | Git 全局配置 |

示例：

```bash
docker exec -it agenthub-app-1 gh auth login
docker exec -it agenthub-app-1 glab auth login --hostname gitlab.example.com
```

SSH 密钥放在 `/data/home/.ssh`，不要把宿主机个人 `~/.ssh` 整体挂进容器；生产环境应使用专用只读密钥卷。

## 安全边界

- 容器以 `node` 非 root 运行，根文件系统只读，`/data` 与 `/tmp` 可写；
- 不挂载 Docker socket、业务仓库或宿主机个人配置目录；
- `bash` 能访问容器网络；需要出站白名单时由部署层网络策略限制；
- 工具会继承容器环境变量，接入凭据应只放容器环境或 `/data` 卷，不要写入技能/日志。

## 推荐 MCP

常用 MCP 配方（filesystem、playwright、fetch、brave-search、github、gitlab、postgres、memory）见
[`examples/mcp/README.md`](../examples/mcp/README.md)。

这些服务默认不启用：MCP 会扩大工具与网络边界，必须由部署方按需配置凭据并逐项验证。
