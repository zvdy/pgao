import { useQueries, useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { api, type Alert } from '../api';
import StatusBadge from '../components/StatusBadge';

export default function Overview() {
  const ready = useQuery({ queryKey: ['ready'], queryFn: api.ready });
  const clusters = useQuery({ queryKey: ['clusters'], queryFn: api.listClusters });

  // Fan out an /alerts query per cluster so we can render the alert
  // count column without any backend changes. TanStack Query dedupes
  // these against the per-cluster page if the user opens one.
  const alertResults = useQueries({
    queries: (clusters.data ?? []).map((c) => ({
      queryKey: ['alerts', c.id],
      queryFn: () => api.getAlerts(c.id),
      retry: false,
    })),
  });
  const alertsByCluster = new Map<string, Alert[]>();
  (clusters.data ?? []).forEach((c, i) => {
    if (alertResults[i]?.data) {
      alertsByCluster.set(c.id, alertResults[i]!.data);
    }
  });
  const fleetCritical = Array.from(alertsByCluster.values())
    .flat()
    .filter((a) => a.severity === 'critical' && (!a.status || a.status === 'active'))
    .length;

  if (ready.error || clusters.error) {
    return (
      <div className="error">
        Failed to load: {String((ready.error ?? clusters.error) as Error)}.
      </div>
    );
  }

  return (
    <div className="overview">
      <section className="summary">
        <h1>Fleet</h1>
        <p>
          {ready.data
            ? `${ready.data.healthy} / ${ready.data.total} clusters healthy`
            : 'loading…'}
          {fleetCritical > 0 && (
            <>
              {' '}· <span className="badge sev-critical">{fleetCritical} critical alert{fleetCritical === 1 ? '' : 's'}</span>
            </>
          )}
        </p>
      </section>

      <section>
        <h2>Clusters</h2>
        <table className="grid">
          <thead>
            <tr>
              <th>Cluster</th>
              <th>Status</th>
              <th>Version</th>
              <th>Databases</th>
              <th>Alerts</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {(clusters.data ?? []).map((c) => {
              const cfg = (c.configuration ?? {}) as Record<string, unknown>;
              const version = String(cfg.version ?? 'unknown');
              const dbs = Array.isArray(cfg.databases) ? cfg.databases.length : 0;
              const alerts = (alertsByCluster.get(c.id) ?? []).filter(
                (a) => !a.status || a.status === 'active',
              );
              const worst = worstSeverity(alerts);
              return (
                <tr key={c.id}>
                  <td>
                    <Link to={`/clusters/${encodeURIComponent(c.id)}`}>
                      {c.name ?? c.id}
                    </Link>
                  </td>
                  <td>
                    <StatusBadge status={c.status ?? 'unknown'} />
                  </td>
                  <td className="muted">{version.split(' ').slice(0, 2).join(' ')}</td>
                  <td className="muted">{dbs}</td>
                  <td>
                    {alerts.length === 0 ? (
                      <span className="muted">—</span>
                    ) : (
                      <span className={`badge sev-${worst}`}>{alerts.length}</span>
                    )}
                  </td>
                  <td>
                    <Link to={`/clusters/${encodeURIComponent(c.id)}`}>details →</Link>
                  </td>
                </tr>
              );
            })}
            {(clusters.data ?? []).length === 0 && !clusters.isLoading && (
              <tr>
                <td colSpan={6} className="muted center">
                  No clusters registered yet.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </section>
    </div>
  );
}

function worstSeverity(alerts: Alert[]): string {
  const order = ['critical', 'high', 'medium', 'low', 'info'];
  for (const level of order) {
    if (alerts.some((a) => a.severity === level)) return level;
  }
  return 'info';
}
