# 部署指南

## 依赖

- Docker 24+ / Docker Compose v2
- 一台能出网访问飞书与模型网关的机器（出网 443）

## 首次部署

```bash
git clone https://github.com/dongdonglog/Agenthub.git
cd Agenthub
cp .env.example .env
```

编辑 `.env`：

```bash
FEISHU_APP_ID=cli_xxx
FEISHU_APP_SECRET=xxx
FEISHU_BOT_OPEN_ID=ou_xxx
FEISHU_ALLOWED_USERS=ou_xxx           # 留空会拒绝所有人
FEISHU_MENTION_FREE_CHATS=oc_xxx      # 测试群免 @
AGENTHUB_AGENT_API_KEY=sk-xxx
AGENTHUB_AGENT_VISION_MODEL=my-gpt-5.5
AGENTHUB_ADMIN_TOKEN=一段随机串         # 管理台访问令牌
```

模型相关配置（Base URL / 模型 / API Key / 备用与视觉模型）可以在管理台「设置」页修改，
保存后写入 `data/settings.yaml` 并对后续运行生效，优先级高于 `.env`。

`docker-compose.yml` 会覆盖数据库地址与数据目录，需额外提供 `POSTGRES_PASSWORD`：

```bash
echo 'POSTGRES_PASSWORD=强密码' >> .env
docker compose up -d --build
```

## 端口

默认只发布到宿主机回环：

| 用途 | 默认 | 覆盖变量 |
|---|---|---|
| 健康/指标 | `127.0.0.1:8787` | `AGENTHUB_HTTP_PORT` |
| 管理台 | `127.0.0.1:8788` | `AGENTHUB_ADMIN_PORT` |

本机端口冲突时：

```bash
AGENTHUB_HTTP_PORT=18887 AGENTHUB_ADMIN_PORT=18888 docker compose up -d
```

需要远端访问管理台时，用 SSH 隧道而不是直接暴露公网：

```bash
ssh -L 8788:127.0.0.1:8788 user@host
```

## 验收

```bash
docker compose ps                    # 两个服务 healthy
curl -s 127.0.0.1:8787/healthz       # {"status":"ok",...}
curl -s 127.0.0.1:8787/metrics | grep agenthub_health_check
docker compose logs --tail=50 app
```

容器启动时会自动执行迁移、把 `skills/` 种子复制到 `/data/skills`。

只做冒烟（不接飞书，避免与线上重复收事件）：

```bash
COMPOSE_CHANNEL=off AGENTHUB_HTTP_PORT=28887 AGENTHUB_ADMIN_PORT=28888 \
  docker compose up -d --build
```

## 备份与恢复

```bash
./scripts/backup.sh                      # 数据库 dump + /data 卷归档，默认保留 14 天
BACKUP_DIR=/backup KEEP_DAYS=30 ./scripts/backup.sh

./scripts/restore.sh backups/db-20261111-120000.dump
./scripts/restore.sh backups/db-....dump backups/data-....tar.gz
```

恢复到已有库会覆盖数据，脚本会要求输入 `yes`。

## 镜像发布

打 tag 才会构建并推送（分支提交不发布）：

```bash
git tag v1.2.3
git push origin v1.2.3
```

GitHub Actions（`.github/workflows/release.yml`）会先跑测试，再构建 `linux/amd64` + `linux/arm64`
镜像并推送到 `donglog/agenthub`，生成三个 tag：`1.2.3`、`1.2`、`latest`（预发布版本不带 `latest`）。

需要在仓库 Secrets 配置：

| Secret | 说明 |
|---|---|
| `DOCKERHUB_USERNAME` | Docker Hub 用户名（donglog） |
| `DOCKERHUB_TOKEN` | Docker Hub Access Token（不要用密码） |

用发布镜像部署（不本地构建）：

```bash
AGENTHUB_TAG=1.2.3 docker compose pull
AGENTHUB_TAG=1.2.3 docker compose up -d
```

或直接运行：

```bash
docker run -d --name agenthub \
  --env-file .env \
  -v agenthub-data:/data \
  -p 127.0.0.1:8787:8787 -p 127.0.0.1:8788:8788 \
  donglog/agenthub:1.2.3
```

## 升级

```bash
git pull
docker compose up -d --build
```

迁移只前进，不需要手工执行 SQL；回滚镜像不会回滚已应用的迁移。

## 监控与告警

`/metrics` 输出 Prometheus 文本格式，关键指标：

| 指标 | 说明 |
|---|---|
| `agenthub_health_check{check="feishu"}` | 飞书连接 1/0 |
| `agenthub_runs_total{state=...}` | 运行完成/失败/中断 |
| `agenthub_deliveries_total{result=...}` | 投递结果 |
| `agenthub_messages_accepted_total` | 受理消息数 |
| `agenthub_http_requests_total` | HTTP 请求 |

告警规则在 `deploy/prometheus/alerts.yml`（服务不可达、飞书断开、运行失败、投递失败、调用量异常）。

## 常见问题

| 现象 | 排查 |
|---|---|
| 容器起不来，端口占用 | 换 `AGENTHUB_HTTP_PORT` / `AGENTHUB_ADMIN_PORT` |
| `permission denied` 写 `/data` | 用了 bind mount；改用命名卷，或确保目录属主为 65532 |
| 健康检查失败 | `docker compose logs app`；确认 DB 可达、迁移成功 |
| 飞书反复重连 | 凭据错误或 DNS 被污染（证书与域名不符），检查出网 DNS |
