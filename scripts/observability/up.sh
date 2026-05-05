#!/usr/bin/env bash
# Spin up a kind cluster with kube-prometheus-stack + pgao so an
# operator can browse Grafana, import docs/grafana/pgao-overview.json,
# and see real data scraped from a Postgres running inside the cluster.
#
# Idempotent. Tear down with scripts/observability/down.sh.

set -euo pipefail

CLUSTER="${CLUSTER:-pgao-obs}"
NS_PROM="${NS_PROM:-monitoring}"
NS_PG="${NS_PG:-postgres}"
NS_PGAO="${NS_PGAO:-pgao}"
KPS_VERSION="${KPS_VERSION:-65.5.0}"

bin_check() {
  for bin in kind kubectl helm docker; do
    command -v "$bin" >/dev/null 2>&1 || {
      echo "missing dependency: $bin" >&2
      exit 1
    }
  done
}

bin_check

if ! kind get clusters | grep -qx "$CLUSTER"; then
  echo "==> creating kind cluster $CLUSTER"
  kind create cluster --name "$CLUSTER"
else
  echo "==> kind cluster $CLUSTER already exists"
fi

kubectl cluster-info --context "kind-$CLUSTER" >/dev/null

echo "==> building + loading pgao image"
docker build -t pgao:obs .
kind load docker-image pgao:obs --name "$CLUSTER"

echo "==> deploying Postgres"
kubectl create namespace "$NS_PG" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$NS_PG" create secret generic postgres-creds \
  --from-literal=password=postgres \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -n "$NS_PG" -f - <<'YAML'
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: postgres
spec:
  serviceName: postgres
  replicas: 1
  selector: { matchLabels: { app: postgres } }
  template:
    metadata: { labels: { app: postgres } }
    spec:
      containers:
        - name: postgres
          image: postgres:16-alpine
          args:
            - "-c"
            - "shared_preload_libraries=pg_stat_statements"
            - "-c"
            - "pg_stat_statements.track=all"
          env:
            - name: POSTGRES_PASSWORD
              valueFrom: { secretKeyRef: { name: postgres-creds, key: password } }
            - name: POSTGRES_DB
              value: postgres
          ports: [{ containerPort: 5432 }]
          readinessProbe:
            exec: { command: [pg_isready, -U, postgres] }
            initialDelaySeconds: 5
            periodSeconds: 5
---
apiVersion: v1
kind: Service
metadata: { name: postgres }
spec:
  selector: { app: postgres }
  ports: [{ port: 5432, targetPort: 5432 }]
YAML

kubectl -n "$NS_PG" rollout status statefulset/postgres --timeout=180s

# pg_stat_statements has to be created in the database too.
kubectl -n "$NS_PG" exec sts/postgres -- \
  psql -U postgres -c "CREATE EXTENSION IF NOT EXISTS pg_stat_statements;" || true

echo "==> installing kube-prometheus-stack"
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts >/dev/null
helm repo update >/dev/null
kubectl create namespace "$NS_PROM" --dry-run=client -o yaml | kubectl apply -f -
helm upgrade --install kps prometheus-community/kube-prometheus-stack \
  --namespace "$NS_PROM" \
  --version "$KPS_VERSION" \
  --set grafana.adminPassword=admin \
  --set grafana.service.type=ClusterIP \
  --wait --timeout 10m

echo "==> deploying pgao via Helm"
kubectl create namespace "$NS_PGAO" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$NS_PGAO" create secret generic pgao-pg-creds \
  --from-literal=password=postgres \
  --dry-run=client -o yaml | kubectl apply -f -

helm upgrade --install pgao charts/pgao \
  --namespace "$NS_PGAO" \
  --set image.repository=pgao --set image.tag=obs --set image.pullPolicy=IfNotPresent \
  --set replicaCount=1 \
  --set serviceMonitor.enabled=true \
  --set "clusters[0].id=local" \
  --set "clusters[0].name=Local" \
  --set "clusters[0].host=postgres.${NS_PG}.svc.cluster.local" \
  --set "clusters[0].user=postgres" \
  --set "clusters[0].database=postgres" \
  --set "clusters[0].existingPasswordSecret=pgao-pg-creds" \
  --set "clusters[0].passwordSecretKey=password" \
  --set "clusters[0].ssl_mode=disable" \
  --wait --timeout 5m

echo
echo "Stack is up."
echo
echo "  Grafana:    kubectl -n $NS_PROM port-forward svc/kps-grafana 3000:80"
echo "              login: admin / admin"
echo "              import: docs/grafana/pgao-overview.json"
echo
echo "  pgao API:   kubectl -n $NS_PGAO port-forward svc/pgao 8080:8080"
echo "              curl http://127.0.0.1:8080/health"
echo
echo "  Tear down:  scripts/observability/down.sh"
