# Dashboard API

The REST and WebSocket API under `/api/v1` that the dashboard (DarmawanWeb/netra-fleet-fe) uses.
All routes are registered in `Server.Handler` in [`server.go`](../internal/api/server.go). Robots use
the [robot endpoints](#robot-endpoints) and third-party applications the
[integration API](#integration-api); both are only summarised here.

## Conventions

- Requests and responses are JSON (UTF-8) unless stated. Request bodies are limited to 1 MiB;
  unknown fields are ignored and omitted fields take their zero value (`false`, `""`, `0`, `null`).
- Times are RFC 3339. Robot IDs, `uid`s and integration `client_id`s are strings; other IDs are
  integers.
- Time ranges use `from` and `to` (RFC 3339). A missing or unparsable `to` is now; a missing or
  unparsable `from` is 7 days before `to`. Invalid values fall back silently.
- Lists are plain JSON arrays without paging. Limits: 500 patrols, anomalies or events, 1000 audit
  entries, 3000 track points.
- Updates answer 204 without a body unless the table says otherwise.
- In the tables, `{a, b}` lists the keys of a JSON object; complete examples follow several tables.
- Every request with a session token that is not `GET` or `HEAD` and succeeds (status below 400)
  is written to the audit log ([architecture](architecture.md#audit-log)); robot routes are not
  audited.

## Authentication

Log in with a username and password:

```bash
curl -s -X POST https://netra.example.com/api/v1/auth/login \
  -H 'Content-Type: application/json' -d '{"username": "admin", "password": "change-me"}'
```

```json
{
  "token": "q3Xc0n8V...",
  "expires_at": "2026-10-19T01:00:00.482913311Z",
  "username": "admin",
  "role": "admin",
  "permissions": ["view", "operate", "configure", "audit", "users"]
}
```

Send the token as `Authorization: Bearer <token>` on every other request. Where a browser cannot
set headers (the WebSocket feed, `<img>` photos, file downloads and reports), pass it as
`?token=<token>` instead; the header wins when both are present. Sessions last
`NETRA_SESSION_HOURS` (72 h by default) and end with `POST /api/v1/auth/logout`. Each route needs one
permission; the role-to-permission table is in [architecture](architecture.md#roles-and-permissions).

## Errors

Errors are JSON with a single human-readable message:

```json
{"error": "your role cannot do this"}
```

| Status | Meaning |
|---|---|
| 400 | Invalid body or parameter; the message says what is expected |
| 401 | `login required` (missing, unknown or expired session, or disabled user); `wrong username or password` at login; `unknown robot token` on robot routes |
| 403 | `your role cannot do this`; `feature not licensed` with `"feature": "<module>"` on a module the license does not include ([licensing](licensing.md)); `robot limit of the license reached` with `robot_limit`; on robot routes an `X-Robot-Id` that does not match the token, or another robot's route |
| 404 | `not found` |
| 409 | Robot offline, robot ID or username already taken, patrol cannot start, last enabled admin; `this deployment uses <mode> maps` with `"map_mode": "<mode>"` on a route of another [map mode](#map-modes); a robot map cannot be calibrated; an asset tag already used, an asset or point still in use, a finding in a status that does not allow the change, an inspection point's kind |
| 422 | The robot refused `set_inspection` or `set_people`; body `{"ok": false, "msg": "..."}` |
| 429 | Too many failed logins from this IP (8 within 10 minutes) |
| 500 | Database or file error; the message is the error text |
| 504 | The robot did not acknowledge within 10 s |

Unknown paths and wrong methods get the Go router's plain-text 404 or 405, and a photo or floor-plan
file missing on disk gets a plain-text 404.

## Routes

### Session and account

| Method | Path | Permission | What it does |
|---|---|---|---|
| POST | `/api/v1/auth/login` | none | `{username, password}` → `{token, expires_at, username, role, permissions}`; 401 after a 400 ms delay on wrong credentials or a disabled user; 429 when the IP is throttled |
| POST | `/api/v1/auth/logout` | view | Deletes the current session |
| GET | `/api/v1/me` | view | → `{username, display_name, role, permissions, timezone}` (`timezone` is `NETRA_TZ`) |
| POST | `/api/v1/me/password` | view | `{current, new}` (`new` at least 8 characters); ends the user's other sessions; 400 when `current` is wrong |

### Info, health and metrics

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/healthz` | none | `{"status": "ok"}` while the process serves HTTP (no database check) |
| GET | `/readyz` | none | `{"status": "ready", "version"}` when the database answers too (and NATS in a split installation), else 503 with `database unavailable` or `nats unavailable` |
| GET | `/api/v1/info` | view | → `{public_url, time_zone, version, installation_id, edition, tier, features, map_mode, robot_limit, license, branding}`: `NETRA_PUBLIC_URL` (the address robots connect to), `NETRA_TZ`, the build version, this server's installation ID, the licensed modules in force, the map mode (`floorplan`, `pgm` or `gps`), the robot limit (0: none) and the license and branding as below |
| GET | `/api/v1/setup` | none | → `{required, installation_id, version, license}`: `required` while no user exists (a new installation); the dashboard then opens the setup wizard |
| POST | `/api/v1/setup` | none | Only while `required`. `{username, display_name, password, license}` (`license` optional) → 201 like a login, plus `license`: creates the first administrator, installs the license; 400 for a bad username, password or license (nothing is created), 409 once a user exists |
| GET | `/api/v1/branding` | none | → `{name, color, color_dark, logo}` from the license (`name` `NETRA` without one); `logo` is the URL of the logo or absent. Public: the login page uses it |
| GET | `/api/v1/branding/logo` | none | The logo of the license (SVG or PNG), `ETag`, cached 5 minutes; 404 without one |
| GET | `/metrics` | none | Prometheus text format ([metrics](architecture.md#metrics)) |
| GET | `/api/v1/ws` | view | Live WebSocket feed, see [below](#live-websocket-feed) |

### System

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/system` | users | → `{version, installation_id, started_at, uptime_s, time_zone, tls, database: {size_bytes, managed}, disk: {path, data_bytes, data_files, data_complete, free_bytes, total_bytes}, backups: {enabled, keep_days, at, dir, list}, robots: {registered, online}, license, map_mode}`; `list` is `{name, size, created}` newest first; the data size is cached for 5 minutes |
| POST | `/api/v1/system/backups` | users | Makes a database backup now → 201 `{name, size, created}`; 409 when built-in backups are off |
| GET | `/api/v1/system/backups/{name}` | users | Downloads a backup (`netra-YYYYMMDD-HHMMSS.dump`); 404 for any other name |

### License

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/license` | users | → `{installation_id, map_modes, license, catalog, editions, tiers, robots, robot_limit}`: `license` is `{status, id, client, edition, tier, features, map, max_robots, installation, issued_at, expires_at, grace_ends, source, error}` (`installation` the server the license is bound to, absent when unbound) (`status` `missing`, `invalid`, `valid`, `grace` or `expired`; `features` empty unless `valid` or `grace`; `source` `file`, `env` or `upload`; `error` why the last license given was refused), `catalog` every module `{ID, Description}`, `robots` the number registered |
| PUT | `/api/v1/license` | users | Body: the license text, or `{"license": "..."}`. Verifies and applies it at once, keeps it for the next start, broadcasts `{"type": "license"}` on the live feed → the new `license`; 400 when the signature or format is wrong, 409 when it was issued before the current license |

### Robots and control

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/robots` | view | → robots ordered by ID with `online` and `clock_skew_s` |
| GET | `/api/v1/robots/{id}/track` | view | `from`, `to` → telemetry history (one sample per 5 s) thinned evenly to at most 3000 points |
| POST | `/api/v1/robots` | configure | `{id, name}`: `id` matches `^[a-z0-9][a-z0-9_-]{1,31}$`, `name` defaults to `id` → 201 `{id, token}`, the only time the token is shown; 409 when the insert fails (ID taken); 403 `{error, robot_limit}` when the license's robot limit is reached |
| PUT | `/api/v1/robots/{id}` | configure | `{name, floor_plan_id}`: `name` required, an omitted `floor_plan_id` clears it |
| DELETE | `/api/v1/robots/{id}` | configure | Deletes the robot; the database cascades to its routes, schedules, patrols, anomalies, crowd samples and events (telemetry history and files stay) |
| POST | `/api/v1/robots/{id}/token` | configure | → `{id, token}`, a new robot token; the old one is refused from now on |
| POST | `/api/v1/robots/{id}/command` | operate | `{name, args}`, `name` one of `pause`, `resume`, `localize`, `stop_patrol`, `estop` (latches the robot's software e-stop), `estop_release` → 202 `{command_id}` without waiting for the ack; 400 for other names (`start_patrol` goes through `POST /api/v1/patrols`); 409 robot offline |
| POST | `/api/v1/robots/{id}/inspection` | operate, `anomaly` | `{enabled, model, interval}`, at least one: `model` from `GET /api/v1/inspection/models`, `interval` in seconds up to 3600 (0 continuous, negative at stop points only) → sends `set_inspection` and waits up to 10 s: 200 `{ok: true, msg}`, 422 `{ok: false, msg}`, 504 no ack, 409 offline |
| POST | `/api/v1/robots/{id}/people` | operate, `crowd` | `{enabled}` (required) → sends `set_people`; answers as for inspection |
| GET | `/api/v1/inspection/models` | view, `anomaly` | → array of the model names in `NETRA_INSPECTION_MODELS` |

```json
{
  "id": "l1w-01",
  "name": "L1W 01",
  "floor_plan_id": 1,
  "last_seen": "2026-10-16T01:20:11.402113Z",
  "last_telemetry": {"type": "telemetry", "ts": 1792113611.4, "pose": {"x": 41.2, "y": -6.8, "yaw": 0.12}},
  "created_at": "2026-10-01T02:00:00.512309Z",
  "online": true,
  "clock_skew_s": 0.3
}
```

`last_telemetry` is the robot's last telemetry message as sent. `clock_skew_s` is how many seconds
the robot clock is behind the server (negative: ahead), `null` until the robot has sent telemetry
since the server started. A track point is
`{ts, x, y, yaw, lat, lon, battery, nav_state, people, cpu_percent, gpu_percent, memory_percent, memory_used_gb, memory_total_gb, cpu_temp_c}`;
position, battery and load are `null` when the robot did not report them (`lat` and `lon` are its
[GPS](#gps-in-telemetry) position).

### Routes

Routes are created by robots (`POST /api/v1/robot/routes`). They can be renamed and archived but not
deleted.

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/routes` | view | `robot_id`, `archived=true` (archived routes; otherwise active ones) → routes, last recorded first, without points |
| GET | `/api/v1/routes/{id}` | view | → the route with `points`, an array of `[x, y, yaw_deg, dwell_sec]` from its CSV |
| PUT | `/api/v1/routes/{id}` | configure | `{name, archived}`, `name` required |
| GET | `/api/v1/routes/{id}/file` | view | → the route CSV (`text/csv`, attachment `route-<id>.csv`) |

```json
{
  "id": 5,
  "uid": "0b7c3a5e-2f4d-4f3b-9d1e-7a6c5b4d3e2f",
  "robot_id": "l1w-01",
  "name": "Corridor L1",
  "recorded_at": "2026-10-10T03:12:00.25Z",
  "length_m": 17.1,
  "stops": 1,
  "closed": true,
  "archived": false,
  "created_at": "2026-10-10T03:15:31.817244Z",
  "points": [[0, 0, 0, 0], [5, 0, 90, 10], [5, 5, 180, 0], [0, 0, 0, 0]]
}
```

### Schedules

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/schedules` | view | → schedules ordered by `time_of_day` |
| POST | `/api/v1/schedules` | operate | Schedule body → 200 `{id}` |
| PUT | `/api/v1/schedules/{id}` | operate | Schedule body, replaces every field → 200 `{id}`; 404 unknown ID |
| DELETE | `/api/v1/schedules/{id}` | operate | Deletes the schedule |

```json
{"robot_id": "l1w-01", "route_id": 5, "name": "Morning round", "days": [1, 3, 5], "time_of_day": "09:30", "enabled": true, "once_on": null}
```

- `robot_id`, `route_id` and `time_of_day` (`HH:MM` in `NETRA_TZ`) are required.
- Give `days` (0 = Sunday to 6 = Saturday) for a weekly schedule, or `once_on` (`YYYY-MM-DD`) for a
  single date; with `once_on` the days are cleared.
- `name` defaults to `Patrol HH:MM`. `enabled` is `false` when omitted.
- The route is not checked against the robot here; a mismatch fails when the schedule fires.
- Listed schedules also carry `id` and `last_run_on`, the date the schedule last fired.

### Patrols and events

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/patrols` | view | `robot_id`, `from`, `to` (on `created_at`) → patrols, newest first |
| POST | `/api/v1/patrols` | operate | `{robot_id, route_id}` → 202 `{id}` once `start_patrol` was sent; 409 when the route is unknown or belongs to another robot, or the robot is offline (the patrol then exists as `failed`) |
| GET | `/api/v1/patrols/{id}` | view | → `{patrol, recap}` |
| POST | `/api/v1/patrols/{id}/stop` | operate | Sends `stop_patrol` → 202 without body; if the robot is offline the patrol is set `stopped` at once |
| GET | `/api/v1/events` | view | `robot_id`, `patrol_id` → the 500 newest events `{id, robot_id, patrol_id, type, payload, ts}`; `payload` is the robot message |

```json
{
  "patrol": {
    "id": 42,
    "robot_id": "l1w-01",
    "route_id": 5,
    "schedule_id": null,
    "status": "finished",
    "message": "goal_reached",
    "laps": 1,
    "started_at": "2026-10-16T02:30:04.118502Z",
    "finished_at": "2026-10-16T03:12:40.903377Z",
    "created_at": "2026-10-16T02:30:00.041913Z"
  },
  "recap": {
    "anomalies": {"trash": 1, "floor_damage": 1},
    "events": {"patrol_event": 2, "arrived": 1},
    "crowd_peak": 4
  }
}
```

Patrol statuses are `requested`, `running`, `finished`, `stopped` and `failed`
([lifecycle](architecture.md#patrol-lifecycle-and-scheduler)). In the recap, anomalies are counted
per category without `rejected` ones, and `stop_event` events are counted by their `event` field.

### Anomalies

Module `anomaly` ([licensing](licensing.md#feature-modules)), with the rules and on-robot inspection.

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/anomalies` | view | `status`, `category` (final category, else early), `robot_id`, `patrol_id`, `from`, `to` (on `detected_at`) → anomalies, newest detection first |
| GET | `/api/v1/anomalies/{id}` | view | → one anomaly |
| GET | `/api/v1/anomalies/{id}/image` | view | → the robot's JPEG, `Cache-Control: private, max-age=86400` |
| POST | `/api/v1/anomalies/{id}/validate` | operate | `{decision, category, note}`: `decision` is `confirm` or `reject`; a non-empty `category` replaces the final category and a non-empty `note` the final note; confirming runs the Telegram rule |

```json
{
  "id": 1287,
  "uid": "6f1c2b0e-3a4d-4c1e-9b8a-2d5f7e9c1a3b",
  "robot_id": "l1w-01",
  "patrol_id": 42,
  "route_id": 5,
  "detected_at": "2026-10-16T02:41:11.402113Z",
  "early_category": "trash",
  "early_confidence": 0.74,
  "early_note": "a plastic bottle on the floor",
  "final_category": "trash",
  "final_confidence": 0.91,
  "final_note": "plastic bottle and paper cup",
  "x": 41.2,
  "y": -6.8,
  "yaw": 0.12,
  "stop_index": 12,
  "status": "confirmed",
  "validated_by": null,
  "validated_at": null,
  "notified_at": "2026-10-16T02:41:39.118245Z",
  "created_at": "2026-10-16T02:41:12.003311Z"
}
```

Statuses: `queued`, `analyzing`, `needs_validation`, `confirmed`, `rejected`, `dismissed`
([pipeline](architecture.md#anomaly-pipeline)). `final_*` fields are `null` (or empty) until the
final detection ran; position fields are `null` when the robot sent no pose.

### Rules

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/rules` | view | → rules ordered by category |
| PUT | `/api/v1/rules/{category}` | configure | Rule body, `label` required; creates or replaces the rule of the path's category |
| DELETE | `/api/v1/rules/{category}` | configure | Deletes the rule; 404 for an unknown category and for `unknown`, the fallback rule |

```json
{"category": "floor_damage", "label": "Floor damage", "requires_validation": true, "notify_telegram": true, "telegram_chat_id": "", "min_confidence": 0.5}
```

`telegram_chat_id` empty means `NETRA_TELEGRAM_CHAT_ID`. Anomalies whose final confidence is below
`min_confidence` are dismissed.

### Crowd

Module `crowd`, with `POST /api/v1/robots/{id}/people`.

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/crowd/heatmap` | view | `robot_id`, `from`, `to` → cells `{x, y, size, peak, mean, samples}`, `x`, `y` and `size` in map metres |
| GET | `/api/v1/crowd/timeline` | view | `robot_id`, `bucket` (`hour` by default, or `day`), `from`, `to` → `{bucket, peak}` points, buckets truncated in `NETRA_TZ` |

### Substation

Module `substation`: the equipment, the inspection points robots take on their routes, what they
measured, and the findings operators follow up. How readings are rated, the findings' life and the
robot side are in [substation](substation.md).

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/substation/summary` | view | → `{assets, points, findings, unacknowledged, assets_by_condition, inspections_24h, last_inspection}`: findings not resolved by priority (`"1"` to `"4"`), open ones nobody acknowledged, assets by their most severe priority (`"ok"` for none) |
| GET | `/api/v1/substation/criteria` | view | → `{thermal, asset_kinds, point_kinds, references}`: NETA MTS Table 100.18 as `{priority, similar, ambient, action}` rows, and the choices of the forms |
| GET | `/api/v1/substation/settings` | view | → `{finding_priority, alert_priority, pd: {watch_db, alarm_db}, telegram_chat_id}` |
| PUT | `/api/v1/substation/settings` | configure | The settings; fields not sent keep their value → the settings |
| GET | `/api/v1/substation/assets` | view | `archived=true` adds archived assets → assets by bay and tag with their condition (`priority`, `open_findings`, `last_inspected`) |
| POST | `/api/v1/substation/assets` | configure | `{tag, name, kind, bay, voltage_kv, phase, x, y, notes, archived}` → 201 the asset; 409 when the tag is taken (ignoring case) |
| GET | `/api/v1/substation/assets/{id}` | view | → one asset |
| PUT | `/api/v1/substation/assets/{id}` | configure | The asset body → the asset |
| DELETE | `/api/v1/substation/assets/{id}` | configure | 409 while a point or a finding refers to it: archive it instead |
| GET | `/api/v1/substation/points` | view | `route_id`, `enabled=true` → points by route, stop and `seq`, with the route's `robot_id` and the robot's `stance` `{x, y, yaw_deg}` at the stop |
| POST | `/api/v1/substation/points` | configure | `{name, kind, asset_id, route_id, stop_index, seq, ptz, settings, targets, enabled}` → 201 the point |
| GET | `/api/v1/substation/points/{id}` | view | → one point |
| PUT | `/api/v1/substation/points/{id}` | configure | The point body; 409 when `kind` changes → the point |
| DELETE | `/api/v1/substation/points/{id}` | configure | 409 when it has inspections: disable it instead |
| GET | `/api/v1/substation/points/{id}/trend` | view | `from` (30 days before `to` by default), `to` → `{point_id, kind, series: [{key, label}], samples: [{inspection_id, taken_at, ambient_c, priority, values: {key: °C or dB}}]}`, oldest first, at most 2000 |
| POST | `/api/v1/substation/points/{id}/inspect` | operate | Sends `inspect_point` to the robot of the point's route and waits for the ack: 200 `{ok, msg}`, 422 refused, 409 robot offline or no route, 504 no answer |
| GET | `/api/v1/substation/inspections` | view | `point_id`, `patrol_id`, `robot_id`, `priority` (rated this or more severe), `from`, `to`, `limit` (500, at most 5000) → inspections, newest first |
| GET | `/api/v1/substation/inspections/{id}` | view | → one inspection |
| GET | `/api/v1/substation/inspections/{id}/image/{name}` | view | `thermal`, `visual` or `acoustic` → the JPEG or PNG; 404 when the capture has none |
| GET | `/api/v1/substation/findings` | view | `status` (`open`, `acknowledged`, `resolved`, or `active` for the first two), `priority`, `asset_id`, `point_id`, `from` and `to` (on `last_seen`; any time without them), `limit` → findings: not resolved first, then by priority and latest |
| GET | `/api/v1/substation/findings/{id}` | view | → one finding |
| POST | `/api/v1/substation/findings/{id}/acknowledge` | operate | Optional `{note}` → the finding; 409 unless it is open |
| POST | `/api/v1/substation/findings/{id}/resolve` | operate | Optional `{note}` → the finding; 409 when it is resolved |

A point, as returned:

```json
{
  "id": 3, "name": "Line 1 disconnector", "kind": "thermal", "asset_id": 1, "route_id": 7, "stop_index": 0,
  "seq": 0, "ptz": {"pan_deg": -30, "tilt_deg": 12, "zoom": 4},
  "settings": {"reference": "auto", "emissivity": 0.95},
  "targets": [
    {"key": "r", "label": "Clamp R", "asset_id": 1, "phase": "R", "group": "clamp", "roi": {"x": 0.1, "y": 0.4, "w": 0.2, "h": 0.2}, "limit_c": null},
    {"key": "t", "label": "Clamp T", "asset_id": 1, "phase": "T", "group": "clamp", "roi": {"x": 0.7, "y": 0.4, "w": 0.2, "h": 0.2}, "limit_c": null}
  ],
  "enabled": true, "created_at": "2026-11-04T07:02:11Z", "updated_at": "2026-11-04T07:02:11Z",
  "last_inspected": "2026-11-04T08:15:45Z", "priority": 1, "robot_id": "gi-01",
  "stance": {"x": 10, "y": 0, "yaw_deg": 90}
}
```

An inspection keeps every target's rating; `thermal` or `pd` depending on the point:

```json
{
  "id": 41, "uid": "f0b3c1d2-7a8e-4b1f-9c2d-3e4f5a6b7c8d", "point_id": 3, "point_name": "Line 1 disconnector",
  "robot_id": "gi-01", "patrol_id": 42, "taken_at": "2026-11-04T08:15:45Z", "kind": "thermal",
  "reference": "auto", "ambient_c": 31,
  "thermal": [
    {"key": "r", "max_c": 40.2, "label": "Clamp R", "asset_id": 1, "ref_key": "t", "delta_similar_c": -18.1, "delta_ambient_c": 9.2, "priority": 0},
    {"key": "t", "max_c": 58.3, "label": "Clamp T", "asset_id": 1, "ref_key": "r", "delta_similar_c": 18.1, "delta_ambient_c": 27.3, "priority": 1, "basis": "similar"}
  ],
  "priority": 1, "x": 10, "y": 0.1, "yaw": 1.57, "images": ["thermal", "visual"],
  "created_at": "2026-11-04T08:15:46Z"
}
```

A finding:

```json
{
  "id": 12, "point_id": 3, "point_name": "Line 1 disconnector", "target_key": "t", "label": "Clamp T",
  "asset_id": 1, "asset_tag": "PMS-L1", "asset_name": "Disconnector line 1", "kind": "thermal",
  "status": "acknowledged", "priority": 1, "basis": "similar", "last_priority": 3,
  "first_inspection_id": 41, "last_inspection_id": 44, "first_seen": "2026-11-04T08:15:45Z",
  "last_seen": "2026-11-05T08:14:02Z", "last_value": 50, "last_delta_c": 10, "occurrences": 2,
  "note": "WO-2211 hot clamp", "acknowledged_by": "supervisor", "acknowledged_at": "2026-11-04T09:01:13Z",
  "resolved_by": null, "resolved_at": null, "notified_at": "2026-11-04T08:15:47Z",
  "created_at": "2026-11-04T08:15:46Z", "updated_at": "2026-11-05T08:14:03Z"
}
```

### Map modes

The license fixes the kind of map a deployment draws on (`map_mode` in `GET /api/v1/info`):
`floorplan` (an uploaded layout calibrated by hand), `pgm` (the robot's own occupancy grid) or
`gps` (outdoor, latitude and longitude). Uploads exist only for the deployment's kind of map, and
the georeference only in `gps`; other modes answer
`409 {"error": "this deployment uses <mode> maps", "map_mode": "<mode>"}`. Plans that exist stay
viewable and assignable in every mode ([architecture](architecture.md#map-modes)).

| Route | `floorplan` | `pgm` | `gps` |
|---|---|---|---|
| `POST /api/v1/floorplans` | yes | 409 | 409 |
| `POST /api/v1/floorplans/pgm` | 409 | yes | 409 |
| `/api/v1/geo...` | 409 | 409 | yes |
| other floor plan routes | yes | yes | yes |

### Floor plans

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/floorplans` | view | → plans ordered by ID, with calibration |
| POST | `/api/v1/floorplans` | configure, `floorplan` maps | Multipart: `file` (`.svg`, `.png`, `.jpg`, `.jpeg` or `.pdf`, up to 32 MiB), optional `name` (default: the file name without extension), `width_px`, `height_px` → 201 `{id}` |
| POST | `/api/v1/floorplans/pgm` | configure, `pgm` maps | A robot map, multipart: `yaml` (the `map.yaml` text, as a field or a file), `image` (its `.pgm`, P5 8- or 16-bit or plain P2, or a PNG), optional `name` (default: the yaml file name, else the image's) → 201 `{id, name, width_px, height_px, resolution, origin}`; 400 with the reason for a yaml or image that cannot be read. Up to 64 MiB, 10 000 px a side |
| PUT | `/api/v1/floorplans/{id}` | configure | Calibration body, `name` and `px_per_m` > 0 required; replaces name, size and calibration; 409 for a robot map, which its `map.yaml` places |
| DELETE | `/api/v1/floorplans/{id}` | configure | Deletes the plan and its file; robots showing it get `floor_plan_id` `null` |
| GET | `/api/v1/floorplans/{id}/file` | view | → the file with its content type and `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'` |

```json
{
  "id": 1,
  "name": "Ground floor",
  "kind": "floorplan",
  "content_type": "image/svg+xml",
  "width_px": 2400,
  "height_px": 1600,
  "origin_x_px": 812,
  "origin_y_px": 1033,
  "px_per_m": 20,
  "rotation_rad": 0,
  "created_at": "2026-10-01T02:00:00.779102Z",
  "resolution": null,
  "origin": null
}
```

A map position `(x, y)` in metres becomes a plan pixel with
`px = origin_x_px + s·(cos θ·x + sin θ·y)` and `py = origin_y_px + s·(sin θ·x − cos θ·y)`, where
`s = px_per_m` and `θ = rotation_rad` (the same formula as in [INTEGRATION.md](../INTEGRATION.md)).

A robot map has `kind` `pgm`, `content_type` `image/png` (the map rendered with the occupancy of
ROS map_server: free light, occupied dark, unknown grey), `resolution` (metres per pixel) and
`origin` (`[x, y, yaw]` of the lower-left pixel), both from its `map.yaml`. Its calibration comes
from them, so the formula above puts map positions on the right pixel:

```text
px_per_m     = 1 / resolution
rotation_rad = yaw
origin_x_px  = −px_per_m · (cos yaw · ox + sin yaw · oy)
origin_y_px  = height_px + px_per_m · (cos yaw · oy − sin yaw · ox)
```

`map.yaml` is read with the flat keys `image`, `resolution`, `origin` (`[x, y, yaw]`, as a flow or
block list), `negate` (0 or 1), `occupied_thresh` and `free_thresh` (default 0.65 and 0.196, the
values `map_saver` writes) and `mode` (`trinary` by default, `scale` or `raw`), as nav2
map_server reads them.

### Site georeference

`gps` maps only. One georeference per deployment places the robot map frame on the earth, and
says what to draw under it.

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/geo` | view | → the site, below |
| PUT | `/api/v1/geo` | configure | `{points, heading_deg, basemap}`: replaces the control points and the basemap and fits the transform → the site; 400 with the reason when the points cannot be fitted or the basemap is invalid. No points and no heading: no georeference (the basemap is still saved). Broadcasts `{"type": "geo"}` |
| POST | `/api/v1/geo/fit` | configure | `{points, heading_deg}` → `{points, transform}`: the fit without saving it, as the site page shows it while points are edited; 400 when it cannot be fitted |
| POST | `/api/v1/geo/overlay` | configure | Multipart `image` (PNG, JPEG or WebP, north up, up to 64 MiB), `north`, `south`, `east`, `west` (degrees, at most 1° across) and optional `opacity` (0.05 to 1, default 1) → the site. Without an image (or as a plain form) only the bounds and opacity of the current image change. Broadcasts `geo` |
| GET | `/api/v1/geo/overlay/file` | view | → the overlay image (`?token=` like other files), cached a day; 404 without one |
| DELETE | `/api/v1/geo/overlay` | configure | Removes the overlay and its file → 204. Broadcasts `geo` |

```json
{
  "points": [
    {"label": "Gate", "x": 0, "y": 0, "lat": -6.2, "lon": 106.816, "residual_m": 0.16},
    {"label": "Transformer T1", "x": 60, "y": 0, "lat": -6.19973, "lon": 106.81647, "residual_m": 0.09},
    {"label": "Control room", "x": 0, "y": 40, "lat": -6.19969, "lon": 106.81582, "residual_m": 0.14}
  ],
  "heading_deg": null,
  "transform": {
    "ref_lat": -6.1998067, "ref_lon": 106.8160967, "scale": 0.99825, "rotation_rad": 0.52466,
    "east_m": -10.61, "north_m": -21.52, "method": "points", "rms_m": 0.13, "heading_deg": 59.94
  },
  "basemap": {"kind": "osm", "url": "https://tile.openstreetmap.org/{z}/{x}/{y}.png", "attribution": "© OpenStreetMap contributors", "max_zoom": 19},
  "overlay": {"url": "/api/v1/geo/overlay/file?v=overlay-1793769951211781164", "content_type": "image/png",
    "north": -6.199, "south": -6.201, "east": 106.8175, "west": 106.815, "opacity": 0.9, "updated_at": "2026-11-04T05:25:51.211902Z"},
  "presets": [{"kind": "osm", "...": "..."}, {"kind": "satellite", "...": "..."}],
  "updated_by": "admin",
  "updated_at": "2026-11-04T05:25:51.211902Z"
}
```

- **Control points** `{label, x, y, lat, lon}` are the same places in map metres and WGS84
  degrees (at most 64, labels up to 64 characters). Two or more are fitted by least squares; one
  needs `heading_deg`, the compass heading of the map x axis (degrees clockwise from north), and
  gives a scale of 1. `heading_deg` is ignored with two or more points.
- **Transform**: `east = scale·(cos θ·x − sin θ·y) + east_m`, `north = scale·(sin θ·x + cos θ·y) + north_m`
  with `θ = rotation_rad`, in the tangent plane of the WGS84 ellipsoid at `ref_lat`, `ref_lon` (the
  centre of the points), then onto the ellipsoid; the inverse undoes it exactly. `heading_deg` of
  the transform is the compass heading of the map x axis, a robot's heading is
  `heading_deg − yaw` in degrees. `residual_m` is the ground distance between a point and where the
  transform puts its map position, `rms_m` their root mean square; with two points both are 0.
- **Refused fits** (400): fewer than two points without a heading, points at one map position or
  one latitude/longitude, a scale outside 0.5 to 2 (the robot map is in metres, so map and earth
  positions are not the same places), points more than 20 km from their centre, positions that
  are not numbers or not on the earth.
- **Basemap** `{kind, url, attribution, max_zoom}`: `osm` (OpenStreetMap), `satellite` (Esri World
  Imagery), `custom` or `none`. For the presets the server fills in the URL, attribution and zoom.
  A custom `url` is a tile template with `{z}`, `{x}` and `{y}` (or `{-y}`) starting with
  `https://`, `http://` or `/` (a tile server behind the same proxy, for sites without internet),
  `max_zoom` 1 to 24 (default 19), `attribution` plain text up to 300 characters. Tiles load in the
  operator's browser, which needs to reach the tile server.
- **Overlay**: an image of the site (drone or aerial photo, north up) stretched over its bounds,
  drawn over the basemap; it works offline.

### Reports

All report routes take `lang` (`en` by default, or `id`) and, except the patrol PDF, `from` and `to`.
The patrol PDF is in the core; the summary and the exports need the module `reports.export`.
The answer is a file download (`Content-Disposition: attachment`).

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/reports/patrols/{id}/pdf` | view | → `patrol-<id>.pdf` |
| GET | `/api/v1/reports/summary` | view | `robot_id`, `format` (`xlsx`, otherwise PDF) → `netra-summary.pdf` or `netra-summary.xlsx` |
| GET | `/api/v1/reports/anomalies` | view | `status`, `category`, `robot_id` → `anomalies.xlsx` |
| GET | `/api/v1/reports/patrols` | view | `robot_id` → `patrols.xlsx` |
| GET | `/api/v1/reports/substation/findings` | view | Module `substation` too; the filters of the findings list → `findings.xlsx` |
| GET | `/api/v1/reports/substation/inspections` | view | Module `substation` too; `point_id`, `patrol_id`, `robot_id`, `priority` → `readings.xlsx`, a row per thermal target or pd reading |

The contents are described in [architecture](architecture.md#reports).

### Users

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/users` | users | → users ordered by username, `{id, username, display_name, role, disabled, last_login, created_at}` |
| POST | `/api/v1/users` | users | `{username, display_name, role, password}`: username 3 to 32 characters of `a-z 0-9 . _ -` starting with a letter or digit, role `viewer`, `operator`, `supervisor` or `admin`, password at least 8 characters → 201 `{id}`; 409 when the insert fails (username taken) |
| PUT | `/api/v1/users/{id}` | users | `{display_name, role, disabled}`, `role` required; disabling ends the user's sessions; 409 when no enabled admin would remain |
| DELETE | `/api/v1/users/{id}` | users | Deletes the user and their sessions; 409 for one's own account or the last enabled admin |
| POST | `/api/v1/users/{id}/password` | users | `{password}` (at least 8 characters); ends all the user's sessions |

### Audit

Module `audit`. Actions are recorded with or without it; the module is the page that reads them.

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/audit` | audit | `username`, `action` (exact match), `from`, `to` → up to 1000 entries, newest first, `{ts, username, action, target, detail, ip}` |

### Integration API clients

Module `integration`, which also gates the whole [integration API](#integration-api).

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/api-clients` | users | → `{clients, scopes}`: clients newest first, `scopes` the valid scope names |
| POST | `/api/v1/api-clients` | users | `{name, scopes}`: `name` 1 to 80 characters, `scopes` from the list → 201 `{client, client_secret}`; the secret is shown only here |
| PUT | `/api/v1/api-clients/{id}` | users | `{name, scopes, enabled}` (`enabled` optional, unchanged when omitted) → 200 the client; disabling revokes its tokens |
| POST | `/api/v1/api-clients/{id}/secret` | users | → `{client_id, client_secret}`; the old secret and every token issued with it stop working |
| DELETE | `/api/v1/api-clients/{id}` | users | Deletes the client and its tokens |

```json
{
  "client": {
    "id": 3,
    "client_id": "nc_3f9d0c1a2b7e4f60",
    "name": "Facility analytics",
    "scopes": ["anomalies:read", "robots:read"],
    "enabled": true,
    "created_by": "admin",
    "created_at": "2026-10-16T02:00:00.306615Z",
    "last_used_at": null
  },
  "client_secret": "ncs_..."
}
```

Scopes are `anomalies:read`, `robots:read`, `patrols:read` and `maps:read`; duplicates are dropped and
the list is stored in that order.

## Live WebSocket feed

Dashboards connect to `wss://<host>/api/v1/ws?token=<session token>` (`ws://` without TLS); the
`view` permission is required. The server sends JSON text messages; whatever the client sends is
read and discarded. The session is checked only when connecting. The server pings every 20 s and
queues at most 256 messages per dashboard, dropping messages for a client that falls behind, so
reload state from the REST API after a reconnect.

| `type` | Fields | Sent when |
|---|---|---|
| `robot_online` | `robot_id`, `online` | A robot WebSocket connects (`true`) or disconnects (`false`) |
| `hello`, `telemetry`, `patrol_event`, `stop_event`, `alert`, `crowd`, `ack` | `robot_id`, `data` (the robot's message as received) | The server handled a robot message |
| `patrol_requested` | `robot_id`, `patrol_id` | `start_patrol` was sent to the robot |
| `route` | `robot_id`, `id` | A robot uploaded a route (also when the upload repeats an existing one) |
| `anomaly` | `id`, `status`, and depending on the event `robot_id`, `category`, `notified` | Upload (`queued`, with `robot_id`), final detection (with `robot_id` and `category`), operator decision, Telegram alert sent (`notified: true`) |
| `license` | `license` (as in `GET /api/v1/license`) | A license was uploaded in the dashboard |
| `geo` | none | The site georeference or its overlay changed (`gps` maps) |
| `inspection` | `id`, `point_id`, `robot_id`, `priority`, `kind` | A robot uploaded a new inspection (module `substation`) |
| `finding` | `id`, `event`, `status`, `priority`, `point_id`, `asset_id`, and `last_priority` except for `notified` | A finding `opened`, was `seen` again, `escalated` or read `normal`; an operator `acknowledged` or `resolved` it; it was sent to Telegram (`notified`) |

```json
{"type": "robot_online", "robot_id": "l1w-01", "online": true}
{"type": "telemetry", "robot_id": "l1w-01", "data": {"type": "telemetry", "ts": 1792113611.4, "pose": {"x": 41.2, "y": -6.8, "yaw": 0.12}}}
{"type": "patrol_requested", "robot_id": "l1w-01", "patrol_id": 42}
{"type": "anomaly", "robot_id": "l1w-01", "id": 1287, "status": "queued"}
{"type": "anomaly", "robot_id": "l1w-01", "id": 1287, "status": "needs_validation", "category": "floor_damage"}
{"type": "anomaly", "id": 1287, "status": "confirmed"}
{"type": "anomaly", "id": 1287, "status": "confirmed", "notified": true}
```

## Robot endpoints

Robots authenticate with their robot token (`Authorization: Bearer <token>`, or `?token=`) and may
send `X-Robot-Id`, which must match the token's robot. The full protocol (message fields, command
semantics, upload metadata) is in the
[Pashupati robot API](https://github.com/DarmawanRobotics/Pashupati/blob/main/src/robot_fleet/API.md);
the server side is described in [architecture](architecture.md#robot-connection-lifecycle). In a
split installation these routes are served by the `gateway` processes and the others by the `api`
processes ([process roles](architecture.md#process-roles-and-split-installations)); `/healthz`,
`/readyz` and `/metrics` by every process.

| Method | Path | What it does |
|---|---|---|
| GET | `/api/v1/robot/ws` | WebSocket: telemetry, patrol and stop events, alerts, crowd windows and acks in; commands out. Crowd windows are dropped without the module `crowd` |
| POST | `/api/v1/robot/anomalies` | Multipart `meta` (JSON with `uid`) and `image.jpg` → 201 `{id, created}`; idempotent on `uid`. Without the module `anomaly`: 202 `{id: 0, created: false, dropped}`, nothing kept |
| POST | `/api/v1/robot/routes` | Multipart `meta` (JSON with `uid`) and `route.csv` → 201 `{id}`; idempotent on `uid` |
| GET | `/api/v1/robot/routes/{id}/file` | → the route CSV; 403 for a route of another robot |
| GET | `/api/v1/robot/routes/{id}/inspection` | → `{route_id, points}`: the route's enabled inspection points by stop; 403 for a route of another robot; empty without the module `substation` ([substation](substation.md#robot-protocol)) |
| POST | `/api/v1/robot/inspections` | Multipart `meta` (JSON with `uid` and `point_id`) and the images `thermal`, `visual`, `acoustic` → 201 `{id, created, priority}`; idempotent on `uid`. Without the module `substation`: 202 `{id: 0, created: false, dropped}`, nothing kept |

### GPS in telemetry

A robot with a GNSS receiver may add `gps` to its `telemetry` messages:

```json
{"type": "telemetry", "ts": 1792113611.4, "pose": {"x": 41.2, "y": -6.8, "yaw": 0.12},
 "gps": {"lat": -6.1997312, "lon": 106.8164705, "fix": "rtk_fixed", "hdop": 0.7}}
```

| Field | | |
|---|---|---|
| `lat`, `lon` | required | WGS84 degrees of the robot, ideally of the point its map pose describes (the base link) |
| `fix` | optional | `none`, `gps`, `dgps`, `rtk_float` or `rtk_fixed`; `none` means no position: send it (or leave `gps` out) while there is no fix |
| `hdop` | optional | Horizontal dilution of precision |

On a `gps` deployment the dashboard places the robot at its GPS position when there is one, and
otherwise at its map pose through the site georeference; the heading always comes from the pose
`yaw`, so keep sending `pose`. The 5 s history keeps `lat` and `lon` for trails and track replay.
Without `gps` nothing changes.

## Integration API

Third-party applications use the read-only API under `/api/ext/v1` with OAuth 2.0 client
credentials; clients are managed with the [API client routes](#integration-api-clients) above. The
guide is [INTEGRATION.md](../INTEGRATION.md); a running server serves Swagger UI at `/api/ext/docs`
and the OpenAPI description at `/api/ext/v1/openapi.yaml`.
