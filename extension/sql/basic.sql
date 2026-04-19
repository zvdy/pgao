-- Basic smoke test: extension can be created and version reports 0.1.0.
CREATE EXTENSION pgao;

SELECT pgao.version();

-- Extension should create its own schema.
SELECT nspname FROM pg_namespace WHERE nspname = 'pgao';

-- Functions should be present in pg_proc.
SELECT count(*) > 0 AS has_functions
FROM pg_proc p
JOIN pg_namespace n ON n.oid = p.pronamespace
WHERE n.nspname = 'pgao';

DROP EXTENSION pgao;

-- After drop, no pgao.* functions should remain (the empty schema may linger,
-- per Postgres semantics, but all extension-owned objects must be gone).
SELECT count(*) FROM pg_proc p
JOIN pg_namespace n ON n.oid = p.pronamespace
WHERE n.nspname = 'pgao';
