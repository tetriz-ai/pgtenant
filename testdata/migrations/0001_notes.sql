-- Copyright 2026 The pgtenant Authors
-- SPDX-License-Identifier: MIT
--
-- The sample schema the tests load. testdata/schema.sql is its snapshot,
-- written by pg_dump (see docs/usage.md); regenerate it after editing this file.

CREATE TABLE organizations (
    id uuid PRIMARY KEY,
    name text NOT NULL
);

CREATE TABLE notes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    body text NOT NULL,
    pinned boolean NOT NULL DEFAULT false,
    created_on date NOT NULL DEFAULT current_date
);

-- At most one pinned note per organization.
CREATE UNIQUE INDEX notes_one_pinned_per_org ON notes (organization_id) WHERE pinned;

-- Rows are visible to their own tenant, or to every caller in system scope.
-- The comparison is on text so an empty or '__system__' setting never fails a uuid cast.
ALTER TABLE notes ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON notes
    USING (current_setting('app.current_tenant', true) = '__system__'
           OR organization_id::text = current_setting('app.current_tenant', true))
    WITH CHECK (current_setting('app.current_tenant', true) = '__system__'
                OR organization_id::text = current_setting('app.current_tenant', true));

-- An optional date window: when app.data_cutoff holds a date, older rows are hidden.
-- An unset or empty setting means no window; nullif keeps an empty value from
-- reaching the date cast.
CREATE POLICY cutoff_window ON notes AS RESTRICTIVE FOR SELECT
    USING (created_on >= coalesce(nullif(current_setting('app.data_cutoff', true), ''), '-infinity')::date);

-- A partitioned table with one 2026 partition. The dump carries that partition, and
-- tests add a DEFAULT one for rows outside its range.
CREATE TABLE note_events (
    note_id uuid NOT NULL,
    happened_on date NOT NULL,
    kind text NOT NULL
) PARTITION BY RANGE (happened_on);
CREATE TABLE note_events_2026 PARTITION OF note_events
    FOR VALUES FROM ('2026-01-01') TO ('2027-01-01');
