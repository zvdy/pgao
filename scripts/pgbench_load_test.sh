#!/bin/bash
# PostgreSQL Load Testing Script using pgbench
# Generates realistic workload to test PGAO monitoring

set -e

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${BLUE}======================================"
echo "PGAO Load Testing Script"
echo "======================================${NC}"

# Configuration
NAMESPACE="postgres-clusters"
SCALE_FACTOR=50  # Number of accounts (scaling factor for pgbench)
CLIENTS=10       # Number of concurrent clients
JOBS=4           # Number of threads
DURATION=60      # Test duration in seconds
PASSWORD="${DB_PASSWORD:-changeme}"  # Use environment variable or default

# Get list of PostgreSQL clusters
CLUSTERS=$(kubectl get statefulsets -n $NAMESPACE -o jsonpath='{.items[*].metadata.name}')

if [ -z "$CLUSTERS" ]; then
    echo -e "${RED}Error: No PostgreSQL clusters found in namespace $NAMESPACE${NC}"
    exit 1
fi

echo -e "${GREEN}Found clusters: $CLUSTERS${NC}"
echo

# Function to run pgbench on a cluster
run_pgbench() {
    local cluster=$1
    local pod="${cluster}-0"
    
    echo -e "${BLUE}======================================"
    echo "Testing cluster: $cluster"
    echo "======================================${NC}"
    
    # Check if pod is ready
    if ! kubectl get pod -n $NAMESPACE $pod &>/dev/null; then
        echo -e "${RED}Pod $pod not found${NC}"
        return 1
    fi
    
    # Initialize pgbench database
    echo -e "${YELLOW}[1/4] Initializing pgbench database (scale factor: $SCALE_FACTOR)...${NC}"
    kubectl exec -n $NAMESPACE $pod -- \
        pgbench -i -s $SCALE_FACTOR -U postgres postgres || {
        echo -e "${RED}Failed to initialize pgbench${NC}"
        return 1
    }
    
    # Create indexes for better query performance
    echo -e "${YELLOW}[2/4] Creating additional indexes...${NC}"
    kubectl exec -n $NAMESPACE $pod -- \
        psql -U postgres -d postgres -c "
        CREATE INDEX IF NOT EXISTS idx_pgbench_accounts_aid ON pgbench_accounts(aid);
        CREATE INDEX IF NOT EXISTS idx_pgbench_branches_bid ON pgbench_branches(bid);
        CREATE INDEX IF NOT EXISTS idx_pgbench_tellers_tid ON pgbench_tellers(tid);
        ANALYZE;
        " || echo -e "${YELLOW}Indexes might already exist${NC}"
    
    # Run mixed workload test
    echo -e "${YELLOW}[3/4] Running TPC-B benchmark (${DURATION}s, ${CLIENTS} clients, ${JOBS} threads)...${NC}"
    kubectl exec -n $NAMESPACE $pod -- \
        pgbench -c $CLIENTS -j $JOBS -T $DURATION -P 10 -r -U postgres postgres
    
    # Run custom query workload
    echo -e "${YELLOW}[4/4] Running custom query workload...${NC}"
    kubectl exec -n $NAMESPACE $pod -- bash -c "cat > /tmp/custom_queries.sql << 'EOF'
-- Heavy SELECT with aggregation
SELECT b.bid, COUNT(a.aid), AVG(a.abalance), MAX(a.abalance), MIN(a.abalance)
FROM pgbench_accounts a
JOIN pgbench_branches b ON a.bid = b.bid
GROUP BY b.bid;

-- Index scan queries
SELECT * FROM pgbench_accounts WHERE aid BETWEEN 1 AND 100;

-- Sequential scan
SELECT COUNT(*) FROM pgbench_accounts WHERE abalance > 0;

-- Join query
SELECT t.tid, t.tbalance, b.bbalance
FROM pgbench_tellers t
JOIN pgbench_branches b ON t.bid = b.bid
WHERE t.tbalance > 0;

-- Update query
UPDATE pgbench_accounts SET abalance = abalance + 100 WHERE aid = 1;

-- Complex aggregation
SELECT 
    bid,
    COUNT(*) as account_count,
    AVG(abalance) as avg_balance,
    STDDEV(abalance) as stddev_balance,
    SUM(CASE WHEN abalance > 0 THEN 1 ELSE 0 END) as positive_balance_count
FROM pgbench_accounts
GROUP BY bid
HAVING COUNT(*) > 100;
EOF
"
    
    # Run custom queries
    for i in {1..10}; do
        kubectl exec -n $NAMESPACE $pod -- \
            psql -U postgres -d postgres -f /tmp/custom_queries.sql &>/dev/null || true
        sleep 1
    done
    
    echo -e "${GREEN}✓ Load test completed for $cluster${NC}"
    echo
}

