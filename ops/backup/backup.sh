#!/usr/bin/env bash
# Daily MySQL backup at $BACKUP_TIME (container TZ), keeps $BACKUP_RETENTION_DAYS days.
#   docker compose exec backup backup.sh now     # run once immediately
set -uo pipefail

BACKUP_DIR=/backups
BACKUP_TIME="${BACKUP_TIME:-02:00}"
RETENTION="${BACKUP_RETENTION_DAYS:-30}"
mkdir -p "$BACKUP_DIR"

# The API reads this file (mounted read-only) and alerts the owner in Telegram when
# a backup failed or none ran for over a day; the admin panel shows the last run.
STATUS="$BACKUP_DIR/.last_backup.json"
status() { # ok(true|false) file size
  printf '{"ok":%s,"at":"%s","file":"%s","size":"%s"}\n' "$1" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$2" "$3" > "$STATUS.tmp" && mv "$STATUS.tmp" "$STATUS"
}

run_backup() {
  local ts file tmp
  ts="$(date +%Y%m%d-%H%M)"
  file="$BACKUP_DIR/${MYSQL_DATABASE}-${ts}.sql.gz"
  tmp="$file.partial"
  if MYSQL_PWD="$MYSQL_PASSWORD" mysqldump -h "$MYSQL_HOST" -u "$MYSQL_USER" \
       --single-transaction --quick --routines --triggers --events --set-gtid-purged=OFF \
       "$MYSQL_DATABASE" | gzip -9 > "$tmp" && gzip -t "$tmp"; then
    mv "$tmp" "$file"
    find "$BACKUP_DIR" -name "${MYSQL_DATABASE}-*.sql.gz" -mtime +"$RETENTION" -delete
    echo "$(date -Is) backup ok: $file ($(du -h "$file" | cut -f1))"
    status true "$(basename "$file")" "$(du -h "$file" | cut -f1)"
  else
    rm -f "$tmp"
    echo "$(date -Is) backup FAILED" >&2
    status false "" ""
    return 1
  fi
}

if [ "${1:-}" = "now" ]; then run_backup; exit $?; fi

echo "backup scheduler started: daily at $BACKUP_TIME, retention $RETENTION days"
while true; do
  now=$(date +%s)
  next=$(date -d "today $BACKUP_TIME" +%s)
  [ "$next" -le "$now" ] && next=$(date -d "tomorrow $BACKUP_TIME" +%s)
  sleep $(( next - now ))
  run_backup
  sleep 61
done
