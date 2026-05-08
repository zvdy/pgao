import { useMutation } from '@tanstack/react-query';
import { useState } from 'react';
import { api, ApiError, type QueryAnalysis } from '../api';

const SAMPLE = `SELECT u.id, u.email, COUNT(o.id) AS order_count
FROM users u
LEFT JOIN orders o ON o.user_id = u.id
WHERE u.created_at > NOW() - INTERVAL '30 days'
GROUP BY u.id, u.email
ORDER BY order_count DESC
LIMIT 100;`;

export default function Analyzer() {
  const [query, setQuery] = useState<string>(() => {
    return localStorage.getItem('pgao.lastQuery') ?? SAMPLE;
  });

  const m = useMutation({
    mutationFn: (q: string) => api.analyzeQuery(q),
    onSuccess: () => localStorage.setItem('pgao.lastQuery', query),
  });

  return (
    <div className="analyzer">
      <h1>SQL analyzer</h1>
      <p className="muted">
        Static analysis via libpg_query. Pgao does <em>not</em> execute the
        statement against any cluster — only the parser runs.
      </p>

      <form
        className="analyzer-form"
        onSubmit={(e) => {
          e.preventDefault();
          if (query.trim()) m.mutate(query);
        }}
      >
        <textarea
          spellCheck={false}
          value={query}
          rows={10}
          onChange={(e) => setQuery(e.target.value)}
          aria-label="SQL to analyse"
        />
        <div className="analyzer-actions">
          <button type="submit" disabled={m.isPending || !query.trim()}>
            {m.isPending ? 'Analyzing…' : 'Analyze'}
          </button>
          <button
            type="button"
            className="secondary"
            onClick={() => setQuery(SAMPLE)}
          >
            Reset
          </button>
        </div>
      </form>

      {m.error instanceof ApiError && (
        <div className="error">
          {m.error.status} — {m.error.message}
        </div>
      )}
      {m.error && !(m.error instanceof ApiError) && (
        <div className="error">{String(m.error)}</div>
      )}

      {m.data && <AnalysisView analysis={m.data} />}
    </div>
  );
}

function AnalysisView({ analysis }: { analysis: QueryAnalysis }) {
  const summary: Array<[string, string]> = [
    ['Type', analysis.query_type || '—'],
    ['Complexity', analysis.complexity || '—'],
    ['Estimated cost', String(analysis.estimated_cost ?? 0)],
    ['Tables', analysis.tables.join(', ') || '—'],
    ['Has join', analysis.has_join ? `yes (${analysis.join_type ?? 'unknown'})` : 'no'],
    ['Has subquery', analysis.has_subquery ? 'yes' : 'no'],
    ['Has aggregate', analysis.has_aggregate ? 'yes' : 'no'],
    ['Has window fn', analysis.has_window_function ? 'yes' : 'no'],
  ];
  return (
    <div className="analysis">
      <section>
        <h2>Summary</h2>
        <dl className="kv">
          {summary.map(([k, v]) => (
            <div key={k}>
              <dt>{k}</dt>
              <dd>{v}</dd>
            </div>
          ))}
        </dl>
      </section>

      {analysis.normalized && (
        <section>
          <h2>Normalized</h2>
          <pre className="code">{analysis.normalized}</pre>
        </section>
      )}

      {analysis.suggestions.length > 0 && (
        <section>
          <h2>Suggestions</h2>
          <ul className="alerts">
            {analysis.suggestions.map((s, i) => (
              <li key={i} className={`alert sev-${s.severity}`}>
                <div className="alert-row">
                  <span className={`badge sev-${s.severity}`}>{s.severity}</span>
                  <strong className="alert-title">{s.message}</strong>
                </div>
                {s.impact && <p className="alert-desc">Impact: {s.impact}</p>}
                {s.recommended && (
                  <pre className="code">{s.recommended}</pre>
                )}
                <div className="muted alert-meta">
                  {s.type} · confidence {(s.confidence * 100).toFixed(0)}%
                </div>
              </li>
            ))}
          </ul>
        </section>
      )}

      {analysis.warnings.length > 0 && (
        <section>
          <h2>Warnings</h2>
          <ul>
            {analysis.warnings.map((w, i) => (
              <li key={i}>{w}</li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}
