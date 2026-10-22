-- Copyright 2026 The pgtenant Authors
-- SPDX-License-Identifier: MIT
--
-- The schema every example runs against. Run as a superuser; it is safe to re-run.

-- The application role. It is not a superuser and has no BYPASSRLS, so the
-- policies apply to it. It owns the tables, so FORCE ROW LEVEL SECURITY below
-- is what makes the policies apply to the owner too.
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'pgtenant_app') THEN
        CREATE ROLE pgtenant_app LOGIN PASSWORD 'app' NOSUPERUSER NOBYPASSRLS;
    END IF;
END
$$;

DROP SCHEMA IF EXISTS pgtenant_example CASCADE;
CREATE SCHEMA pgtenant_example AUTHORIZATION pgtenant_app;
SET search_path = pgtenant_example;
SET ROLE pgtenant_app;

CREATE TABLE notes (
    id serial PRIMARY KEY,
    organization_id uuid NOT NULL,
    body text NOT NULL
);

-- Rows are visible to their own tenant, or to every caller in system scope.
-- The comparison is on text so an empty or '__system__' setting never fails a uuid cast.
ALTER TABLE notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE notes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON notes
    USING (current_setting('app.current_tenant', true) = '__system__'
           OR organization_id::text = current_setting('app.current_tenant', true))
    WITH CHECK (current_setting('app.current_tenant', true) = '__system__'
                OR organization_id::text = current_setting('app.current_tenant', true));

RESET ROLE;

-- Two tenants' notes, written as the superuser, which bypasses the policies.
INSERT INTO notes (organization_id, body) VALUES
    ('00000000-0000-4000-8000-00000000000a', 'A: renew the domain'),
    ('00000000-0000-4000-8000-00000000000a', 'A: send the invoice'),
    ('00000000-0000-4000-8000-00000000000b', 'B: book the venue'),
    ('00000000-0000-4000-8000-00000000000b', 'B: order the badges');
