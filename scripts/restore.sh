#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
backup="${1:?Usage: restore.sh BACKUP_DIRECTORY --confirm-replace}"
[[ "${2:-}" = --confirm-replace ]] || { echo 'Restores and replaces the configured HomeFleet data volumes. Pass --confirm-replace for this deployment only.' >&2; exit 2; }
for file in homefleet.db master.key caddy-data.tar.gz deployment.env; do [[ -f "$backup/$file" ]] || { echo "Missing $file" >&2; exit 1; }; done
docker compose stop
cp "$backup/deployment.env" .env
chmod 600 .env
if [[ -d "$backup/config" ]]; then
  for file in "$backup/config"/compose*.yaml; do cp "$file" ./; done
  if [[ -f "$backup/config/compose.override.yaml" ]]; then
    cp "$backup/config/compose.override.yaml" compose.override.yaml
  elif [[ -f compose.override.yaml ]]; then
    rm compose.override.yaml
  fi
  cp -R "$backup/config/deploy/." deploy/
fi
docker compose config --quiet
docker compose run --rm --no-deps --user root --entrypoint sh -v "$(cd "$backup" && pwd):/restore:ro" hub -c 'rm -f /data/homefleet.db-wal /data/homefleet.db-shm; cp /restore/homefleet.db /data/homefleet.db; cp /restore/master.key /data/master.key; chown 10001:10001 /data/homefleet.db /data/master.key; chmod 600 /data/homefleet.db /data/master.key'
docker compose run --rm --no-deps --entrypoint sh -v "$(cd "$backup" && pwd):/restore:ro" caddy -c 'tar -xzf /restore/caddy-data.tar.gz -C /data'
docker compose up -d
echo 'Restored this HomeFleet deployment. Verify /healthz and reconnect agents; do not automatically retry unknown tasks.'