# Function to get current metrics from PGAO
check_pgao_metrics() {
    echo -e "${BLUE}======================================"
    echo "Fetching PGAO Metrics"
    echo "======================================${NC}"
    
    # Check if port-forward is running
    if ! pgrep -f "port-forward.*pgao.*8080" > /dev/null; then
        echo -e "${YELLOW}Starting port-forward to PGAO service...${NC}"
        kubectl port-forward -n pgao svc/pgao 8080:8080 &>/dev/null &
        sleep 3
    fi
    
    # Fetch cluster metrics
    echo -e "${YELLOW}Cluster Status:${NC}"
    curl -s http://localhost:8080/api/v1/clusters | jq -r '.[] | "\(.id): \(.name) - \(.status)"' 2>/dev/null || echo "Could not fetch cluster status"
    
    echo
    echo -e "${YELLOW}Metrics for prod-cluster-1:${NC}"
    curl -s http://localhost:8080/api/v1/clusters/prod-cluster-1/metrics | jq '.' 2>/dev/null || echo "Could not fetch metrics"
    
    echo
    echo -e "${YELLOW}Active Queries (if any):${NC}"
    kubectl exec -n $NAMESPACE prod-cluster-1-0 -- \
        psql -U postgres -d postgres -c "
        SELECT pid, usename, datname, state, 
               EXTRACT(EPOCH FROM (now() - query_start)) as duration_sec,
               LEFT(query, 80) as query
        FROM pg_stat_activity 
        WHERE state != 'idle' 
          AND pid != pg_backend_pid()
        ORDER BY duration_sec DESC;
        " 2>/dev/null || true
}

# Function to show database statistics
show_db_stats() {
    local cluster=$1
    local pod="${cluster}-0"
    
    echo -e "${BLUE}======================================"
    echo "Database Statistics: $cluster"
    echo "======================================${NC}"
    
    echo -e "${YELLOW}Connection stats:${NC}"
    kubectl exec -n $NAMESPACE $pod -- \
        psql -U postgres -d postgres -c "
        SELECT 
            datname,
            numbackends as connections,
            xact_commit as commits,
            xact_rollback as rollbacks,
            blks_read as disk_reads,
            blks_hit as buffer_hits,
            ROUND(100.0 * blks_hit / NULLIF(blks_hit + blks_read, 0), 2) as cache_hit_ratio
        FROM pg_stat_database 
        WHERE datname = 'postgres';
        " 2>/dev/null || true
    
    echo
    echo -e "${YELLOW}Top 5 tables by activity:${NC}"
    kubectl exec -n $NAMESPACE $pod -- \
        psql -U postgres -d postgres -c "
        SELECT 
            schemaname || '.' || relname as table_name,
            seq_scan + idx_scan as total_scans,
            n_tup_ins as inserts,
            n_tup_upd as updates,
            n_live_tup as live_tuples,
            n_dead_tup as dead_tuples
        FROM pg_stat_user_tables 
        ORDER BY total_scans DESC 
        LIMIT 5;
        " 2>/dev/null || true
    
    echo
    echo -e "${YELLOW}Query statistics (pg_stat_statements):${NC}"
    kubectl exec -n $NAMESPACE $pod -- \
        psql -U postgres -d postgres -c "
        SELECT 
            calls,
            ROUND(mean_exec_time::numeric, 2) as avg_ms,
            ROUND(total_exec_time::numeric, 2) as total_ms,
            LEFT(query, 80) as query
        FROM pg_stat_statements 
        WHERE query NOT LIKE '%pg_stat_statements%'
        ORDER BY mean_exec_time DESC 
        LIMIT 5;
        " 2>/dev/null || true
}

# Main execution
main() {
    echo -e "${GREEN}Starting load test on PostgreSQL clusters...${NC}"
    echo "Configuration:"
    echo "  - Scale Factor: $SCALE_FACTOR"
    echo "  - Clients: $CLIENTS"
    echo "  - Jobs: $JOBS"
    echo "  - Duration: ${DURATION}s"
    echo
    
    # Run load tests on each cluster
    for cluster in $CLUSTERS; do
        run_pgbench $cluster
        
        # Show statistics after load test
        show_db_stats $cluster
        
        echo
        echo -e "${YELLOW}Waiting 5 seconds before next cluster...${NC}"
        sleep 5
    done
    
    echo -e "${GREEN}======================================"
    echo "All load tests completed!"
    echo "======================================${NC}"
    
    # Wait for metrics to be collected
    echo -e "${YELLOW}Waiting 10 seconds for PGAO to collect fresh metrics...${NC}"
    sleep 10
    
    # Check PGAO metrics
    check_pgao_metrics
    
    echo
    echo -e "${GREEN}======================================"
    echo "Load Testing Complete!"
    echo "======================================${NC}"
    echo
    echo "You can now check:"
    echo "  1. Real-time metrics: curl http://localhost:8080/api/v1/clusters | jq"
    echo "  2. Specific cluster: curl http://localhost:8080/api/v1/clusters/prod-cluster-1/metrics | jq"
    echo "  3. Health status: curl http://localhost:8080/api/v1/clusters/prod-cluster-1/health | jq"
    echo "  4. Analyze queries: curl -X POST http://localhost:8080/api/v1/analyze -d '{\"query\":\"SELECT * FROM pgbench_accounts WHERE aid = 1\"}' | jq"
}

# Run main function
main
