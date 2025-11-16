# PGAO - PostgreSQL Analytics Observer

[![Go Version](https://img.shields.io/badge/Go-1.21-blue)](https://golang.org)
[![Go Report Card](https://goreportcard.com/badge/github.com/zvdy/pgao)](https://goreportcard.com/report/github.com/zvdy/pgao)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

Monitor and analyze PostgreSQL clusters at scale. Query analysis via [pg_query_go](https://github.com/pganalyze/pg_query_go), multi-cluster support, REST API.

## Quick Start

```bash
# Local test with KIND
kind create cluster --name pgao-test
make docker-build
kind load docker-image pgao:latest --name pgao-test

cd terraform
cp .env.example .env
# Edit .env with your password
source .env && terraform apply -auto-approve

# Access API
kubectl port-forward -n pgao svc/pgao 8080:8080
curl http://localhost:8080/api/v1/clusters | jq
```

## Features

- Multi-cluster PostgreSQL monitoring
- Query analysis & optimization suggestions (pg_query_go v6)
- Real-time metrics: connections, cache hit ratio, replication lag
- REST API with health checks
- Kubernetes native with Terraform IaC

## What Data We Expose

**Per-Cluster Metrics** (`/api/v1/clusters/{id}/metrics`):
- **Connections**: Active vs Total (e.g., 10/100)
- **Performance**: Transactions/sec, Cache hit ratio (%)
- **I/O**: Disk read/write in KB
- **Health**: Lock waits, Deadlocks, Table bloat (%)
- **Replication**: Lag in milliseconds (for replicas)

**Cluster Configuration** (`/api/v1/clusters/{id}`):
- PostgreSQL version & settings (shared_buffers, max_connections, work_mem)
- Installed extensions (pg_stat_statements, pgcrypto, etc.)
- Available databases
- Replication topology (primary/replica status)

**Health Status** (`/api/v1/clusters/{id}/health`):
- Overall score (0-100)
- Active alerts (warnings/critical)
- Detected issues (low cache hit, high connections, bloat)

**Query Analysis** (`POST /api/v1/analyze`):
- Normalized SQL
- Parse tree structure
- Query fingerprint (ID)

<details>
<summary><b>API Endpoints</b></summary>

```bash
GET  /health                              # Health check
GET  /ready                               # Readiness (requires DB connections)
GET  /api/v1/clusters                     # List all clusters
GET  /api/v1/clusters/{id}                # Cluster details
GET  /api/v1/clusters/{id}/metrics        # Cluster metrics
POST /api/v1/analyze                      # Analyze SQL query
```

Example:
```bash
curl http://localhost:8080/api/v1/clusters | jq
curl -X POST http://localhost:8080/api/v1/analyze \
  -d '{"query":"SELECT * FROM users WHERE id = 1"}' | jq
```
</details>

<details>
<summary><b>Configuration</b></summary>

`config.yaml` with environment variable expansion:

```yaml
clusters:
  - id: "prod-1"
    host: "postgres.example.com"
    port: 5432
    user: "postgres"
    password: "${DATABASE_PASSWORD}"  # Expanded from env
    database: "postgres"
    ssl_mode: "require"

metrics:
  collection_interval: 60s
  enable_prometheus: true
```
</details>

<details>
<summary><b>Local Development</b></summary>

```bash
# Build
make build

# Test
make test
make lint

# Docker
make docker-build
make docker-run

# Deploy to K8s
cd terraform
terraform init
terraform apply -var='kube_context=kind-pgao-test' \
                -var='postgres_password=your-password'

# Load Testing (optional)
export DB_PASSWORD="your-password"  # Set password for scripts
./scripts/pgbench_load_test.sh      # Bash-based pgbench test
python3 scripts/advanced_load_test.py  # Python-based advanced test
```
</details>

<details>
<summary><b>Deployment Options</b></summary>

### KIND (Kubernetes IN Docker)
```bash
kind create cluster --name pgao-test
make docker-build
kind load docker-image pgao:latest --name pgao-test
cd terraform && terraform apply
```

### Existing Kubernetes
```bash
# Build and push image
make docker-build
docker tag pgao:latest your-registry/pgao:latest
docker push your-registry/pgao:latest

# Update terraform/main.tf with your image
terraform apply
```

### What gets deployed:
- 3 PostgreSQL clusters (6 pods): prod-cluster-1 (3 replicas), prod-cluster-2 (2), dev-cluster-1 (1)
- PGAO app (2 replicas) with auto-restart on failure
- All clusters pre-configured with pg_stat_statements
</details>

<details>
<summary><b>Troubleshooting</b></summary>

**PGAO pods not ready (0/1)?**
- Check logs: `kubectl logs -n pgao -l app=pgao`
- PostgreSQL might not be ready yet: `kubectl get pods -n postgres-clusters`
- Restart after PG is up: `kubectl rollout restart deployment/pgao -n pgao`

**Password authentication failed?**
- Verify secret: `kubectl get secret -n pgao pgao-secrets -o yaml`
- Check PG password: `kubectl get secret -n postgres-clusters prod-cluster-1-password -o jsonpath='{.data.password}' | base64 -d`

**Connection refused errors?**
- Normal during initial startup (PG takes 30-60s to initialize)
- Wait for all PG pods to be Running (1/1): `kubectl get pods -n postgres-clusters -w`
- Then restart PGAO: `kubectl rollout restart deployment/pgao -n pgao`
</details>

## Requirements

- Go 1.23+ (with CGO for pg_query_go v6)
- Docker
- kubectl + KIND/minikube/k3s
- Terraform


## License

MIT - See [LICENSE](LICENSE)

---

Built with [pg_query_go](https://github.com/pganalyze/pg_query_go) by pganalyze
