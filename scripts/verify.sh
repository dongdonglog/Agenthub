#!/usr/bin/env bash
# 最小验收：推一条告警，确认它变成事故 → 案件 → 诊断报告。
#
#   AGENTHUB_AIOPS_TOKEN=xxx \
#   AGENTHUB_ADMIN_TOKEN=xxx \
#   ./scripts/verify.sh http://127.0.0.1:8787 http://127.0.0.1:8788
#
# 只看结果，不改任何数据；退出码 0 表示链路通。
set -euo pipefail

API=${1:-http://127.0.0.1:8787}
ADMIN=${2:-http://127.0.0.1:8788}
STAMP=$(date +%s)
SERVICE="verify-${STAMP}"
TITLE="验证告警 ${STAMP}"

echo "==> 1/4 健康检查"
curl -sf "$API/healthz" | jq -e '.status == "ok"' >/dev/null || {
  echo "healthz 不是 ok" >&2
  exit 1
}

echo "==> 2/4 推送告警（resource=${SERVICE}）"
curl -sf -X POST "$API/api/v1/alerts/alertmanager" \
  -H "Authorization: Bearer ${AGENTHUB_AIOPS_TOKEN:?}" \
  -H 'Content-Type: application/json' \
  -d "{\"version\":\"4\",\"groupKey\":\"${SERVICE}\",\"status\":\"firing\",
       \"commonLabels\":{\"service\":\"${SERVICE}\",\"severity\":\"critical\",\"alertname\":\"VerifyAlert\"},
       \"commonAnnotations\":{\"summary\":\"${TITLE}\",\"description\":\"验收用合成告警\"},
       \"alerts\":[{\"status\":\"firing\",
         \"labels\":{\"alertname\":\"VerifyAlert\",\"service\":\"${SERVICE}\"},
         \"annotations\":{\"summary\":\"${TITLE}\"},\"fingerprint\":\"${SERVICE}\"}]}" >/dev/null

AUTH=(-H "Authorization: Bearer ${AGENTHUB_ADMIN_TOKEN:?}")

echo "==> 3/4 确认事故与案件已创建"
INCIDENT=$(curl -sf "${AUTH[@]}" "$ADMIN/admin/api/v1/incidents?service=${SERVICE}" | jq -r '.items[0].id // empty')
[ -n "$INCIDENT" ] || { echo "没有生成事故" >&2; exit 1; }
CASE=$(curl -sf "${AUTH[@]}" "$ADMIN/admin/api/v1/cases?q=${SERVICE}" | jq -r '.items[0].id // empty')
[ -n "$CASE" ] || { echo "没有生成案件" >&2; exit 1; }
echo "    incident=${INCIDENT} case=${CASE}"

echo "==> 4/4 等待诊断报告（最多 5 分钟）"
# 注意：/cases/{id}/report 在没有真实报告时会用案件标题拼一份兜底草稿，
# 所以必须查 /reports（只返回已入库的 case_report），否则会误报成功。
REPORT=""
for _ in $(seq 1 60); do
  REPORT=$(curl -sf "${AUTH[@]}" "$ADMIN/admin/api/v1/reports" \
    | jq -r --arg case "$CASE" '.items[] | select(.report.case_id == $case) | .report.problem' | head -1)
  [ -n "$REPORT" ] && break
  sleep 5
done
[ -n "${REPORT:-}" ] || {
  echo "5 分钟内没有真实报告入库；检查模型/pi 是否可用（案例页可见运行详情）" >&2
  echo "注意：案件页看到的草稿可能来自标题兜底，不代表 pi 已产出。" >&2
  exit 1
}

echo
echo "OK：告警 → 事故 → 案件 → 报告 已通"
echo "  事故 $INCIDENT"
echo "  案件 $CASE"
echo "  报告 $(echo "$REPORT" | head -c 60)…"
echo
echo "提醒：报告里若有「未验证」，说明对应数据源没接上，不等于服务正常。"
