-- internal/backup-controller/migrations/001_init.sql
--
-- Initial Postgres schema for the kombify-Backup multi-tenant control
-- plane. Mirrors internal/backup-controller/types.go row-for-row.
--
-- This migration is a planning artifact in the Phase-1 / Phase-2
-- scaffold: there is no Postgres driver wired into the controller
-- yet, and the in-memory Store is the day-one shipping target. When
-- the pgx driver lands (separate PR), this file is the schema that
-- gets executed against a fresh database.
--
-- Conventions:
--   - All primary keys are uuid (text). Not bigint sequences — we want
--     the controller to mint IDs without round-tripping the DB.
--   - All timestamps are timestamptz, UTC.
--   - audit_log has no foreign keys: it must outlive Tenants, Hosts,
--     and Repos that may be hard-deleted. SaaS compliance trumps
--     referential integrity here.

CREATE TABLE tenants (
    id          text PRIMARY KEY,
    name        text NOT NULL,
    plan        text NOT NULL CHECK (plan IN ('free', 'pro', 'business')),
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE fleets (
    id         text PRIMARY KEY,
    tenant_id  text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name       text NOT NULL,
    region     text
);

CREATE INDEX fleets_tenant_idx ON fleets(tenant_id);

CREATE TABLE hosts (
    id              text PRIMARY KEY,
    fleet_id        text NOT NULL REFERENCES fleets(id) ON DELETE CASCADE,
    hostname        text NOT NULL,
    -- agent_token_hash stores a sha256 of the bearer token shown to
    -- the operator at enrollment time. The controller never persists
    -- the original token; the agent presents the original on every
    -- request and the server hashes-and-compares.
    agent_token_hash text NOT NULL,
    last_seen        timestamptz,
    stackkit_kind    text NOT NULL CHECK (stackkit_kind IN ('base-kit', 'modern-homelab', 'ha-kit'))
);

CREATE INDEX hosts_fleet_idx ON hosts(fleet_id);
CREATE UNIQUE INDEX hosts_token_idx ON hosts(agent_token_hash);

CREATE TABLE repos (
    id               text PRIMARY KEY,
    tenant_id        text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kind             text NOT NULL CHECK (kind IN ('b2', 'hetzner-storagebox', 's3')),
    endpoint         text NOT NULL,
    -- credentials_ref is a pointer into the platform secrets store,
    -- e.g. 'secret://kombify/tenants/<id>/repo'. Actual credential
    -- bytes never live here.
    credentials_ref  text NOT NULL
);

CREATE INDEX repos_tenant_idx ON repos(tenant_id);

CREATE TABLE jobs (
    id         text PRIMARY KEY,
    tenant_id  text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    host_id    text NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    repo_id    text NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    schedule   text NOT NULL,
    last_run   timestamptz,
    status     text NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'running', 'ok', 'failed'))
);

CREATE INDEX jobs_tenant_idx ON jobs(tenant_id);
CREATE INDEX jobs_host_idx   ON jobs(host_id);
-- The scheduler picks up due jobs by minute; status drives the SKIP
-- LOCKED queue once we move off the in-memory implementation.
CREATE INDEX jobs_status_idx ON jobs(status);

-- audit_log is intentionally append-only and FK-free. Hard deletes of
-- tenants, hosts, or repos must not cascade here — the audit record
-- is the legal evidence of what happened, including the deletion.
CREATE TABLE audit_log (
    id        text PRIMARY KEY,
    tenant_id text,
    actor     text NOT NULL,
    action    text NOT NULL,
    resource  text NOT NULL,
    ts        timestamptz NOT NULL DEFAULT now(),
    payload   jsonb
);

CREATE INDEX audit_tenant_ts_idx ON audit_log(tenant_id, ts DESC);
