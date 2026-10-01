-- Roles: admin (everything), supervisor (operations + users read + audit), operator (operations), viewer (read only).
UPDATE users SET role = 'admin' WHERE role NOT IN ('admin', 'supervisor', 'operator', 'viewer');
ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_login TIMESTAMPTZ;
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('admin', 'supervisor', 'operator', 'viewer'));
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS ip TEXT NOT NULL DEFAULT '';

-- One-off schedules run on a single date instead of weekly days.
ALTER TABLE schedules ADD COLUMN IF NOT EXISTS once_on DATE;
ALTER TABLE schedules ALTER COLUMN days SET DEFAULT '{}';

CREATE TABLE IF NOT EXISTS audit_log (
    ts       TIMESTAMPTZ NOT NULL DEFAULT now(),
    username TEXT NOT NULL,
    action   TEXT NOT NULL,
    target   TEXT NOT NULL DEFAULT '',
    detail   JSONB NOT NULL DEFAULT '{}',
    ip       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS audit_ts ON audit_log (ts DESC);

-- Position/battery history for track playback and battery charts (1 sample per robot every few seconds).
CREATE TABLE IF NOT EXISTS telemetry (
    ts        TIMESTAMPTZ NOT NULL,
    robot_id  TEXT NOT NULL,
    x         DOUBLE PRECISION,
    y         DOUBLE PRECISION,
    yaw       DOUBLE PRECISION,
    battery   DOUBLE PRECISION,
    nav_state TEXT NOT NULL DEFAULT '',
    mode      TEXT NOT NULL DEFAULT '',
    people    INT NOT NULL DEFAULT 0,
    patrol_id BIGINT
);
CREATE INDEX IF NOT EXISTS telemetry_robot_ts ON telemetry (robot_id, ts DESC);

-- TimescaleDB when available (timescale/timescaledb image); plain PostgreSQL keeps normal tables.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'timescaledb') THEN
        CREATE EXTENSION IF NOT EXISTS timescaledb;
        PERFORM create_hypertable('telemetry', 'ts', if_not_exists => TRUE, migrate_data => TRUE);
        PERFORM create_hypertable('audit_log', 'ts', if_not_exists => TRUE, migrate_data => TRUE);
        PERFORM add_retention_policy('telemetry', INTERVAL '180 days', if_not_exists => TRUE);
        ALTER TABLE telemetry SET (timescaledb.compress, timescaledb.compress_segmentby = 'robot_id');
        PERFORM add_compression_policy('telemetry', INTERVAL '7 days', if_not_exists => TRUE);
    END IF;
END
$$;
