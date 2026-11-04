# Database

`netra-api` keeps its state in PostgreSQL. With TimescaleDB available, two tables become
hypertables; plain PostgreSQL works without it. Access goes through `database/sql` with the
`lib/pq` driver in [`internal/store`](../internal/store/store.go). Photos and floor-plan files live
on disk ([files outside the database](#files-outside-the-database)).

## How migrations are applied

Migrations are the SQL files in `internal/store/migrations/`, embedded into the binary with
`//go:embed migrations/*.sql`. `store.Open`:

1. opens the pool (`NETRA_DATABASE_URL`, at most 20 open connections) and pings the database up to
   31 times, one second apart, so `netra-api` can start before the database is ready;
2. creates `schema_migrations (name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`
   if needed;
3. sorts the embedded file names as strings and, for each name not yet in `schema_migrations`, runs
   the whole file as one statement batch inside a transaction and records the name in the same
   transaction.

Any error stops start-up (`netra-api` exits with status 1). Consequences:

- A migration runs once per database and is identified by its file name only; editing a file that
  was already applied has no effect on existing databases.
- There are no down migrations.
- Order is lexical, hence the zero-padded `NNN_` prefix.
- Most statements are idempotent (`IF NOT EXISTS`, `ON CONFLICT`), not all of them
  (`ADD CONSTRAINT users_role_check` in 002), so a migration must not be applied by hand before the
  service sees it.

## Overview

```mermaid
erDiagram
    users ||--o{ sessions : "logs in with"
    floor_plans |o--o{ robots : "drawn on"
    robots ||--o{ routes : records
    robots ||--o{ schedules : "scheduled by"
    routes ||--o{ schedules : "used by"
    robots ||--o{ patrols : runs
    routes |o--o{ patrols : follows
    schedules |o--o{ patrols : starts
    robots ||--o{ anomalies : detects
    patrols |o--o{ anomalies : "found during"
    routes |o--o{ anomalies : "found on"
    robots ||--o{ crowd_samples : counts
    patrols |o--o{ crowd_samples : "counted during"
    robots ||--o{ events : reports
    patrols |o--o{ events : "reported during"
    api_clients ||--o{ api_tokens : issues
    assets |o--o{ inspection_points : "measured at"
    routes |o--o{ inspection_points : "stops at"
    inspection_points ||--o{ inspections : "captured at"
    robots |o--o{ inspections : uploads
    patrols |o--o{ inspections : "taken during"
    inspection_points ||--o{ findings : "deficiency of"
    assets |o--o{ findings : concerns
    inspections ||--o{ findings : "first and last seen in"

    users {
        bigint id PK
        text username UK
        text role
        boolean disabled
    }
    sessions {
        text token_hash PK
        bigint user_id FK
        timestamptz expires_at
    }
    robots {
        text id PK
        text token_hash
        bigint floor_plan_id FK
        jsonb last_telemetry
    }
    floor_plans {
        bigint id PK
        text kind
        text file_path
        double px_per_m
    }
    routes {
        bigint id PK
        text uid UK
        text robot_id FK
        text csv
    }
    schedules {
        bigint id PK
        text robot_id FK
        bigint route_id FK
        text time_of_day
        date once_on
    }
    patrols {
        bigint id PK
        text robot_id FK
        bigint route_id FK
        bigint schedule_id FK
        text status
    }
    anomalies {
        bigint id PK
        text uid UK
        text robot_id FK
        bigint patrol_id FK
        bigint route_id FK
        text status
        timestamptz updated_at
    }
    anomaly_rules {
        text category PK
        boolean requires_validation
        double min_confidence
    }
    crowd_samples {
        bigint id PK
        text robot_id FK
        bigint patrol_id FK
        jsonb cells
    }
    events {
        bigint id PK
        text robot_id FK
        bigint patrol_id FK
        jsonb payload
    }
    telemetry {
        timestamptz ts
        text robot_id "no foreign key"
        bigint patrol_id "no foreign key"
    }
    audit_log {
        timestamptz ts
        text username
        text action
    }
    api_clients {
        bigint id PK
        text client_id UK
        text secret_hash
    }
    api_tokens {
        text token_hash PK
        bigint client_pk FK
        timestamptz expires_at
    }
    geo_site {
        smallint id PK "always 1"
        jsonb control_points
        double scale
        text basemap
    }
    assets {
        bigint id PK
        text tag UK
        text kind
        boolean archived
    }
    inspection_points {
        bigint id PK
        text kind
        bigint asset_id FK
        bigint route_id FK
        int stop_index
        jsonb targets
    }
    inspections {
        bigint id PK
        text uid UK
        bigint point_id FK
        text robot_id FK
        bigint patrol_id FK
        smallint priority
    }
    findings {
        bigint id PK
        bigint point_id FK
        text target_key
        bigint asset_id FK
        text status
        smallint priority
    }
```

| Table | Holds | Written by |
|---|---|---|
| `users` | Dashboard accounts | Admin bootstrap at start-up, user routes, login (`last_login`) |
| `sessions` | SHA-256 digests of session tokens | Login, logout, user and password changes |
| `robots` | Registered robots, token digest, last telemetry | Robot routes, robot telemetry |
| `floor_plans` | Plan files and their calibration | Floor-plan routes |
| `routes` | Recorded routes with their CSV | Robot uploads; rename and archive routes |
| `schedules` | Weekly and one-off patrol schedules | Schedule routes; scheduler (`last_run_on`) |
| `patrols` | Patrol runs | Patrol start, robot `patrol_event`, command acks |
| `events` | Robot `patrol_event`, `stop_event` and `alert` messages | Robot WebSocket |
| `anomaly_rules` | What happens to each anomaly category | Seeds (001, 003), rule routes |
| `anomalies` | Detections: early and final result, status, validation, notification | Robot uploads, pipeline, validation |
| `crowd_samples` | Crowd windows with their grid cells | Robot WebSocket |
| `telemetry` | One history sample per robot every 5 s | Robot WebSocket |
| `audit_log` | User and integration actions | API ([audit log](architecture.md#audit-log)) |
| `api_clients`, `api_tokens` | Integration clients and their access tokens | API client routes, `/api/ext/v1/oauth/token` |
| `license`, `settings` | The license uploaded in the dashboard; the installation ID | License routes, first start |
| `geo_site` | The georeference of a `gps` deployment: control points, fitted transform, basemap, overlay | Site georeference routes |
| `assets`, `inspection_points` | Substation equipment and where robots inspect it | Substation routes |
| `inspections` | Thermal and partial discharge captures with their ratings | Robot inspection uploads |
| `findings` | Deficiencies from their first abnormal reading until resolved | Robot inspection uploads, acknowledge and resolve routes |

## Migrations

### 001_init.sql

[`001_init.sql`](../internal/store/migrations/001_init.sql) is the base schema. Foreign keys to
`robots` cascade on delete; references to patrols, routes, schedules and floor plans are set to
`NULL` when those rows are deleted (routes cascade into schedules).

**`users`**

| Column | Type | Notes |
|---|---|---|
| `id` | `BIGSERIAL` | Primary key |
| `username` | `TEXT` | Unique, not null |
| `password_hash` | `TEXT` | Not null; `pbkdf2-sha256$<iterations>$<salt>$<hash>` |
| `role` | `TEXT` | Not null, default `operator` |
| `created_at` | `TIMESTAMPTZ` | Default `now()` |

**`sessions`**

| Column | Type | Notes |
|---|---|---|
| `token_hash` | `TEXT` | Primary key; SHA-256 hex of the session token |
| `user_id` | `BIGINT` | References `users`, on delete cascade |
| `expires_at` | `TIMESTAMPTZ` | Not null |

**`floor_plans`**

| Column | Type | Notes |
|---|---|---|
| `id` | `BIGSERIAL` | Primary key |
| `name`, `file_path`, `content_type` | `TEXT` | Not null |
| `width_px`, `height_px` | `DOUBLE PRECISION` | Default 0 |
| `origin_x_px`, `origin_y_px` | `DOUBLE PRECISION` | Default 0; plan pixel of the map origin |
| `px_per_m` | `DOUBLE PRECISION` | Default 20 |
| `rotation_rad` | `DOUBLE PRECISION` | Default 0 |
| `created_at` | `TIMESTAMPTZ` | Default `now()` |

The column comment gives the transform: plan pixel = origin + scale · R(rotation) · (x, −y).

**`robots`**

| Column | Type | Notes |
|---|---|---|
| `id` | `TEXT` | Primary key, the robot ID |
| `name` | `TEXT` | Not null |
| `token_hash` | `TEXT` | Not null; SHA-256 hex of the robot token |
| `floor_plan_id` | `BIGINT` | References `floor_plans`, on delete set null |
| `last_seen` | `TIMESTAMPTZ` | Time of the last telemetry |
| `last_telemetry` | `JSONB` | Last telemetry message as sent |
| `created_at` | `TIMESTAMPTZ` | Default `now()` |

**`routes`**

| Column | Type | Notes |
|---|---|---|
| `id` | `BIGSERIAL` | Primary key |
| `uid` | `TEXT` | Unique, not null; the robot's route ID, makes uploads idempotent |
| `robot_id` | `TEXT` | References `robots`, on delete cascade |
| `name` | `TEXT` | Not null |
| `recorded_at` | `TIMESTAMPTZ` | Not null |
| `length_m` | `DOUBLE PRECISION` | Default 0 |
| `stops` | `INT` | Default 0 |
| `closed` | `BOOLEAN` | Default false |
| `csv` | `TEXT` | Not null; `x,y,yaw_deg,dwell_sec` rows |
| `archived` | `BOOLEAN` | Default false |
| `created_at` | `TIMESTAMPTZ` | Default `now()` |

**`schedules`**

| Column | Type | Notes |
|---|---|---|
| `id` | `BIGSERIAL` | Primary key |
| `robot_id` | `TEXT` | References `robots`, on delete cascade |
| `route_id` | `BIGINT` | References `routes`, on delete cascade |
| `name` | `TEXT` | Not null |
| `days` | `INT[]` | Not null; 0 = Sunday to 6 = Saturday |
| `time_of_day` | `TEXT` | Not null; `HH:MM` in the server time zone |
| `enabled` | `BOOLEAN` | Default true |
| `last_run_on` | `DATE` | Last day the schedule fired |
| `created_at` | `TIMESTAMPTZ` | Default `now()` |

**`patrols`**, index `patrols_robot_created (robot_id, created_at DESC)`

| Column | Type | Notes |
|---|---|---|
| `id` | `BIGSERIAL` | Primary key |
| `robot_id` | `TEXT` | References `robots`, on delete cascade |
| `route_id` | `BIGINT` | References `routes`, on delete set null |
| `schedule_id` | `BIGINT` | References `schedules`, on delete set null |
| `status` | `TEXT` | Default `requested`; `requested`, `running`, `finished`, `stopped`, `failed` |
| `message` | `TEXT` | Default `''` |
| `laps` | `INT` | Default 0 |
| `started_at`, `finished_at` | `TIMESTAMPTZ` | |
| `created_at` | `TIMESTAMPTZ` | Default `now()` |

**`anomaly_rules`**, seeded with `ON CONFLICT DO NOTHING`

| Column | Type | Notes |
|---|---|---|
| `category` | `TEXT` | Primary key |
| `label` | `TEXT` | Not null |
| `requires_validation` | `BOOLEAN` | Default true |
| `notify_telegram` | `BOOLEAN` | Default false |
| `telegram_chat_id` | `TEXT` | Default `''` (use `NETRA_TELEGRAM_CHAT_ID`) |
| `min_confidence` | `DOUBLE PRECISION` | Default 0.5 |

| Seed | Label | Requires validation | Telegram |
|---|---|---|---|
| `trash` | Sampah | false | true |
| `spill` | Tumpahan | false | true |
| `floor_damage` | Lantai Rusak | true | true |
| `fallen_person` | Orang Terjatuh | true | true |
| `unknown` | Lainnya | true | false |

**`anomalies`**, indexes `anomalies_status (status, created_at DESC)` and
`anomalies_detected (detected_at DESC)`

| Column | Type | Notes |
|---|---|---|
| `id` | `BIGSERIAL` | Primary key |
| `uid` | `TEXT` | Unique, not null; the robot's detection ID, makes uploads idempotent |
| `robot_id` | `TEXT` | References `robots`, on delete cascade |
| `patrol_id` | `BIGINT` | References `patrols`, on delete set null |
| `route_id` | `BIGINT` | References `routes`, on delete set null |
| `detected_at` | `TIMESTAMPTZ` | Not null |
| `early_category` | `TEXT` | Not null; the robot's category |
| `early_confidence` | `DOUBLE PRECISION` | Not null |
| `early_note` | `TEXT` | Default `''` |
| `final_category` | `TEXT` | Null until the final detection |
| `final_confidence` | `DOUBLE PRECISION` | |
| `final_note` | `TEXT` | Default `''` |
| `x`, `y`, `yaw` | `DOUBLE PRECISION` | Robot pose in the map frame |
| `stop_index` | `INT` | |
| `image_path` | `TEXT` | Not null; photo path on disk |
| `status` | `TEXT` | Default `queued` |
| `validated_by` | `TEXT` | |
| `validated_at`, `notified_at` | `TIMESTAMPTZ` | |
| `created_at` | `TIMESTAMPTZ` | Default `now()` |

The column comment on `status` (`queued -> analyzing -> needs_validation | notified | closed`)
predates the code; the statuses in use are `queued`, `analyzing`, `needs_validation`, `confirmed`,
`rejected` and `dismissed` ([pipeline](architecture.md#anomaly-pipeline)).

**`crowd_samples`**, index `crowd_time (ts_start)`

| Column | Type | Notes |
|---|---|---|
| `id` | `BIGSERIAL` | Primary key |
| `robot_id` | `TEXT` | References `robots`, on delete cascade |
| `patrol_id` | `BIGINT` | References `patrols`, on delete set null |
| `ts_start`, `ts_end` | `TIMESTAMPTZ` | Not null; the window, corrected to server time |
| `cell_size` | `DOUBLE PRECISION` | Not null; metres |
| `cells` | `JSONB` | Not null; `[[ix, iy, count], ...]` |
| `peak_people` | `INT` | Not null |

**`events`**, index `events_patrol (patrol_id, ts)`

| Column | Type | Notes |
|---|---|---|
| `id` | `BIGSERIAL` | Primary key |
| `robot_id` | `TEXT` | References `robots`, on delete cascade |
| `patrol_id` | `BIGINT` | References `patrols`, on delete set null |
| `type` | `TEXT` | Not null; `patrol_event`, `stop_event` or `alert` |
| `payload` | `JSONB` | Not null; the robot message |
| `ts` | `TIMESTAMPTZ` | Default `now()` (server time of arrival) |

### 002_rbac_audit_timeseries.sql

[`002_rbac_audit_timeseries.sql`](../internal/store/migrations/002_rbac_audit_timeseries.sql) adds
roles, session metadata, one-off schedules, the audit log and the telemetry history.

- `users`: every role other than `admin`, `supervisor`, `operator` and `viewer` becomes `admin`; new
  columns `display_name TEXT NOT NULL DEFAULT ''`, `disabled BOOLEAN NOT NULL DEFAULT false`,
  `last_login TIMESTAMPTZ`; constraint `users_role_check` limits `role` to the four roles.
- `sessions`: new columns `created_at TIMESTAMPTZ NOT NULL DEFAULT now()` and
  `ip TEXT NOT NULL DEFAULT ''`.
- `schedules`: new column `once_on DATE` (run once on that date instead of weekly); `days` defaults
  to `'{}'`.
- The comment at the top describes the supervisor as "operations + users read + audit"; the
  permissions actually granted are in [`rbac.go`](../internal/auth/rbac.go), where the supervisor has
  no `users` permission.

**`audit_log`** (no primary key), index `audit_ts (ts DESC)`

| Column | Type | Notes |
|---|---|---|
| `ts` | `TIMESTAMPTZ` | Not null, default `now()` |
| `username` | `TEXT` | Not null; `api:<client_id>` for integration clients, `-` when unknown |
| `action` | `TEXT` | Not null |
| `target` | `TEXT` | Not null, default `''` |
| `detail` | `JSONB` | Not null, default `'{}'` |
| `ip` | `TEXT` | Not null, default `''` |

**`telemetry`** (no primary key, no foreign keys), index `telemetry_robot_ts (robot_id, ts DESC)`

| Column | Type | Notes |
|---|---|---|
| `ts` | `TIMESTAMPTZ` | Not null; server time of the sample |
| `robot_id` | `TEXT` | Not null |
| `x`, `y`, `yaw` | `DOUBLE PRECISION` | Pose in the map frame |
| `battery` | `DOUBLE PRECISION` | Percent |
| `nav_state`, `mode` | `TEXT` | Not null, default `''` |
| `people` | `INT` | Not null, default 0 |
| `patrol_id` | `BIGINT` | |

The `DO` block at the end runs only when `timescaledb` is listed in `pg_available_extensions`:

- `CREATE EXTENSION IF NOT EXISTS timescaledb`;
- `telemetry` and `audit_log` become hypertables on `ts` (`migrate_data => TRUE`);
- retention policy: `telemetry` chunks older than 180 days are dropped;
- compression on `telemetry` (`compress_segmentby = 'robot_id'`) with a policy compressing chunks
  older than 7 days.

`audit_log` gets neither retention nor compression (the repository README says both tables do).
Because the extension is created whenever it is available, the database role must be allowed to
create it; if that fails, the migration fails and `netra-api` does not start. Without the extension
both tables stay plain tables.

### 003_english_defaults.sql

[`003_english_defaults.sql`](../internal/store/migrations/003_english_defaults.sql) changes the
seeded labels to English (`Trash`, `Spill`, `Floor damage`, `Fallen person`, `Other`), each only if
the label is still the Indonesian seed, so custom labels are kept. Translations of the built-in
categories for reports and Telegram come from [`i18n.go`](../internal/i18n/i18n.go).

### 004_robot_load.sql

[`004_robot_load.sql`](../internal/store/migrations/004_robot_load.sql) adds nullable
`DOUBLE PRECISION` columns to `telemetry` for the robot computer load: `cpu_percent`,
`gpu_percent`, `memory_percent`, `memory_used_gb`, `memory_total_gb`, `cpu_temp_c`.

### 005_api_clients.sql

[`005_api_clients.sql`](../internal/store/migrations/005_api_clients.sql) adds the integration API
clients, their tokens, and the change feed column on `anomalies`.

**`api_clients`**

| Column | Type | Notes |
|---|---|---|
| `id` | `BIGSERIAL` | Primary key |
| `client_id` | `TEXT` | Unique, not null; `nc_` and 16 hex digits |
| `name` | `TEXT` | Not null |
| `secret_hash` | `TEXT` | Not null; SHA-256 hex of the `ncs_` secret |
| `scopes` | `TEXT[]` | Not null, default `'{}'` |
| `enabled` | `BOOLEAN` | Not null, default true |
| `created_by` | `TEXT` | Not null, default `''` |
| `created_at` | `TIMESTAMPTZ` | Not null, default `now()` |
| `last_used_at` | `TIMESTAMPTZ` | Updated at most once a minute per client |

**`api_tokens`**, index `api_tokens_expiry (expires_at)`

| Column | Type | Notes |
|---|---|---|
| `token_hash` | `TEXT` | Primary key; SHA-256 hex of the `nat_` access token |
| `client_pk` | `BIGINT` | Not null; references `api_clients(id)`, on delete cascade |
| `scopes` | `TEXT[]` | Not null; scopes granted to this token |
| `expires_at` | `TIMESTAMPTZ` | Not null |

**`anomalies.updated_at`**

- Added as `TIMESTAMPTZ`, back-filled with the latest of `created_at`, `validated_at` and
  `notified_at`, then given the default `clock_timestamp()` and `NOT NULL`.
- Function `netra_touch_updated_at()` (PL/pgSQL) and trigger `anomalies_touch` (`BEFORE UPDATE`,
  for each row) set `updated_at` to `clock_timestamp()` on every update, so status changes, final
  detections, validations and notifications all move the row in the change feed.
- Index `anomalies_updated (updated_at, id)` serves the feed's keyset condition
  `(updated_at, id) > (cursor)` ([`feed.go`](../internal/store/feed.go)).

### 006_license.sql

[`006_license.sql`](../internal/store/migrations/006_license.sql) keeps the license uploaded in the
dashboard ([licensing](licensing.md#installing-a-license)).

**`license`**, one row

| Column | Type | Notes |
|---|---|---|
| `id` | `SMALLINT` | Primary key, default 1, `CHECK (id = 1)` |
| `text` | `TEXT` | Not null; the signed license `NETRA-LIC-1...`, verified again at every start |
| `uploaded_by` | `TEXT` | Not null, default `''`; username |
| `uploaded_at` | `TIMESTAMPTZ` | Not null, default `now()` |

### 007_settings.sql

[`007_settings.sql`](../internal/store/migrations/007_settings.sql) adds the settings of the
installation.

**`settings`**

| Column | Type | Notes |
|---|---|---|
| `key` | `TEXT` | Primary key; `installation_id` holds the installation ID licenses are bound to |
| `value` | `TEXT` | Not null |
| `updated_at` | `TIMESTAMPTZ` | Not null, default `now()` |

### 008_maps.sql

[`008_maps.sql`](../internal/store/migrations/008_maps.sql) adds the map modes
([architecture](architecture.md#map-modes)).

**`floor_plans`**, new columns

| Column | Type | Notes |
|---|---|---|
| `kind` | `TEXT` | Not null, default `floorplan`; `CHECK` `floorplan` (an uploaded layout) or `pgm` (a robot map) |
| `resolution_m` | `DOUBLE PRECISION` | `pgm`: metres per pixel from `map.yaml` |
| `map_origin_x`, `map_origin_y`, `map_origin_yaw` | `DOUBLE PRECISION` | `pgm`: map frame pose of the lower-left pixel from `map.yaml` |

A robot map's `origin_x_px`, `origin_y_px`, `px_per_m` and `rotation_rad` are computed from its
`map.yaml` at upload and are not changed afterwards (`UPDATE` only touches `kind = 'floorplan'`).

**`geo_site`**, one row

| Column | Type | Notes |
|---|---|---|
| `id` | `SMALLINT` | Primary key, default 1, `CHECK (id = 1)` |
| `control_points` | `JSONB` | Not null, default `'[]'`; `[{label, x, y, lat, lon}]` |
| `heading_deg` | `DOUBLE PRECISION` | Compass heading of the map x axis, used with a single control point |
| `fit_method` | `TEXT` | `points` or `heading`; null until fitted |
| `ref_lat`, `ref_lon` | `DOUBLE PRECISION` | Reference of the tangent plane (degrees) |
| `scale`, `rotation_rad`, `east_m`, `north_m` | `DOUBLE PRECISION` | The fitted similarity from map metres to the tangent plane |
| `rms_m` | `DOUBLE PRECISION` | RMS residual of the fit, metres |
| `basemap` | `TEXT` | Not null, default `osm`; `CHECK` `osm`, `satellite`, `custom` or `none` |
| `basemap_url`, `basemap_attribution` | `TEXT` | Not null, default `''`; used by `custom` |
| `basemap_max_zoom` | `INT` | Not null, default 19 |
| `overlay_path`, `overlay_type` | `TEXT` | The overlay image on disk and its content type; null without one |
| `overlay_north`, `overlay_south`, `overlay_east`, `overlay_west` | `DOUBLE PRECISION` | Bounds of the overlay (degrees) |
| `overlay_opacity` | `DOUBLE PRECISION` | Not null, default 1 |
| `overlay_updated_at` | `TIMESTAMPTZ` | When the overlay or its bounds last changed |
| `updated_by` | `TEXT` | Not null, default `''`; username of the last change |
| `updated_at` | `TIMESTAMPTZ` | Not null, default `now()` |

**`telemetry`**, new columns `lat` and `lon` (`DOUBLE PRECISION`): the GPS position of a robot
that reports a fix, for trails and track replay on a `gps` map.

Migrations run under the advisory lock `7151987`, so servers starting on one database at once
apply them one after the other.

### 009_substation.sql

[`009_substation.sql`](../internal/store/migrations/009_substation.sql) adds substation inspection
([substation](substation.md)).

**`assets`**: the equipment of the site

| Column | Type | Notes |
|---|---|---|
| `id` | `BIGSERIAL` | Primary key |
| `tag` | `TEXT` | Not null; unique ignoring case (index `assets_tag` on `lower(tag)`) |
| `name`, `kind` | `TEXT` | Not null; `kind` one of `substation.AssetKinds`, checked by the API |
| `bay`, `phase`, `notes` | `TEXT` | Not null, default `''` |
| `voltage_kv` | `DOUBLE PRECISION` | |
| `x`, `y` | `DOUBLE PRECISION` | Position in the map frame, metres |
| `archived` | `BOOLEAN` | Not null, default false |
| `created_at`, `updated_at` | `TIMESTAMPTZ` | Not null, default `now()` |

**`inspection_points`**: where a robot inspects

| Column | Type | Notes |
|---|---|---|
| `id` | `BIGSERIAL` | Primary key |
| `name` | `TEXT` | Not null |
| `kind` | `TEXT` | Not null; `CHECK` `thermal` or `pd` |
| `asset_id` | `BIGINT` | References `assets(id)` `ON DELETE RESTRICT` |
| `route_id` | `BIGINT` | References `routes(id)` `ON DELETE SET NULL` |
| `stop_index` | `INT` | The route stop, `CHECK (stop_index >= 0)` |
| `seq` | `INT` | Not null, default 0; order at the stop |
| `ptz` | `JSONB` | `{pan_deg, tilt_deg, zoom}` |
| `settings` | `JSONB` | Not null, default `'{}'`; `{reference, emissivity, distance_m, watch_db, alarm_db}` |
| `targets` | `JSONB` | Not null, default `'[]'`; `[{key, label, asset_id, phase, group, roi, limit_c}]`, GIN index (`jsonb_path_ops`) for the assets they name |
| `enabled` | `BOOLEAN` | Not null, default true |
| `created_at`, `updated_at` | `TIMESTAMPTZ` | Not null, default `now()` |

**`inspections`**: one capture with its rating

| Column | Type | Notes |
|---|---|---|
| `id` | `BIGSERIAL` | Primary key |
| `uid` | `TEXT` | Not null, unique: robot uploads repeat safely |
| `point_id` | `BIGINT` | Not null, references `inspection_points(id)` `ON DELETE RESTRICT` |
| `robot_id` | `TEXT` | References `robots(id)` `ON DELETE SET NULL` |
| `patrol_id` | `BIGINT` | References `patrols(id)` `ON DELETE SET NULL` |
| `taken_at` | `TIMESTAMPTZ` | Not null; the robot time corrected for its clock offset |
| `kind`, `reference` | `TEXT` | Not null; the point's kind and the reference that rated it |
| `ambient_c` | `DOUBLE PRECISION` | |
| `thermal` | `JSONB` | Every target's reading and rating (`substation.Result`) |
| `pd` | `JSONB` | `{db, detected, kind, priority}` |
| `priority` | `SMALLINT` | Not null, default 0; the most severe of the capture |
| `x`, `y`, `yaw` | `DOUBLE PRECISION` | The robot pose |
| `images` | `JSONB` | Not null, default `'{}'`; `{thermal, visual, acoustic}` file paths |
| `created_at` | `TIMESTAMPTZ` | Not null, default `now()` |

Indexes on `(point_id, taken_at DESC)`, `patrol_id` and `taken_at DESC`.

**`findings`**: a deficiency of one target until it is resolved

| Column | Type | Notes |
|---|---|---|
| `id` | `BIGSERIAL` | Primary key |
| `point_id` | `BIGINT` | Not null, references `inspection_points(id)` `ON DELETE RESTRICT` |
| `target_key` | `TEXT` | Not null; the thermal target, or `pd` |
| `label`, `basis`, `note` | `TEXT` | Not null, default `''` |
| `asset_id` | `BIGINT` | References `assets(id)` `ON DELETE SET NULL` |
| `kind` | `TEXT` | Not null |
| `status` | `TEXT` | Not null, default `open`; `CHECK` `open`, `acknowledged` or `resolved` |
| `priority` | `SMALLINT` | Not null; the most severe seen while not resolved |
| `last_priority` | `SMALLINT` | Not null; of the latest reading, 0 when normal |
| `first_inspection_id`, `last_inspection_id` | `BIGINT` | Not null, reference `inspections(id)` `ON DELETE RESTRICT` |
| `first_seen`, `last_seen` | `TIMESTAMPTZ` | Not null |
| `last_value`, `last_delta_c` | `DOUBLE PRECISION` | °C (hottest) or dB, and the rise that rated it |
| `occurrences` | `INT` | Not null, default 1; abnormal readings |
| `acknowledged_by`, `resolved_by` | `TEXT` | Usernames |
| `acknowledged_at`, `resolved_at`, `notified_at` | `TIMESTAMPTZ` | |
| `created_at`, `updated_at` | `TIMESTAMPTZ` | Not null, default `now()` |

The unique index `findings_active` on `(point_id, target_key) WHERE status <> 'resolved'` keeps one
finding per target until it is resolved; recording an inspection and its findings is one
transaction. Indexes on `asset_id` (not resolved) and `(status, priority, last_seen DESC)`.

The site settings are the JSON value of the `settings` key `substation`.

## Retention

- With TimescaleDB, `telemetry` rows older than 180 days are dropped and chunks older than 7 days
  are compressed.
- Expired `api_tokens` rows are deleted whenever a new token is issued.
- Nothing else is deleted automatically: patrols, events, anomalies (and their photos), crowd
  samples, `audit_log` and `sessions` (expired sessions included) grow until removed through the API
  or by hand. Deleting a robot cascades to its routes, schedules, patrols, anomalies, crowd samples
  and events, but not to `telemetry` rows or photo files.
- Substation inspections, findings and their images are kept: a point with inspections and an asset
  in use cannot be deleted, and deleting a robot only clears `inspections.robot_id`.

## Files outside the database

| Path | Referenced by | Written by |
|---|---|---|
| `NETRA_DATA_DIR/anomalies/<YYYY-MM-DD>/<uid>.jpg` | `anomalies.image_path` | Robot anomaly upload; the date is the upload day in the process time zone |
| `NETRA_DATA_DIR/floorplans/<unix nanoseconds><ext>` | `floor_plans.file_path` | Floor-plan upload, or the PNG rendered from a robot map (`.png`); deleted with the plan |
| `NETRA_DATA_DIR/geo/overlay-<unix nanoseconds><ext>` | `geo_site.overlay_path` | Overlay upload (`.png`, `.jpg` or `.webp`); deleted when replaced or removed |
| `NETRA_DATA_DIR/inspections/<YYYY-MM-DD>/<uid>-<name><ext>` | `inspections.images` | Robot inspection upload: `thermal`, `visual`, `acoustic`, `.jpg` or `.png`; kept with the inspection |

The stored paths are built from `NETRA_DATA_DIR` at upload time (relative paths stay relative to
the working directory). Back up the data directory together with the database, and keep
`NETRA_DATA_DIR` stable or rewrite the stored paths when moving it.
