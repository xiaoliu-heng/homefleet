#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
out="${1:?Usage: backup.sh EXISTING_BACKUP_PARENT/new-backup-directory}"
[[ ! -e "$out" ]] || { echo 'Backup destination must not already exist.' >&2; exit 1; }
mkdir -m 700 -p "$out"
docker compose exec -T hub /app/homefleet-hub --backup /data/export.db
trap 'docker compose exec -T hub rm -f /data/export.db >/dev/null 2>&1 || true' EXIT
docker compose cp hub:/data/export.db "$out/homefleet.db"
docker compose cp hub:/data/master.key "$out/master.key"
docker compose exec -T caddy tar -czf - -C /data caddy > "$out/caddy-data.tar.gz"
cp .env "$out/deployment.env"
chmod 600 "$out"/*
mkdir -m 700 "$out/config"
for file in compose*.yaml; do cp "$file" "$out/config/$file"; done
cp -R deploy "$out/config/deploy"
chmod -R go-rwx "$out/config"
printf 'Backup created at %s; contains credentials and the local CA private key.\n' "$out"
