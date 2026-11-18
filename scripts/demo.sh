#!/usr/bin/env bash
# 零凭据演示：不接飞书、不配模型，本机起一个能看效果的最小实例。
#
#   ./scripts/demo.sh              # 需要本机 PostgreSQL
#   DEMO_DB_URL=postgres://... ./scripts/demo.sh
#
# 起来后：
#   管理台  http://127.0.0.1:20902/admin/
#   推告警  ./scripts/verify.sh http://127.0.0.1:20901 http://127.0.0.1:20902
#
# 没有模型密钥时自动改用回显执行器，链路照常跑通，只是结论是回显文本。
set -euo pipefail
cd "$(dirname "$0")/.."

DB_URL=${DEMO_DB_URL:-postgres://postgres:agenthub@127.0.0.1:55433/agenthub_demo?sslmode=disable}
PORT=${DEMO_HTTP_PORT:-20901}
ADMIN_PORT=${DEMO_ADMIN_PORT:-20902}
DATA_DIR=${DEMO_DATA_DIR:-./tmp/demo-data}

mkdir -p "$DATA_DIR"
go build -o "$DATA_DIR/agentd" ./cmd/agentd

echo "==> 演示实例启动中（channel=off，无模型凭据时用回显）"
echo "    管理台 http://127.0.0.1:${ADMIN_PORT}/admin/"
echo "    推告警 ./scripts/verify.sh http://127.0.0.1:${PORT} http://127.0.0.1:${ADMIN_PORT}"
echo

exec env \
  AGENTHUB_ENV=development \
  AGENTHUB_CHANNEL=off \
  AGENTHUB_DB_URL="$DB_URL" \
  AGENTHUB_DATA_DIR="$DATA_DIR" \
  AGENTHUB_HTTP_ADDR="127.0.0.1:${PORT}" \
  AGENTHUB_ADMIN_ADDR="127.0.0.1:${ADMIN_PORT}" \
  AGENTHUB_AIOPS_ENABLED=true \
  AGENTHUB_AIOPS_TOKEN=demo \
  AGENTHUB_AIOPS_CHAT_ID=oc_demo \
  AGENTHUB_MIGRATIONS_DIR="$PWD/migrations" \
  "$DATA_DIR/agentd"
