import { Outlet, Link, useLocation } from 'react-router-dom';
import { useEffect, useState } from 'react';

// Persist the API key in localStorage so reloads don't lose it. The
// header sets it once; everything else reads via getApiKey() in api.ts.
const STORAGE_KEY = 'pgao.apiKey';

export default function App() {
  const loc = useLocation();
  const [apiKey, setApiKey] = useState(() => localStorage.getItem(STORAGE_KEY) ?? '');

  useEffect(() => {
    if (apiKey) localStorage.setItem(STORAGE_KEY, apiKey);
    else localStorage.removeItem(STORAGE_KEY);
  }, [apiKey]);

  return (
    <div className="app">
      <header className="topbar">
        <Link to="/" className="brand">pgao</Link>
        <nav>
          <Link to="/" className={loc.pathname === '/' ? 'active' : ''}>
            Overview
          </Link>
          <Link
            to="/analyze"
            className={loc.pathname.startsWith('/analyze') ? 'active' : ''}
          >
            Analyzer
          </Link>
        </nav>
        <div className="auth">
          <input
            type="password"
            placeholder="API key (optional)"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            aria-label="API key"
          />
        </div>
      </header>
      <main className="content">
        <Outlet />
      </main>
      <footer className="bottom">
        <a href="/metrics" target="_blank" rel="noreferrer">/metrics</a>
        <span>·</span>
        <a href="https://github.com/zvdy/pgao" target="_blank" rel="noreferrer">github</a>
      </footer>
    </div>
  );
}
