#!/usr/bin/env bash
# Reproduces the Grafana + Prometheus screenshots under
# docs/screenshots/05-08. Bare binaries — no docker / kind — so it works
# wherever the UI screenshot script does.
#
# Pipeline:
#   1. (re)start a local Postgres 16 with pg_stat_statements preloaded
#   2. build + start pgao against it
#   3. download Prometheus + Grafana binaries (cached under /tmp/obs)
#   4. provision the Prometheus datasource and import
#      docs/grafana/pgao-overview.json into Grafana via API
#   5. run pgbench in the background so panels are non-flat during the
#      shoot
#   6. drive a headless Chromium with Playwright over Grafana's
#      dashboard (kiosk + dark) and Prometheus' graph / targets pages
#
# Usage:
#   scripts/screenshots/capture-observability.sh [OUTPUT_DIR]

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="${1:-$REPO_ROOT/docs/screenshots}"
PG_BIN="/usr/lib/postgresql/16/bin"
PGDATA="${PGDATA:-/tmp/pgao-pg}"
PG_PORT="${PG_PORT:-55432}"
PGAO_PORT="${PGAO_PORT:-8080}"
PROM_PORT="${PROM_PORT:-9095}"
GRAFANA_PORT="${GRAFANA_PORT:-3000}"
OBS="${OBS:-/tmp/obs}"
PROM_VER="${PROM_VER:-2.55.0}"
GRAFANA_VER="${GRAFANA_VER:-11.4.0}"

trap cleanup EXIT
cleanup() {
  echo "==> cleanup"
  pkill -f "/grafana-v${GRAFANA_VER}/bin/grafana" 2>/dev/null || true
  pkill -f "prometheus.*--config.file=$OBS/prometheus.yml" 2>/dev/null || true
  pkill -f "pgbench.*-T" 2>/dev/null || true
  pkill -f "$REPO_ROOT/bin/pgao\|/tmp/pgao\b" 2>/dev/null || true
  sudo -u postgres "$PG_BIN/pg_ctl" -D "$PGDATA" stop -m fast 2>/dev/null || true
}

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing $1" >&2; exit 1; }; }
need go
need npm
need pgbench
need psql
need curl
[ -x "$PG_BIN/postgres" ] || { echo "missing postgres 16 at $PG_BIN" >&2; exit 1; }

mkdir -p "$OBS" "$OUT"

echo "==> postgres on :$PG_PORT"
if [ ! -d "$PGDATA" ] || [ ! -s "$PGDATA/PG_VERSION" ]; then
  mkdir -p "$PGDATA"; chown postgres:postgres "$PGDATA"; chmod 700 "$PGDATA"
  sudo -u postgres "$PG_BIN/initdb" --auth=trust --username=postgres -D "$PGDATA" >/dev/null
  {
    echo "shared_preload_libraries = 'pg_stat_statements'"
    echo "pg_stat_statements.track = 'all'"
    echo "port = $PG_PORT"
    echo "unix_socket_directories = '/tmp'"
  } | sudo -u postgres tee -a "$PGDATA/postgresql.conf" >/dev/null
fi
sudo -u postgres "$PG_BIN/pg_ctl" -D "$PGDATA" -l "$OBS/pg.log" start
sleep 1
psql -h 127.0.0.1 -p "$PG_PORT" -U postgres <<SQL >/dev/null 2>&1 || true
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
DO \$\$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pgao_monitor') THEN
    CREATE ROLE pgao_monitor LOGIN PASSWORD 'monitor' IN ROLE pg_monitor;
    GRANT pg_read_all_stats TO pgao_monitor;
    GRANT CONNECT ON DATABASE postgres TO pgao_monitor;
  END IF;
END \$\$;
SQL

echo "==> building pgao"
cd "$REPO_ROOT"
npm --prefix web ci --no-audit --no-fund >/dev/null 2>&1 || npm --prefix web install --no-audit --no-fund >/dev/null
npm --prefix web run build >/dev/null
go build -o "$OBS/pgao" ./src/main.go

cat >"$OBS/pgao-config.yaml" <<YAML
server: { host: "127.0.0.1", port: $PGAO_PORT }
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
CONFIG_PATH="$OBS/pgao-config.yaml" "$OBS/pgao" >"$OBS/pgao.log" 2>&1 &
sleep 4
curl -sf "http://127.0.0.1:$PGAO_PORT/health" >/dev/null

echo "==> prometheus on :$PROM_PORT"
if [ ! -x "$OBS/prometheus-${PROM_VER}.linux-amd64/prometheus" ]; then
  curl -sL "https://github.com/prometheus/prometheus/releases/download/v${PROM_VER}/prometheus-${PROM_VER}.linux-amd64.tar.gz" | tar -xz -C "$OBS"
fi
cat >"$OBS/prometheus.yml" <<YAML
global: { scrape_interval: 5s, evaluation_interval: 5s }
scrape_configs:
  - job_name: pgao
    static_configs:
      - targets: ['127.0.0.1:$PGAO_PORT']
        labels: { instance: local }
YAML
"$OBS/prometheus-${PROM_VER}.linux-amd64/prometheus" \
  --config.file="$OBS/prometheus.yml" \
  --storage.tsdb.path="$OBS/prom-data" \
  --web.listen-address=127.0.0.1:$PROM_PORT \
  >"$OBS/prom.log" 2>&1 &
sleep 3

