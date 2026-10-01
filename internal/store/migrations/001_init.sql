CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL PRIMARY KEY,
    username      TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'operator',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS floor_plans (
    id            BIGSERIAL PRIMARY KEY,
    name          TEXT NOT NULL,
    file_path     TEXT NOT NULL,
    content_type  TEXT NOT NULL,
    width_px      DOUBLE PRECISION NOT NULL DEFAULT 0,
    height_px     DOUBLE PRECISION NOT NULL DEFAULT 0,
    -- map frame -> plan pixels: p = origin + scale * R(rotation) * (x, -y)
    origin_x_px   DOUBLE PRECISION NOT NULL DEFAULT 0,
    origin_y_px   DOUBLE PRECISION NOT NULL DEFAULT 0,
    px_per_m      DOUBLE PRECISION NOT NULL DEFAULT 20,
    rotation_rad  DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS robots (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    token_hash     TEXT NOT NULL,
    floor_plan_id  BIGINT REFERENCES floor_plans(id) ON DELETE SET NULL,
    last_seen      TIMESTAMPTZ,
    last_telemetry JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS routes (
    id          BIGSERIAL PRIMARY KEY,
    uid         TEXT UNIQUE NOT NULL,
    robot_id    TEXT NOT NULL REFERENCES robots(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    length_m    DOUBLE PRECISION NOT NULL DEFAULT 0,
    stops       INT NOT NULL DEFAULT 0,
    closed      BOOLEAN NOT NULL DEFAULT false,
    csv         TEXT NOT NULL,
    archived    BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS schedules (
    id           BIGSERIAL PRIMARY KEY,
    robot_id     TEXT NOT NULL REFERENCES robots(id) ON DELETE CASCADE,
    route_id     BIGINT NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    days         INT[] NOT NULL,              -- 0 = Sunday ... 6 = Saturday
    time_of_day  TEXT NOT NULL,               -- HH:MM in the server time zone
    enabled      BOOLEAN NOT NULL DEFAULT true,
    last_run_on  DATE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS patrols (
    id          BIGSERIAL PRIMARY KEY,
    robot_id    TEXT NOT NULL REFERENCES robots(id) ON DELETE CASCADE,
    route_id    BIGINT REFERENCES routes(id) ON DELETE SET NULL,
    schedule_id BIGINT REFERENCES schedules(id) ON DELETE SET NULL,
    status      TEXT NOT NULL DEFAULT 'requested', -- requested, running, finished, stopped, failed
    message     TEXT NOT NULL DEFAULT '',
    laps        INT NOT NULL DEFAULT 0,
    started_at  TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS patrols_robot_created ON patrols (robot_id, created_at DESC);

CREATE TABLE IF NOT EXISTS anomaly_rules (
    category            TEXT PRIMARY KEY,
    label               TEXT NOT NULL,
    requires_validation BOOLEAN NOT NULL DEFAULT true,
    notify_telegram     BOOLEAN NOT NULL DEFAULT false,
    telegram_chat_id    TEXT NOT NULL DEFAULT '',
    min_confidence      DOUBLE PRECISION NOT NULL DEFAULT 0.5
);
INSERT INTO anomaly_rules (category, label, requires_validation, notify_telegram) VALUES
    ('trash', 'Sampah', false, true),
    ('spill', 'Tumpahan', false, true),
    ('floor_damage', 'Lantai Rusak', true, true),
    ('fallen_person', 'Orang Terjatuh', true, true),
    ('unknown', 'Lainnya', true, false)
ON CONFLICT (category) DO NOTHING;

CREATE TABLE IF NOT EXISTS anomalies (
    id               BIGSERIAL PRIMARY KEY,
    uid              TEXT UNIQUE NOT NULL,
    robot_id         TEXT NOT NULL REFERENCES robots(id) ON DELETE CASCADE,
    patrol_id        BIGINT REFERENCES patrols(id) ON DELETE SET NULL,
    route_id         BIGINT REFERENCES routes(id) ON DELETE SET NULL,
    detected_at      TIMESTAMPTZ NOT NULL,
    early_category   TEXT NOT NULL,
    early_confidence DOUBLE PRECISION NOT NULL,
    early_note       TEXT NOT NULL DEFAULT '',
    final_category   TEXT,
    final_confidence DOUBLE PRECISION,
    final_note       TEXT NOT NULL DEFAULT '',
    x                DOUBLE PRECISION,
    y                DOUBLE PRECISION,
    yaw              DOUBLE PRECISION,
    stop_index       INT,
    image_path       TEXT NOT NULL,
    -- queued -> analyzing -> needs_validation | notified | closed; validated -> confirmed | rejected
    status           TEXT NOT NULL DEFAULT 'queued',
    validated_by     TEXT,
    validated_at     TIMESTAMPTZ,
    notified_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS anomalies_status ON anomalies (status, created_at DESC);
CREATE INDEX IF NOT EXISTS anomalies_detected ON anomalies (detected_at DESC);

CREATE TABLE IF NOT EXISTS crowd_samples (
    id          BIGSERIAL PRIMARY KEY,
    robot_id    TEXT NOT NULL REFERENCES robots(id) ON DELETE CASCADE,
    patrol_id   BIGINT REFERENCES patrols(id) ON DELETE SET NULL,
    ts_start    TIMESTAMPTZ NOT NULL,
    ts_end      TIMESTAMPTZ NOT NULL,
    cell_size   DOUBLE PRECISION NOT NULL,
    cells       JSONB NOT NULL,
    peak_people INT NOT NULL
);
CREATE INDEX IF NOT EXISTS crowd_time ON crowd_samples (ts_start);

CREATE TABLE IF NOT EXISTS events (
    id        BIGSERIAL PRIMARY KEY,
    robot_id  TEXT NOT NULL REFERENCES robots(id) ON DELETE CASCADE,
    patrol_id BIGINT REFERENCES patrols(id) ON DELETE SET NULL,
    type      TEXT NOT NULL,
    payload   JSONB NOT NULL,
    ts        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS events_patrol ON events (patrol_id, ts);
