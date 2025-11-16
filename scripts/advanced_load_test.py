#!/usr/bin/env python3
import psycopg2
import requests
import time
import json
import random
import os
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime
from typing import Dict, List

# Configuration
PGAO_API = "http://localhost:8080/api/v1"
DB_PASSWORD = os.getenv("DB_PASSWORD", "changeme")  # Use environment variable or default

CLUSTERS = [
    {
        "id": "prod-cluster-1",
        "host": "localhost",
        "port": 5432,
        "user": "postgres",
        "password": DB_PASSWORD,
        "database": "postgres"
    },
    {
        "id": "prod-cluster-2",
        "host": "localhost",
        "port": 5433,
        "user": "postgres",
        "password": DB_PASSWORD,
        "database": "postgres"
    },
    {
        "id": "dev-cluster-1",
        "host": "localhost",
        "port": 5434,
        "user": "postgres",
        "password": DB_PASSWORD,
        "database": "postgres"
    }
]

# Workload patterns
QUERIES = {
    "oltp_read": [
        "SELECT * FROM pgbench_accounts WHERE aid = %s;",
        "SELECT * FROM pgbench_tellers WHERE tid = %s;",
        "SELECT * FROM pgbench_branches WHERE bid = %s;",
    ],
    "oltp_write": [
        "UPDATE pgbench_accounts SET abalance = abalance + %s WHERE aid = %s;",
        "INSERT INTO pgbench_history (tid, bid, aid, delta, mtime) VALUES (%s, %s, %s, %s, NOW());",
    ],
    "analytics": [
        """
        SELECT b.bid, COUNT(a.aid) as accounts, 
               AVG(a.abalance) as avg_balance,
               SUM(a.abalance) as total_balance
        FROM pgbench_accounts a
        JOIN pgbench_branches b ON a.bid = b.bid
        GROUP BY b.bid;
        """,
        """
        SELECT COUNT(*) as total_accounts,
               SUM(CASE WHEN abalance > 0 THEN 1 ELSE 0 END) as positive,
               SUM(CASE WHEN abalance < 0 THEN 1 ELSE 0 END) as negative
        FROM pgbench_accounts;
        """,
    ],
    "slow_queries": [
        "SELECT * FROM pgbench_accounts ORDER BY abalance LIMIT 100;",
        "SELECT DISTINCT abalance FROM pgbench_accounts ORDER BY abalance;",
    ]
}


class LoadGenerator:
    def __init__(self, cluster_config: Dict):
        self.config = cluster_config
        self.conn = None
        self.connect()
    
    def connect(self):
        """Establish database connection"""
        try:
            self.conn = psycopg2.connect(
                host=self.config["host"],
                port=self.config["port"],
                user=self.config["user"],
                password=self.config["password"],
                database=self.config["database"]
            )
            self.conn.autocommit = True
        except Exception as e:
            print(f"⚠️  Failed to connect to {self.config['id']}: {e}")
    
    def execute_query(self, query: str, params: tuple = None) -> bool:
        """Execute a query and return success status"""
        if not self.conn:
            return False
        
        try:
            with self.conn.cursor() as cur:
                cur.execute(query, params)
                return True
        except Exception as e:
            print(f"❌ Query failed: {str(e)[:50]}")
            return False
    
    def generate_oltp_read_load(self, duration: int = 30):
        """Generate OLTP read workload"""
        start = time.time()
        count = 0
        while time.time() - start < duration:
            query = random.choice(QUERIES["oltp_read"])
            params = (random.randint(1, 1000),)
            if self.execute_query(query, params):
                count += 1
            time.sleep(random.uniform(0.01, 0.1))
        return count
    
    def generate_oltp_write_load(self, duration: int = 30):
        """Generate OLTP write workload"""
        start = time.time()
        count = 0
        while time.time() - start < duration:
            query = random.choice(QUERIES["oltp_write"])
            if "UPDATE" in query:
                params = (random.randint(-100, 100), random.randint(1, 1000))
            else:
                params = (random.randint(1, 10), random.randint(1, 10), 
                         random.randint(1, 1000), random.randint(-100, 100))
            
            if self.execute_query(query, params):
                count += 1
            time.sleep(random.uniform(0.05, 0.2))
        return count
    
    def generate_analytics_load(self, duration: int = 30):
        """Generate analytics workload"""
        start = time.time()
        count = 0
        while time.time() - start < duration:
            query = random.choice(QUERIES["analytics"])
            if self.execute_query(query):
                count += 1
            time.sleep(random.uniform(1, 3))
        return count
    
    def close(self):
        """Close database connection"""
        if self.conn:
            self.conn.close()