echo "==> grafana on :$GRAFANA_PORT"
if [ ! -x "$OBS/grafana-v${GRAFANA_VER}/bin/grafana" ]; then
  curl -sL -o "$OBS/grafana.tar.gz" "https://dl.grafana.com/oss/release/grafana-${GRAFANA_VER}.linux-amd64.tar.gz"
  tar -xzf "$OBS/grafana.tar.gz" -C "$OBS"
fi
rm -rf "$OBS/grafana-data"
cat >"$OBS/grafana.ini" <<INI
[server]
http_addr = 127.0.0.1
http_port = $GRAFANA_PORT
[paths]
data = $OBS/grafana-data
logs = $OBS/grafana-data/logs
plugins = $OBS/grafana-data/plugins
[users]
default_theme = dark
[security]
admin_user = admin
admin_password = admin
[log]
level = warn
[analytics]
reporting_enabled = false
check_for_updates = false
INI
GF_PATHS_CONFIG="$OBS/grafana.ini" "$OBS/grafana-v${GRAFANA_VER}/bin/grafana" server >"$OBS/grafana.log" 2>&1 &
sleep 6
# Provision datasource + dashboard via API (file-based provisioning is
# unreliable across Grafana minor versions; API-based is deterministic).
curl -sf -u admin:admin -H "Content-Type: application/json" \
  -d "{\"name\":\"Prometheus\",\"type\":\"prometheus\",\"uid\":\"prom-local\",\"access\":\"proxy\",\"url\":\"http://127.0.0.1:$PROM_PORT\",\"isDefault\":true}" \
  "http://127.0.0.1:$GRAFANA_PORT/api/datasources" >/dev/null || true
python3 - <<PY
import json, urllib.request
src = '$REPO_ROOT/docs/grafana/pgao-overview.json'
d = json.load(open(src))
d['templating']['list'] = [t for t in d['templating']['list'] if t.get('name') != 'DS_PROMETHEUS']
def fix(o):
    if isinstance(o, dict):
        if o.get('type') == 'prometheus' and str(o.get('uid','')).startswith('\${'):
            o['uid'] = 'prom-local'
        for v in o.values(): fix(v)
    elif isinstance(o, list):
        for v in o: fix(v)
fix(d); d.pop('id', None)
payload = json.dumps({'dashboard': d, 'overwrite': True, 'folderUid': ''}).encode()
import base64
auth = base64.b64encode(b'admin:admin').decode()
req = urllib.request.Request('http://127.0.0.1:$GRAFANA_PORT/api/dashboards/db',
    data=payload, headers={'Content-Type':'application/json','Authorization':'Basic '+auth}, method='POST')
print(urllib.request.urlopen(req).read().decode()[:120])
PY

echo "==> pgbench: init scale 5"
pgbench -h 127.0.0.1 -p "$PG_PORT" -U postgres -i -s 5 postgres >/dev/null 2>&1 || true
echo "==> pgbench: sustained 60 s load while we shoot"
pgbench -h 127.0.0.1 -p "$PG_PORT" -U postgres -c 10 -j 2 -T 60 postgres >"$OBS/pgbench.log" 2>&1 &
sleep 15

echo "==> Playwright"
SHOT="$OBS/shotter"; mkdir -p "$SHOT"
cat >"$SHOT/package.json" <<'JSON'
{ "name": "obs-shots", "private": true, "type": "module", "dependencies": { "playwright": "^1.49.0" } }
JSON
cd "$SHOT"; npm install --silent
npx playwright install --with-deps chromium >/dev/null

cat >"$SHOT/shoot.mjs" <<JS
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
const OUT = process.argv[2];
mkdirSync(OUT, { recursive: true });
const browser = await chromium.launch();
const ctx = await browser.newContext({ viewport: { width: 1600, height: 1000 }, colorScheme: 'dark' });
async function loginGrafana() {
  const p = await ctx.newPage();
  const r = await p.request.post('http://127.0.0.1:$GRAFANA_PORT/login',
    { data: { user: 'admin', password: 'admin' }, headers: { 'Content-Type': 'application/json' } });
  if (!r.ok()) throw new Error('grafana login: ' + r.status());
  await p.close();
}
async function shoot(name, url, opts = {}) {
  const p = await ctx.newPage();
  await p.goto(url, { waitUntil: 'networkidle' });
  await p.waitForTimeout(opts.settle ?? 4000);
  await p.screenshot({ path: \`\${OUT}/\${name}.png\`, fullPage: opts.fullPage ?? false });
  await p.close();
}
await loginGrafana();
const dash = 'http://127.0.0.1:$GRAFANA_PORT/d/pgao-overview/pgao-overview?orgId=1&refresh=10s&from=now-5m&to=now&kiosk&var-job=pgao&var-cluster=local';
await shoot('05-grafana-overview', dash, { settle: 5000 });
await shoot('06-grafana-full', dash, { settle: 5000, fullPage: true });
await shoot('07-prometheus-graph',
  'http://127.0.0.1:$PROM_PORT/graph?g0.expr=pgao_transactions_per_sec&g0.tab=0&g0.range_input=5m&g0.display_mode=lines',
  { settle: 3500 });
await shoot('08-prometheus-targets', 'http://127.0.0.1:$PROM_PORT/targets', { settle: 1500 });
await ctx.close(); await browser.close();
JS
node "$SHOT/shoot.mjs" "$OUT"
echo "done → $OUT"
