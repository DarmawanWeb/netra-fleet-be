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
| 403 | `your role cannot do this`; on robot routes an `X-Robot-Id` that does not match the token, or another robot's route |
| 404 | `not found` |
| 409 | Robot offline, robot ID or username already taken, patrol cannot start, last enabled admin |
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
| GET | `/api/v1/info` | view | → `{public_url, time_zone}`: `NETRA_PUBLIC_URL`, the address robots connect to, and `NETRA_TZ` |
| GET | `/metrics` | none | Prometheus text format ([metrics](architecture.md#metrics)) |
| GET | `/api/v1/ws` | view | Live WebSocket feed, see [below](#live-websocket-feed) |

### Robots and control

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/robots` | view | → robots ordered by ID with `online` and `clock_skew_s` |
| GET | `/api/v1/robots/{id}/track` | view | `from`, `to` → telemetry history (one sample per 5 s) thinned evenly to at most 3000 points |
| POST | `/api/v1/robots` | configure | `{id, name}`: `id` matches `^[a-z0-9][a-z0-9_-]{1,31}$`, `name` defaults to `id` → 201 `{id, token}`, the only time the token is shown; 409 when the insert fails (ID taken) |
| PUT | `/api/v1/robots/{id}` | configure | `{name, floor_plan_id}`: `name` required, an omitted `floor_plan_id` clears it |
| DELETE | `/api/v1/robots/{id}` | configure | Deletes the robot; the database cascades to its routes, schedules, patrols, anomalies, crowd samples and events (telemetry history and files stay) |
| POST | `/api/v1/robots/{id}/token` | configure | → `{id, token}`, a new robot token; the old one is refused from now on |
| POST | `/api/v1/robots/{id}/command` | operate | `{name, args}`, `name` one of `pause`, `resume`, `localize`, `stop_patrol`, `estop` (latches the robot's software e-stop), `estop_release` → 202 `{command_id}` without waiting for the ack; 400 for other names (`start_patrol` goes through `POST /api/v1/patrols`); 409 robot offline |
| POST | `/api/v1/robots/{id}/inspection` | operate | `{enabled, model, interval}`, at least one: `model` from `GET /api/v1/inspection/models`, `interval` in seconds up to 3600 (0 continuous, negative at stop points only) → sends `set_inspection` and waits up to 10 s: 200 `{ok: true, msg}`, 422 `{ok: false, msg}`, 504 no ack, 409 offline |
| POST | `/api/v1/robots/{id}/people` | operate | `{enabled}` (required) → sends `set_people`; answers as for inspection |
| GET | `/api/v1/inspection/models` | view | → array of the model names in `NETRA_INSPECTION_MODELS` |

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
`{ts, x, y, yaw, battery, nav_state, people, cpu_percent, gpu_percent, memory_percent, memory_used_gb, memory_total_gb, cpu_temp_c}`;
position, battery and load are `null` when the robot did not report them.

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

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/crowd/heatmap` | view | `robot_id`, `from`, `to` → cells `{x, y, size, peak, mean, samples}`, `x`, `y` and `size` in map metres |
| GET | `/api/v1/crowd/timeline` | view | `robot_id`, `bucket` (`hour` by default, or `day`), `from`, `to` → `{bucket, peak}` points, buckets truncated in `NETRA_TZ` |

### Floor plans

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/floorplans` | view | → plans ordered by ID, with calibration |
| POST | `/api/v1/floorplans` | configure | Multipart: `file` (`.svg`, `.png`, `.jpg`, `.jpeg` or `.pdf`, up to 32 MiB), optional `name` (default: the file name without extension), `width_px`, `height_px` → 201 `{id}` |
| PUT | `/api/v1/floorplans/{id}` | configure | Calibration body, `name` and `px_per_m` > 0 required; replaces name, size and calibration |
| DELETE | `/api/v1/floorplans/{id}` | configure | Deletes the plan and its file; robots showing it get `floor_plan_id` `null` |
| GET | `/api/v1/floorplans/{id}/file` | view | → the file with its content type and `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'` |

```json
{
  "id": 1,
  "name": "Ground floor",
  "content_type": "image/svg+xml",
  "width_px": 2400,
  "height_px": 1600,
  "origin_x_px": 812,
  "origin_y_px": 1033,
  "px_per_m": 20,
  "rotation_rad": 0,
  "created_at": "2026-10-01T02:00:00.779102Z"
}
```

A map position `(x, y)` in metres becomes a plan pixel with
`px = origin_x_px + s·(cos θ·x + sin θ·y)` and `py = origin_y_px + s·(sin θ·x − cos θ·y)`, where
`s = px_per_m` and `θ = rotation_rad` (the same formula as in [INTEGRATION.md](../INTEGRATION.md)).

### Reports

All report routes take `lang` (`en` by default, or `id`) and, except the patrol PDF, `from` and `to`.
The answer is a file download (`Content-Disposition: attachment`).

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/reports/patrols/{id}/pdf` | view | → `patrol-<id>.pdf` |
| GET | `/api/v1/reports/summary` | view | `robot_id`, `format` (`xlsx`, otherwise PDF) → `netra-summary.pdf` or `netra-summary.xlsx` |
| GET | `/api/v1/reports/anomalies` | view | `status`, `category`, `robot_id` → `anomalies.xlsx` |
| GET | `/api/v1/reports/patrols` | view | `robot_id` → `patrols.xlsx` |

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

| Method | Path | Permission | What it does |
|---|---|---|---|
| GET | `/api/v1/audit` | audit | `username`, `action` (exact match), `from`, `to` → up to 1000 entries, newest first, `{ts, username, action, target, detail, ip}` |

### Integration API clients

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
the server side is described in [architecture](architecture.md#robot-connection-lifecycle).

| Method | Path | What it does |
|---|---|---|
| GET | `/api/v1/robot/ws` | WebSocket: telemetry, patrol and stop events, alerts, crowd windows and acks in; commands out |
| POST | `/api/v1/robot/anomalies` | Multipart `meta` (JSON with `uid`) and `image.jpg` → 201 `{id, created}`; idempotent on `uid` |
| POST | `/api/v1/robot/routes` | Multipart `meta` (JSON with `uid`) and `route.csv` → 201 `{id}`; idempotent on `uid` |
| GET | `/api/v1/robot/routes/{id}/file` | → the route CSV; 403 for a route of another robot |

## Integration API

Third-party applications use the read-only API under `/api/ext/v1` with OAuth 2.0 client
credentials; clients are managed with the [API client routes](#integration-api-clients) above. The
guide is [INTEGRATION.md](../INTEGRATION.md); a running server serves Swagger UI at `/api/ext/docs`
and the OpenAPI description at `/api/ext/v1/openapi.yaml`.
