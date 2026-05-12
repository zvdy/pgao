#!/usr/bin/env bash
# Reproduces the screenshots committed under docs/screenshots/. Useful
# when reviewing UI changes — run this, eyeball the diff, commit the
# refreshed PNGs alongside your patch.
#
# Spins up a bare PostgreSQL 16 + pgao + pgbench load on the local
# machine; no docker / kind needed. Captures four UI states with
# Playwright + headless Chromium.
#
# Prereqs (apt-get on Ubuntu 24.04):
#   postgresql postgresql-contrib postgresql-client     # initdb, postgres, psql, pgbench, pg_stat_statements
#   nodejs npm                                          # Playwright host
#   golang-go                                           # builds pgao
#
# Usage:
#   scripts/screenshots/capture.sh [OUTPUT_DIR]
#
# OUTPUT_DIR defaults to docs/screenshots.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="${1:-$REPO_ROOT/docs/screenshots}"
PG_BIN="/usr/lib/postgresql/16/bin"
PGDATA="${PGDATA:-/tmp/pgao-screenshot-pg}"
PG_PORT="${PG_PORT:-55432}"
PGAO_PORT="${PGAO_PORT:-8080}"
WORK="$(mktemp -d)"
trap 'cleanup' EXIT

pids=()
cleanup() {
  echo "==> cleanup"
  for pid in "${pids[@]:-}"; do kill "$pid" 2>/dev/null || true; done
  if [ -d "$PGDATA" ]; then
    sudo -u postgres "$PG_BIN/pg_ctl" -D "$PGDATA" stop -m fast 2>/dev/null || true
    rm -rf "$PGDATA"
  fi
  rm -rf "$WORK"
}

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing $1" >&2; exit 1; }; }
need go
need npm
need pgbench
need psql
[ -x "$PG_BIN/postgres" ] || { echo "missing postgres 16 at $PG_BIN" >&2; exit 1; }

echo "==> initdb on :$PG_PORT"
rm -rf "$PGDATA"
mkdir -p "$PGDATA"
chown postgres:postgres "$PGDATA"
chmod 700 "$PGDATA"
sudo -u postgres "$PG_BIN/initdb" --auth=trust --username=postgres -D "$PGDATA" >/dev/null
{
  echo "shared_preload_libraries = 'pg_stat_statements'"
  echo "pg_stat_statements.track = 'all'"
  echo "port = $PG_PORT"
  echo "unix_socket_directories = '/tmp'"
  echo "logging_collector = off"
} | sudo -u postgres tee -a "$PGDATA/postgresql.conf" >/dev/null

echo "==> starting postgres"
sudo -u postgres "$PG_BIN/pg_ctl" -D "$PGDATA" -l "$WORK/pg.log" start
sleep 1
psql -h 127.0.0.1 -p "$PG_PORT" -U postgres <<SQL >/dev/null
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
CREATE ROLE pgao_monitor LOGIN PASSWORD 'monitor' IN ROLE pg_monitor;
GRANT pg_read_all_stats TO pgao_monitor;
GRANT CONNECT ON DATABASE postgres TO pgao_monitor;
CREATE DATABASE billing OWNER postgres;
SQL

echo "==> building pgao"
cd "$REPO_ROOT"
npm --prefix web ci --no-audit --no-fund >/dev/null 2>&1 || npm --prefix web install --no-audit --no-fund >/dev/null
npm --prefix web run build >/dev/null
go build -o "$WORK/pgao" ./src/main.go

cat >"$WORK/pgao-config.yaml" <<YAML
server: { host: "127.0.0.1", port: $PGAO_PORT, request_timeout: 10s }
logging: { level: "info", format: "json", output: "stdout" }
metrics: { collection_interval: 5s, query_timeout: 5s, health_check_interval: 3s, enable_prometheus: true, prometheus_port: 9090 }
clusters:
  - id: "local"
    name: "Local PostgreSQL"
    host: "127.0.0.1"
    port: $PG_PORT
    user: "pgao_monitor"
    password: "monitor"
    database: "postgres"
    ssl_mode: "disable"
    statement_timeout: 5s
YAML

echo "==> starting pgao on :$PGAO_PORT"
CONFIG_PATH="$WORK/pgao-config.yaml" "$WORK/pgao" >"$WORK/pgao.log" 2>&1 &
pids+=($!)
sleep 4
curl -sf "http://127.0.0.1:$PGAO_PORT/health" >/dev/null

echo "==> pgbench: init scale 5"
pgbench -h 127.0.0.1 -p "$PG_PORT" -U postgres -i -s 5 postgres >/dev/null 2>&1
echo "==> pgbench: 20 s load with 10 clients"
pgbench -h 127.0.0.1 -p "$PG_PORT" -U postgres -c 10 -j 2 -T 20 postgres >/dev/null

echo "==> waiting for pgao to refresh stats"
sleep 10

echo "==> installing Playwright (once per checkout)"
mkdir -p "$WORK/shots"
cat >"$WORK/package.json" <<'JSON'
{ "name": "pgao-shots", "private": true, "type": "module", "dependencies": { "playwright": "^1.49.0" } }
JSON
cd "$WORK"
npm install --silent
npx playwright install --with-deps chromium >/dev/null

cat >"$WORK/shoot.mjs" <<JS
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
const OUT = process.argv[2];
const BASE = process.env.PGAO ?? 'http://127.0.0.1:$PGAO_PORT';
mkdirSync(OUT, { recursive: true });
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
await page.emulateMedia({ colorScheme: 'light' });
async function shoot(name, url, prep) {
  await page.goto(BASE + url, { waitUntil: 'networkidle' });
  if (prep) await prep();
  await page.waitForTimeout(400);
  await page.screenshot({ path: \`\${OUT}/\${name}.png\`, fullPage: true });
}
await shoot('01-overview', '/');
await shoot('02-cluster-metrics', '/clusters/local');
await shoot('03-cluster-queries', '/clusters/local', async () => {
  await page.getByRole('tab', { name: 'queries' }).click();
  await page.waitForTimeout(600);
});
await shoot('04-cluster-tables', '/clusters/local', async () => {
  await page.getByRole('tab', { name: 'tables' }).click();
  await page.waitForTimeout(600);
});
await browser.close();
JS

echo "==> capturing → $OUT"
mkdir -p "$OUT"
node shoot.mjs "$OUT"
ls -la "$OUT"

echo "done"
