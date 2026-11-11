#!/usr/bin/env bash
# Back up PostgreSQL and the /data volume. Keeps 14 days by default.
set -euo pipefail
cd "$(dirname "$0")/.."

BACKUP_DIR=${BACKUP_DIR:-./backups}
KEEP_DAYS=${KEEP_DAYS:-14}
STAMP=$(date +%Y%m%d-%H%M%S)
mkdir -p "$BACKUP_DIR"
ABS_BACKUP=$(cd "$BACKUP_DIR" && pwd)

APP_ID=$(docker compose ps -q app)
if [ -z "$APP_ID" ]; then
  echo "app container is not running; start it with: docker compose up -d" >&2
  exit 1
fi

echo "==> dump database"
docker compose exec -T postgres pg_dump -U postgres -d postgres -Fc > "$ABS_BACKUP/db-$STAMP.dump"

DATA_VOLUME=$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Name}}{{end}}{{end}}' "$APP_ID")
if [ -n "$DATA_VOLUME" ]; then
  echo "==> archive data volume ($DATA_VOLUME)"
  docker run --rm -v "$DATA_VOLUME":/data -v "$ABS_BACKUP":/backup alpine \
    tar czf "/backup/data-$STAMP.tar.gz" -C /data .
fi

echo "==> prune backups older than $KEEP_DAYS days"
find "$ABS_BACKUP" -type f \( -name 'db-*.dump' -o -name 'data-*.tar.gz' \) -mtime +"$KEEP_DAYS" -delete

ls -lh "$ABS_BACKUP" | tail -5
