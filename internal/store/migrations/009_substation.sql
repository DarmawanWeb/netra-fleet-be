-- Substation inspection (module substation): the equipment, the points a robot inspects, what it
-- measured there, and the findings operators follow up. Positions are in the robot map frame.

CREATE TABLE IF NOT EXISTS assets (
    id          BIGSERIAL PRIMARY KEY,
    tag         TEXT NOT NULL,              -- the site's code, e.g. TRF-1 or PMS-L1
    name        TEXT NOT NULL,
    kind        TEXT NOT NULL,              -- transformer, breaker, disconnector, ... (substation.AssetKinds)
    bay         TEXT NOT NULL DEFAULT '',
    voltage_kv  DOUBLE PRECISION,
    phase       TEXT NOT NULL DEFAULT '',
    x           DOUBLE PRECISION,           -- where it stands, metres in the map frame
    y           DOUBLE PRECISION,
    notes       TEXT NOT NULL DEFAULT '',
    archived    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS assets_tag ON assets (lower(tag));

-- Where a robot inspects: at a stop of a route, with the camera at a pose, measuring targets
-- (thermal) or the partial discharge level (pd).
CREATE TABLE IF NOT EXISTS inspection_points (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('thermal', 'pd')),
    asset_id    BIGINT REFERENCES assets(id) ON DELETE RESTRICT,
    route_id    BIGINT REFERENCES routes(id) ON DELETE SET NULL,
    stop_index  INT CHECK (stop_index >= 0),
    seq         INT NOT NULL DEFAULT 0,      -- order among the points of one stop
    ptz         JSONB,                       -- {pan_deg, tilt_deg, zoom}
    settings    JSONB NOT NULL DEFAULT '{}', -- {reference, emissivity, distance_m, watch_db, alarm_db}
    targets     JSONB NOT NULL DEFAULT '[]', -- thermal: [{key, label, asset_id, phase, group, roi, limit_c}]
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS inspection_points_route ON inspection_points (route_id, stop_index, seq);
CREATE INDEX IF NOT EXISTS inspection_points_targets ON inspection_points USING GIN (targets jsonb_path_ops);

-- One capture at one point, with its evaluation (substation.EvaluateThermal or PDPriority).
CREATE TABLE IF NOT EXISTS inspections (
    id          BIGSERIAL PRIMARY KEY,
    uid         TEXT NOT NULL UNIQUE,        -- from the robot: uploads repeat safely
    point_id    BIGINT NOT NULL REFERENCES inspection_points(id) ON DELETE RESTRICT,
    robot_id    TEXT REFERENCES robots(id) ON DELETE SET NULL,
    patrol_id   BIGINT REFERENCES patrols(id) ON DELETE SET NULL,
    taken_at    TIMESTAMPTZ NOT NULL,
    kind        TEXT NOT NULL,
    reference   TEXT NOT NULL DEFAULT '',    -- thermal: the reference that rated it
    ambient_c   DOUBLE PRECISION,
    thermal     JSONB,                       -- [{key, label, asset_id, max_c, avg_c, min_c, ref_key, delta_similar_c, delta_ambient_c, priority, basis}]
    pd          JSONB,                       -- {db, detected, kind, priority}
    priority    SMALLINT NOT NULL DEFAULT 0, -- the most severe of the capture; 0 none
    x           DOUBLE PRECISION,            -- the robot pose
    y           DOUBLE PRECISION,
    yaw         DOUBLE PRECISION,
    images      JSONB NOT NULL DEFAULT '{}', -- {thermal, visual, acoustic}: file paths
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS inspections_point ON inspections (point_id, taken_at DESC);
CREATE INDEX IF NOT EXISTS inspections_patrol ON inspections (patrol_id);
CREATE INDEX IF NOT EXISTS inspections_taken ON inspections (taken_at DESC);

-- A deficiency of one target (or of a pd point: target 'pd'), from the first abnormal reading
-- until an operator resolves it. At most one is not resolved per target.
CREATE TABLE IF NOT EXISTS findings (
    id                  BIGSERIAL PRIMARY KEY,
    point_id            BIGINT NOT NULL REFERENCES inspection_points(id) ON DELETE RESTRICT,
    target_key          TEXT NOT NULL,
    label               TEXT NOT NULL DEFAULT '',
    asset_id            BIGINT REFERENCES assets(id) ON DELETE SET NULL,
    kind                TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'acknowledged', 'resolved')),
    priority            SMALLINT NOT NULL,   -- the most severe seen since it opened
    basis               TEXT NOT NULL DEFAULT '',
    last_priority       SMALLINT NOT NULL,   -- of the latest reading; 0 when it was normal
    first_inspection_id BIGINT NOT NULL REFERENCES inspections(id) ON DELETE RESTRICT,
    last_inspection_id  BIGINT NOT NULL REFERENCES inspections(id) ON DELETE RESTRICT,
    first_seen          TIMESTAMPTZ NOT NULL,
    last_seen           TIMESTAMPTZ NOT NULL,
    last_value          DOUBLE PRECISION,    -- °C (hottest) or dB
    last_delta_c        DOUBLE PRECISION,
    occurrences         INT NOT NULL DEFAULT 1,
    note                TEXT NOT NULL DEFAULT '',
    acknowledged_by     TEXT,
    acknowledged_at     TIMESTAMPTZ,
    resolved_by         TEXT,
    resolved_at         TIMESTAMPTZ,
    notified_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS findings_active ON findings (point_id, target_key) WHERE status <> 'resolved';
CREATE INDEX IF NOT EXISTS findings_asset ON findings (asset_id) WHERE status <> 'resolved';
CREATE INDEX IF NOT EXISTS findings_status ON findings (status, priority, last_seen DESC);
