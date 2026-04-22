#!/usr/bin/env bash
# Drive the kind-based integration test.
#
# Preconditions: kind, kubectl and docker on PATH; a built pgao:integration image
# already loaded into the cluster.
#
# Runs a set of black-box HTTP assertions against the pgao Service exposed on
# 127.0.0.1:30080 (see scripts/kind/kind-config.yaml).

set -euo pipefail

HOST="${PGAO_HOST:-http://127.0.0.1:30080}"
RETRIES="${PGAO_RETRIES:-60}"

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

wait_for() {
  local url="$1" expect="$2" desc="$3"
  for _ in $(seq 1 "$RETRIES"); do
    code="$(curl -s -o /dev/null -w '%{http_code}' "$url" || true)"
    if [ "$code" = "$expect" ]; then
      pass "$desc -> $code"
      return 0
    fi
    sleep 2
  done
  fail "$desc never returned $expect (last=$code)"
}

main() {
  echo "--- /health ---"
  wait_for "$HOST/health" 200 "health endpoint reachable"

  echo "--- /ready ---"
  wait_for "$HOST/ready" 200 "ready endpoint reachable"

  echo "--- GET /api/v1/clusters ---"
  body="$(curl -sf "$HOST/api/v1/clusters")"
  echo "$body" | grep -q "integration-test" || fail "configured cluster not listed in /api/v1/clusters: $body"
  pass "cluster listed in response"

  echo "--- GET /api/v1/clusters/integration-test ---"
  detail="$(curl -sf "$HOST/api/v1/clusters/integration-test")"
  echo "$detail" | grep -q '"version":"PostgreSQL' \
    || fail "cluster detail missing real PostgreSQL version (issue #3): $detail"
  echo "$detail" | grep -q '"databases"' \
    || fail "cluster detail missing databases list: $detail"
  pass "cluster detail returns real version + databases"

  echo "--- GET /api/v1/clusters/integration-test/metrics ---"
  metrics="$(curl -sf "$HOST/api/v1/clusters/integration-test/metrics")"
  echo "$metrics" | grep -q '"cluster_id":"integration-test"' \
    || fail "metrics response missing cluster_id: $metrics"
  pass "metrics reported for integration-test"

  echo "--- POST /api/v1/analyze (SELECT) ---"
  analysis="$(curl -sf -H 'Content-Type: application/json' \
    -d '{"query":"SELECT id, name FROM test_table WHERE id = 1"}' \
    "$HOST/api/v1/analyze")"
  echo "$analysis" | grep -q '"query_type":"SELECT"' \
    || fail "analyze response missing query_type=SELECT: $analysis"
  pass "analyze returns SELECT for test query"

  echo "--- POST /api/v1/analyze (invalid) ---"
  code="$(curl -s -o /dev/null -w '%{http_code}' -X POST \
    -H 'Content-Type: application/json' \
    -d '{}' "$HOST/api/v1/analyze")"
  [ "$code" = "400" ] || fail "empty body should return 400, got $code"
  pass "analyze rejects empty body"

  echo "--- POST /api/v1/analyze (oversized body) ---"
  # Fire a 2 MiB body past the default 1 MiB cap.
  big="$(head -c 2097152 /dev/zero | tr '\0' A)"
  code="$(curl -s -o /dev/null -w '%{http_code}' -X POST \
    -H 'Content-Type: application/json' \
    --data-binary "{\"query\":\"$big\"}" "$HOST/api/v1/analyze")"
  case "$code" in
    413|400) pass "oversized body rejected -> $code" ;;
    *) fail "oversized body should be rejected (413/400), got $code" ;;
  esac

  echo "--- GET /api/v1/clusters/nonexistent/metrics (internal error sanitisation) ---"
  body="$(curl -s "$HOST/api/v1/clusters/nonexistent/metrics" || true)"
  echo "$body" | grep -q '"error":"internal server error"' \
    || fail "unknown-cluster error must be generic, got: $body"
  echo "$body" | grep -qiE 'connection|failed to' \
    && fail "internal error leaked implementation detail: $body"
  pass "internal errors surface as generic message"

  echo "--- GET /api/v1/clusters/integration-test/queries ---"
  queries="$(curl -sf "$HOST/api/v1/clusters/integration-test/queries?limit=5&order_by=total_exec_time")"
  echo "$queries" | grep -q '\[' \
    || fail "/queries did not return a JSON array: $queries"
  pass "/queries returned a JSON array"

  echo "--- GET /api/v1/clusters/integration-test/tables ---"
  tables="$(curl -sf "$HOST/api/v1/clusters/integration-test/tables?limit=5")"
  echo "$tables" | grep -q '\[' \
    || fail "/tables did not return a JSON array: $tables"
  pass "/tables returned a JSON array"

  echo "--- GET /metrics ---"
  prom="$(curl -sf "$HOST/metrics")"
  echo "$prom" | grep -q '^pgao_cluster_up' \
    || fail "/metrics missing pgao_cluster_up series (issue #5)"
  echo "$prom" | grep -q '^go_goroutines' \
    || fail "/metrics missing Go runtime metrics"
  pass "/metrics exposes pgao_* and go_* series"

  echo
  echo "Integration tests passed"
}

main "$@"
