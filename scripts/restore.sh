#!/usr/bin/env bash
# Restore from backup: scripts/restore.sh backups/db-YYYYmmdd-HHMMSS.dump [data-*.tar.gz]
set -euo pipefail
cd "$(dirname "$0")/.."

DUMP=${1:-}
DATA_TAR=${2:-}
if [ -z "$DUMP" ] || [ ! -f "$DUMP" ]; then
  echo "usage: $0 <db-*.dump> [data-*.tar.gz]" >&2
  exit 1
fi

echo "This will overwrite the current database with $DUMP"
echo -n "Type yes to continue: "
read -r answer
if [ "$answer" != "yes" ]; then
  echo "aborted"
  exit 1
fi

echo "==> stop app"
docker compose stop app

echo "==> restore database"
docker compose exec -T postgres pg_restore -U postgres -d postgres --clean --if-exists --no-owner < "$DUMP"

if [ -n "$DATA_TAR" ] && [ -f "$DATA_TAR" ]; then
  APP_ID=$(docker compose ps -q app)
  DATA_VOLUME=$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Name}}{{end}}{{end}}' "$APP_ID")
  ABS_TAR=$(cd "$(dirname "$DATA_TAR")" && pwd)
  echo "==> restore data volume ($DATA_VOLUME)"
  docker run --rm -v "$DATA_VOLUME":/data -v "$ABS_TAR":/backup alpine \
    sh -c "rm -rf /data/* && tar xzf /backup/$(basename "$DATA_TAR") -C /data"
fi

echo "==> start app"
docker compose start app
docker compose ps
