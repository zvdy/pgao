// Typed thin wrapper around the pgao API.
// All requests automatically attach Authorization: Bearer <key> when
// localStorage has one set by App.tsx.

const STORAGE_KEY = 'pgao.apiKey';

function getApiKey(): string {
  return localStorage.getItem(STORAGE_KEY) ?? '';
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
    this.name = 'ApiError';
  }
}

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  const key = getApiKey();
  if (key) headers.set('Authorization', `Bearer ${key}`);
  if (init?.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }
  const res = await fetch(path, { ...init, headers });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      const body = await res.json();
      if (body?.error) msg = body.error;
    } catch {
      // body may be empty / non-json on some 4xx
    }
    throw new ApiError(res.status, msg);
  }
  return (await res.json()) as T;
}

// Schemas mirror api/openapi.yaml — kept hand-written and tiny instead of
// pulling in a generator. The fields we actually render are typed; the
// rest stay loose so the API can grow without UI changes.

export interface Cluster {
  id: string;
  name?: string;
  status?: string;
  configuration?: Record<string, unknown>;
  metrics?: Record<string, number>;
}

export interface Readiness {
  status: 'ready' | 'not_ready';
  healthy: number;
  total: number;
  reason?: string;
  clusters: Record<string, string>;
}

export interface Metrics {
  cluster_id: string;
  timestamp: string;
  connections_active: number;
  connections_total: number;
  transactions_per_sec: number;
  cache_hit_ratio: number;
  disk_io_read: number;
  disk_io_write: number;
  lock_waits: number;
  deadlock_count: number;
  replication_lag_ms: number;
  table_bloat_pct: number;
}

export interface QueryMetrics {
  query_id: string;
  query: string;
  call_count: number;
  mean_exec_time_ms: number;
  total_exec_time?: number;
  rows_returned?: number;
}

export interface TableMetrics {
  schema: string;
  table: string;
  seq_scan: number;
  idx_scan: number;
  live_tuples: number;
  dead_tuples: number;
  last_vacuum?: string | null;
  last_autovacuum?: string | null;
}

export const api = {
  ready: () => call<Readiness>('/ready'),
  listClusters: () => call<Cluster[]>('/api/v1/clusters'),
  getCluster: (id: string) => call<Cluster>(`/api/v1/clusters/${encodeURIComponent(id)}`),
  getMetrics: (id: string) =>
    call<Metrics>(`/api/v1/clusters/${encodeURIComponent(id)}/metrics`),
  getSlowQueries: (id: string, limit = 20, orderBy = 'total_exec_time') =>
    call<QueryMetrics[]>(
      `/api/v1/clusters/${encodeURIComponent(id)}/queries?limit=${limit}&order_by=${orderBy}`,
    ),
  getTableMetrics: (id: string, limit = 20) =>
    call<TableMetrics[]>(
      `/api/v1/clusters/${encodeURIComponent(id)}/tables?limit=${limit}`,
    ),
};
