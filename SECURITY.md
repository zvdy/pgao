# Security policy

## Reporting a vulnerability

Please report security issues privately. Do **not** open a public GitHub
issue or pull request for anything you'd describe as a vulnerability.

**Preferred channel:** [GitHub Security Advisories](https://github.com/zvdy/pgao/security/advisories/new)
— private to the maintainers, lets us coordinate the fix and CVE with
you, and produces a public advisory once a release is out.

**Alternate:** email the repository owner via the address listed on the
[zvdy](https://github.com/zvdy) profile. Please include:

- A description of the issue and its impact (read access, RCE, auth
  bypass, data exfiltration, etc.).
- Steps to reproduce — ideally a minimal config or proof-of-concept.
- The pgao version + commit SHA you tested against.
- Whether the issue is already public (e.g. an underlying CVE in a
  dependency).

We'll acknowledge within **3 business days** and aim to have a patched
release within **30 days** for high-severity reports, faster for
critical ones with active exploitation. We'll credit you in the
advisory and the `CHANGELOG.md` unless you'd prefer anonymity.

## Supported versions

Pgao is in active early development; only the latest tagged release is
patched. Once we hit `v1.0.0` this policy will be revised.

| Version | Supported          |
| ------- | ------------------ |
| `main`  | :white_check_mark: |
| `< v1`  | Latest tag only    |

## Scope

In scope:

- The pgao Go binary (`src/`) and embedded web UI (`web/`).
- The official Helm chart (`charts/pgao/`).
- The published container image (`ghcr.io/zvdy/pgao:*`).
- The PostgreSQL extension scaffold (`extension/`).

Out of scope:

- Vulnerabilities in PostgreSQL itself — report upstream to the
  [PostgreSQL security team](https://www.postgresql.org/support/security/).
- Issues in third-party dependencies that are already disclosed and
  tracked by `govulncheck` / Trivy in CI. Open a regular issue if you
  spot one we've missed.
- Misconfigurations of the operator's environment (open `/api/*` to the
  public internet, leaked `PGAO_API_KEY`, etc.). Pgao ships secure
  defaults (auth disabled but documented as such, TLS-to-Postgres
  supported, NetworkPolicy template in the chart) — operators are
  responsible for enabling them.

## Defence-in-depth in pgao

For context on what we already do:

- **API hardening (#6).** Every `/api/v1/*` request goes through a
  middleware chain: panic-recoverer → request log → rate-limit →
  request-timeout → body-size cap → optional API-key auth. Internal
  errors return a generic `internal server error` body; the underlying
  error stays in pgao's logs.
- **Auth gate.** Bearer or `X-API-Key` token, constant-time compared.
  Disabled by default but documented; the Helm chart's NOTES.txt warns
  when auth is off.
- **TLS to Postgres.** `ssl_mode: verify-full` plus `ssl_root_cert` /
  `ssl_cert` / `ssl_key` / `ssl_server_name`. The Helm chart mounts
  cert material from k8s Secrets read-only at mode `0400`.
- **No raw SQL from request input.** Slow-query and table endpoints
  use an `order_by` allowlist (`mean_exec_time | total_exec_time |
  calls`) instead of interpolating user input.
- **Container image.** Multi-stage Dockerfile, non-root user (uid
  1000), pinned base image, no `postgresql-client` at runtime. Released
  images are signed with cosign (keyless OIDC) and ship CycloneDX
  SBOMs. Trivy scans every release and uploads SARIF to the GitHub
  Security tab.
- **Supply chain.** `govulncheck`, `gosec`, and (this PR) CodeQL run
  on every PR. Dependency updates land via regular PRs reviewed
  alongside code.

## Coordinated disclosure

If your report involves a dependency we don't maintain, please give us
a reasonable window (typically two weeks) to upstream a fix or
mitigation before public disclosure. We'll keep you informed of
progress.
