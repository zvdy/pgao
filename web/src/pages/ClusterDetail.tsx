import { Link, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import { api, ApiError, type Alert, type AlertSeverity } from '../api';
import StatusBadge from '../components/StatusBadge';

type Tab = 'metrics' | 'queries' | 'tables' | 'alerts';

const TABS: readonly Tab[] = ['metrics', 'queries', 'tables', 'alerts'] as const;

export default function ClusterDetail() {
  const { id = '' } = useParams<{ id: string }>();
  const [tab, setTab] = useState<Tab>('metrics');

  const cluster = useQuery({
    queryKey: ['cluster', id],
    queryFn: () => api.getCluster(id),
  });

  // Lightweight alert count for the tab badge — same query the Alerts
  // panel uses, so TanStack Query dedupes the request.
  const alerts = useQuery({
    queryKey: ['alerts', id],
    queryFn: () => api.getAlerts(id),
    retry: false,
  });
  const activeAlertCount = (alerts.data ?? []).filter(
    (a) => !a.status || a.status === 'active',
  ).length;

  if (cluster.isLoading) return <div className="muted">loading…</div>;
  if (cluster.error) return <div className="error">{String(cluster.error)}</div>;

  return (
    <div className="cluster-detail">
      <p className="crumbs">
        <Link to="/">Overview</Link> / <strong>{id}</strong>
      </p>

      <header className="cluster-head">
        <h1>{cluster.data?.name ?? id}</h1>
        <StatusBadge status={cluster.data?.status ?? 'unknown'} />
      </header>

      <nav className="tabs" role="tablist">
        {TABS.map((t) => (
          <button
            key={t}
            role="tab"
            aria-selected={tab === t}
            className={tab === t ? 'active' : ''}
            onClick={() => setTab(t)}
          >
            {t}
            {t === 'alerts' && activeAlertCount > 0 && (
              <span className="pill bad">{activeAlertCount}</span>
            )}
          </button>
        ))}
      </nav>

      {tab === 'metrics' && <MetricsPanel id={id} />}
      {tab === 'queries' && <SlowQueriesPanel id={id} />}
      {tab === 'tables' && <TablesPanel id={id} />}
      {tab === 'alerts' && <AlertsPanel id={id} />}
    </div>
  );
}

function MetricsPanel({ id }: { id: string }) {
  const m = useQuery({ queryKey: ['metrics', id], queryFn: () => api.getMetrics(id) });
  if (m.error) return <div className="error">{String(m.error)}</div>;
  const d = m.data;
  if (!d) return <div className="muted">loading…</div>;
  const tiles: Array<[string, string]> = [
    ['Connections', `${d.connections_active} / ${d.connections_total}`],
    ['TPS', d.transactions_per_sec.toFixed(1)],
    ['Cache hit', `${d.cache_hit_ratio.toFixed(2)}%`],
    ['Replication lag', `${d.replication_lag_ms} ms`],
    ['Lock waits', String(d.lock_waits)],
    ['Deadlocks', String(d.deadlock_count)],
    ['Disk read', `${d.disk_io_read.toFixed(1)} KB/s`],
    ['Disk write', `${d.disk_io_write.toFixed(1)} KB/s`],
    ['Bloat', `${d.table_bloat_pct.toFixed(1)}%`],
  ];
  return (
    <div className="tiles">
      {tiles.map(([label, value]) => (
        <div key={label} className="tile">
          <div className="tile-label">{label}</div>
          <div className="tile-value">{value}</div>
        </div>
      ))}
    </div>
  );
}

function SlowQueriesPanel({ id }: { id: string }) {
  const q = useQuery({
    queryKey: ['queries', id],
    queryFn: () => api.getSlowQueries(id),
    retry: false,
  });
  if (q.error instanceof ApiError && q.error.status === 412) {
    return (
      <div className="warning">
        <strong>pg_stat_statements not installed.</strong> {q.error.message}
      </div>
    );
  }
  if (q.error) return <div className="error">{String(q.error)}</div>;
  const rows = q.data ?? [];
  if (rows.length === 0) return <div className="muted">no slow queries recorded</div>;
  return (
    <table className="grid">
      <thead>
        <tr>
          <th>Calls</th>
          <th>Mean (ms)</th>
          <th>Query</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => (
          <tr key={r.query_id}>
            <td>{r.call_count}</td>
            <td>{r.mean_exec_time_ms.toFixed(2)}</td>
            <td className="query">{r.query}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function TablesPanel({ id }: { id: string }) {
  const q = useQuery({
    queryKey: ['tables', id],
    queryFn: () => api.getTableMetrics(id),
  });
  if (q.error) return <div className="error">{String(q.error)}</div>;
  const rows = q.data ?? [];
  if (rows.length === 0) return <div className="muted">no user tables</div>;
  return (
    <table className="grid">
      <thead>
        <tr>
          <th>Table</th>
          <th>seq scan</th>
          <th>idx scan</th>
          <th>live</th>
          <th>dead</th>
          <th>last (auto)vacuum</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((t) => (
          <tr key={`${t.schema}.${t.table}`}>
            <td>
              <span className="muted">{t.schema}.</span>
              {t.table}
            </td>
            <td>{t.seq_scan}</td>
            <td>{t.idx_scan}</td>
            <td>{t.live_tuples}</td>
            <td>{t.dead_tuples}</td>
            <td className="muted">{t.last_autovacuum ?? t.last_vacuum ?? '—'}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function AlertsPanel({ id }: { id: string }) {
  const q = useQuery({ queryKey: ['alerts', id], queryFn: () => api.getAlerts(id) });
  if (q.error) return <div className="error">{String(q.error)}</div>;
  const rows = q.data ?? [];
  if (rows.length === 0) {
    return <div className="muted">no active alerts</div>;
  }

  // Sort by severity (critical first) then most recent.
  const order: Record<AlertSeverity, number> = {
    critical: 0,
    high: 1,
    medium: 2,
    low: 3,
    info: 4,
  };
  const sorted = [...rows].sort((a, b) => {
    const sa = order[a.severity] ?? 99;
    const sb = order[b.severity] ?? 99;
    if (sa !== sb) return sa - sb;
    return (b.timestamp ?? '').localeCompare(a.timestamp ?? '');
  });

  return (
    <ul className="alerts">
      {sorted.map((a, i) => (
        <li key={a.id ?? `${a.title}-${i}`} className={`alert sev-${a.severity}`}>
          <div className="alert-row">
            <span className={`badge sev-${a.severity}`}>{a.severity}</span>
            <strong className="alert-title">{a.title}</strong>
            {a.metric && <span className="muted">· {a.metric}</span>}
          </div>
          <p className="alert-desc">{a.description}</p>
          {(a.actions ?? []).length > 0 && (
            <ul className="alert-actions">
              {(a.actions ?? []).map((act, j) => (
                <li key={j}>{act}</li>
              ))}
            </ul>
          )}
          <AlertContext alert={a} />
        </li>
      ))}
    </ul>
  );
}

function AlertContext({ alert }: { alert: Alert }) {
  const bits: string[] = [];
  if (alert.threshold !== undefined && alert.current_value !== undefined) {
    bits.push(`current ${alert.current_value} vs threshold ${alert.threshold}`);
  }
  if (alert.timestamp) bits.push(new Date(alert.timestamp).toLocaleString());
  if (bits.length === 0) return null;
  return <div className="muted alert-meta">{bits.join(' · ')}</div>;
}
