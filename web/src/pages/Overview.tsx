import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { api } from '../api';
import StatusBadge from '../components/StatusBadge';

export default function Overview() {
  const ready = useQuery({ queryKey: ['ready'], queryFn: api.ready });
  const clusters = useQuery({ queryKey: ['clusters'], queryFn: api.listClusters });

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
              <th></th>
            </tr>
          </thead>
          <tbody>
            {(clusters.data ?? []).map((c) => {
              const cfg = (c.configuration ?? {}) as Record<string, unknown>;
              const version = String(cfg.version ?? 'unknown');
              const dbs = Array.isArray(cfg.databases) ? cfg.databases.length : 0;
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
                    <Link to={`/clusters/${encodeURIComponent(c.id)}`}>details →</Link>
                  </td>
                </tr>
              );
            })}
            {(clusters.data ?? []).length === 0 && !clusters.isLoading && (
              <tr>
                <td colSpan={5} className="muted center">
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
