# pgao web UI

Single-page React + TypeScript dashboard, embedded into the Go binary
via `//go:embed` and served at `/` from the same listener as the API.

## Stack

- Vite + React 18 + TypeScript
- TanStack Query (15 s refetch, 10 s stale window)
- React Router (BrowserRouter — server-side SPA fallback in `embed.go`
  makes `/clusters/:id` deep links work after a full reload)
- Plain CSS (no Tailwind / shadcn yet to keep the bundle ≈ 65 kB gzipped)

## Pages (Phase 1)

- `/` — Overview: fleet readiness summary + cluster table with status,
  PostgreSQL version, database count.
- `/clusters/:id` — Cluster detail with tabs:
  - **metrics** — connection / TPS / cache hit / replication / locks /
    deadlocks / disk I/O / bloat tiles
  - **queries** — top slow queries from `pg_stat_statements`. Surfaces
    the 412 the API returns when the extension is missing as a clear
    "extension not installed" panel.
  - **tables** — `pg_stat_user_tables` activity with last (auto)vacuum.

## Development

```sh
# In one terminal: run the Go API on :8080
make build-go && CONFIG_PATH=config.yaml ./bin/pgao

# In another: hot-reload Vite at http://127.0.0.1:5173
make ui-dev
```

The dev server proxies `/api`, `/metrics`, `/ready`, `/health` to
`127.0.0.1:8080`. API key auth (if enabled on the backend) is configured
through the input in the top bar; the value is persisted to
`localStorage` and attached as `Authorization: Bearer <key>` on every
fetch.

## Production build

```sh
make ui          # npm ci + vite build → web/dist
make build-go    # embeds web/dist into the binary
```

`make build` chains both. The committed `web/dist/index.html`
fallback means a fresh clone can `make build-go` without npm and still
produce a working binary; the user just lands on a "UI not built"
placeholder instead of the SPA.

## Disabling the UI

Set `server.ui_enabled: false` in `config.yaml` to skip the embed
serve. The Go binary still contains the bundle (we don't strip the
embed at compile time), but `/` 404s back to the API surface — useful
when a CDN or separate Ingress fronts the UI.