class PGAOMonitor:
    def __init__(self, api_url: str):
        self.api_url = api_url
    
    def get_clusters(self) -> List[Dict]:
        """Get list of monitored clusters"""
        try:
            response = requests.get(f"{self.api_url}/clusters", timeout=5)
            return response.json() if response.status_code == 200 else []
        except Exception as e:
            print(f"⚠️  Failed to fetch clusters: {e}")
            return []
    
    def get_cluster_metrics(self, cluster_id: str) -> Dict:
        """Get metrics for a specific cluster"""
        try:
            response = requests.get(f"{self.api_url}/clusters/{cluster_id}/metrics", timeout=5)
            return response.json() if response.status_code == 200 else {}
        except Exception as e:
            print(f"⚠️  Failed to fetch metrics for {cluster_id}: {e}")
            return {}
    
    def get_cluster_health(self, cluster_id: str) -> Dict:
        """Get health status for a specific cluster"""
        try:
            response = requests.get(f"{self.api_url}/clusters/{cluster_id}/health", timeout=5)
            return response.json() if response.status_code == 200 else {}
        except Exception as e:
            return {"status": "unknown", "error": str(e)}
    
    def analyze_query(self, query: str) -> Dict:
        """Analyze a query using PGAO"""
        try:
            response = requests.post(
                f"{self.api_url}/analyze",
                json={"query": query},
                timeout=5
            )
            return response.json() if response.status_code == 200 else {}
        except Exception as e:
            print(f"⚠️  Failed to analyze query: {e}")
            return {}
    
    def display_metrics(self, cluster_id: str):
        """Display current metrics for a cluster"""
        metrics = self.get_cluster_metrics(cluster_id)
        if not metrics:
            print(f"❌ No metrics available for {cluster_id}")
            return
        
        print(f"\n📊 Metrics for {cluster_id}:")
        print(f"   ├─ Connections: {metrics.get('connections_active', 0)}/{metrics.get('connections_total', 0)}")
        print(f"   ├─ Cache Hit Ratio: {metrics.get('cache_hit_ratio', 0):.2f}%")
        print(f"   ├─ Transactions/sec: {metrics.get('transactions_per_sec', 0):.2f}")
        print(f"   ├─ Lock Waits: {metrics.get('lock_waits', 0)}")
        print(f"   ├─ Deadlocks: {metrics.get('deadlock_count', 0)}")
        print(f"   ├─ Replication Lag: {metrics.get('replication_lag_ms', 0)}ms")
        print(f"   └─ Table Bloat: {metrics.get('table_bloat_pct', 0):.2f}%")


def run_load_test(duration: int = 60):
    """Run comprehensive load test"""
    print("🚀 Starting Advanced Load Test...")
    print(f"Duration: {duration} seconds")
    print(f"Target clusters: {len(CLUSTERS)}")
    print()
    
    # Initialize load generators
    generators = []
    for cluster in CLUSTERS:
        gen = LoadGenerator(cluster)
        generators.append((cluster["id"], gen))
    
    # Initialize PGAO monitor
    monitor = PGAOMonitor(PGAO_API)
    
    # Display initial cluster status
    print("📋 Initial Cluster Status:")
    clusters = monitor.get_clusters()
    for cluster in clusters:
        print(f"   ├─ {cluster.get('id')}: {cluster.get('name')} - {cluster.get('status')}")
    print()
    
    # Run workload in parallel
    with ThreadPoolExecutor(max_workers=len(generators) * 3) as executor:
        futures = []
        
        for cluster_id, gen in generators:
            # Submit different workload types
            futures.append(executor.submit(gen.generate_oltp_read_load, duration))
            futures.append(executor.submit(gen.generate_oltp_write_load, duration))
            futures.append(executor.submit(gen.generate_analytics_load, duration))
        
        # Monitor progress
        start_time = time.time()
        last_update = start_time
        
        while time.time() - start_time < duration:
            elapsed = time.time() - last_update
            if elapsed >= 10:  # Update every 10 seconds
                print(f"\n⏱️  Progress: {int(time.time() - start_time)}s / {duration}s")
                
                # Display metrics for each cluster
                for cluster_id, _ in generators:
                    monitor.display_metrics(cluster_id)
                
                last_update = time.time()
            
            time.sleep(1)
        
        # Wait for all tasks to complete
        total_queries = 0
        for future in as_completed(futures):
            total_queries += future.result()
    
    # Final metrics
    print("\n" + "="*60)
    print("🎉 Load Test Complete!")
    print("="*60)
    print(f"Total queries executed: {total_queries}")
    print(f"Average QPS: {total_queries / duration:.2f}")
    print()
    
    # Display final metrics
    print("📊 Final Metrics:")
    for cluster_id, gen in generators:
        monitor.display_metrics(cluster_id)
        gen.close()
    
    # Test query analysis
    print("\n🔍 Testing Query Analysis:")
    test_queries = [
        "SELECT * FROM pgbench_accounts WHERE aid = 1;",
        "SELECT COUNT(*) FROM pgbench_accounts WHERE abalance > 0;",
        "SELECT a.*, b.* FROM pgbench_accounts a JOIN pgbench_branches b ON a.bid = b.bid WHERE a.aid < 100;"
    ]
    
    for query in test_queries:
        print(f"\n   Query: {query[:60]}...")
        result = monitor.analyze_query(query)
        if result:
            print(f"   └─ Analysis: {json.dumps(result, indent=6)}")


if __name__ == "__main__":
    print("="*60)
    print("PGAO Advanced Load Generator & Monitor")
    print("="*60)
    print()
    
    # Check PGAO availability
    try:
        response = requests.get(f"{PGAO_API}/../health", timeout=2)
        if response.status_code != 200:
            print("⚠️  PGAO API not accessible. Make sure port-forward is running:")
            print("   kubectl port-forward -n pgao svc/pgao 8080:8080")
            print()
    except:
        print("⚠️  PGAO API not accessible. Make sure port-forward is running:")
        print("   kubectl port-forward -n pgao svc/pgao 8080:8080")
        print()
    
    # Port-forward instructions
    print("💡 Make sure PostgreSQL clusters are port-forwarded:")
    print("   kubectl port-forward -n postgres-clusters prod-cluster-1-0 5432:5432 &")
    print("   kubectl port-forward -n postgres-clusters prod-cluster-2-0 5433:5432 &")
    print("   kubectl port-forward -n postgres-clusters dev-cluster-1-0 5434:5432 &")
    print()
    
    input("Press Enter to start load test (or Ctrl+C to cancel)...")
    
    try:
        run_load_test(duration=60)
    except KeyboardInterrupt:
        print("\n\n⚠️  Load test interrupted by user")
    except Exception as e:
        print(f"\n\n❌ Error during load test: {e}")
