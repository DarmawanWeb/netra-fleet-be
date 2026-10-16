# NETRA Integration API

Read-only API for third-party applications: the anomalies NETRA robots detect, with their original
photos (for example to run your own AI on them), the robots, patrols, routes and floor plans.

| | |
|---|---|
| Base URL | `https://<netra-host>/api/ext/v1` |
| Interactive docs (Swagger UI) | `https://<netra-host>/api/ext/docs` |
| OpenAPI 3 description | `https://<netra-host>/api/ext/v1/openapi.yaml` (import into Postman, Insomnia, code generators) |
| Authentication | OAuth 2.0 client credentials, then `Authorization: Bearer <token>` |
| Format | JSON (UTF-8), times RFC 3339 UTC, photos JPEG |

## 1. Credentials

The NETRA administrator creates a client in **Settings → API clients** and gives you:

* `client_id`, for example `nc_3f9d0c1a2b7e4f60`
* `client_secret`, starting with `ncs_`, shown only once. Keep it on your server, never in a browser
  or mobile app. If it leaks, ask for a new one; the old secret and its tokens stop working at once.
* the scopes you may use:

| Scope | Endpoints |
|---|---|
| `anomalies:read` | `GET /anomalies`, `GET /anomalies/{id}`, `GET /anomalies/{id}/image`, `GET /categories` |
| `robots:read` | `GET /robots` |
| `patrols:read` | `GET /patrols`, `GET /patrols/{id}`, `GET /routes`, `GET /routes/{id}` |
| `maps:read` | `GET /floor-plans`, `GET /floor-plans/{id}/file` |

## 2. Access token

```bash
curl -s -u "$CLIENT_ID:$CLIENT_SECRET" \
  -d grant_type=client_credentials \
  https://netra.example.com/api/ext/v1/oauth/token
```

```json
{"access_token": "nat_0QzR3m9n…", "token_type": "Bearer", "expires_in": 3600, "scope": "anomalies:read robots:read"}
```

The credentials may also be sent as `client_id` / `client_secret` form fields (or JSON). Add
`scope=anomalies:read` to get fewer scopes than the client has. Reuse the token until it expires; on
`401 invalid_token` get a new one.

## 3. Anomalies: follow the change feed

`GET /anomalies` lists anomalies **in the order they last changed**, oldest change first. A new
detection, and every later change of it (final AI detection done, operator confirmed or rejected),
puts the anomaly at the end of the feed. Read it with the cursor:

1. First call without `cursor` (or with `updated_since=<time>` to skip history).
2. Process `data`, store `next_cursor`.
3. While `has_more` is `true`, call again at once with `cursor=<next_cursor>`.
4. When `has_more` is `false` you are up to date: wait 10 to 60 s, then call again with the stored
   `next_cursor`. Changes appear in the feed about 2 s after they happen.

Because the same anomaly comes again when it changes, key your records by `id` and update them.
Keep the same filters while you follow one cursor.

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "https://netra.example.com/api/ext/v1/anomalies?limit=100&final=true"
```

```json
{
  "data": [
    {
      "id": 1287,
      "uid": "6f1c2b0e-3a4d-4c1e-9b8a-2d5f7e9c1a3b",
      "status": "needs_validation",
      "category": "trash",
      "detected_at": "2026-10-16T01:20:11.402113Z",
      "updated_at": "2026-10-16T01:20:39.118245Z",
      "robot": {"id": "l1w-01", "name": "L1W 01"},
      "patrol_id": 42,
      "route_id": 5,
      "stop_index": 12,
      "position": {"x": 41.2, "y": -6.8, "yaw": 0.12, "frame": "map"},
      "early_detection": {"category": "trash", "confidence": 0.74, "description": "a plastic bottle on the floor"},
      "final_detection": {"category": "trash", "confidence": 0.91, "description": "plastic bottle and paper cup"},
      "validation": null,
      "image_url": "https://netra.example.com/api/ext/v1/anomalies/1287/image"
    }
  ],
  "next_cursor": "MTc5MjExMzIzOTExODI0NToxMjg3",
  "has_more": false
}
```

### Fields

| Field | |
|---|---|
| `status` | `queued` → `analyzing` → `needs_validation`, `confirmed` or `dismissed`; an operator turns `needs_validation` into `confirmed` or `rejected` |
| `category` | current category: the final detection (or the operator's correction) when present, else the robot's |
| `early_detection` | the robot's own detection |
| `final_detection` | the server's final AI detection, `null` while `queued` / `analyzing` |
| `validation` | `{"result": "confirmed" \| "rejected", "validated_at": …}` when an operator decided, else `null` |
| `position` | robot map frame, metres (`x` forward, `y` left), `yaw` in radians; `null` if unknown |
| `image_url` | the original JPEG from the robot camera; needs the bearer token |

### Filters

| Parameter | |
|---|---|
| `status=confirmed,rejected` | only these statuses |
| `final=true` / `false` | final AI detection done / not yet |
| `validated=true` / `false` | decided by an operator / not decided |
| `category=trash,spill` | current category (see `GET /categories`) |
| `robot_id=l1w-01` | one robot |
| `detected_from`, `detected_to` | detection time window (RFC 3339) |
| `limit` | 1 to 200, default 50 |

Typical choices: everything as early as possible → no filter; only after the server's AI →
`final=true`; real anomalies only → `status=confirmed`; operator decisions → `validated=true`.

## 4. Photos

```bash
curl -s -H "Authorization: Bearer $TOKEN" -o 1287.jpg \
  https://netra.example.com/api/ext/v1/anomalies/1287/image
```

The JPEG is exactly what the robot uploaded (typically 1280 px wide). It never changes, so download
it once per anomaly.

## 5. Other endpoints

* `GET /robots`: robots, `online`, `last_seen`, last state (`nav_state`, `battery_pct`, `people`,
  `estop`, `pose`, current `patrol`).
* `GET /patrols?from=&to=&robot_id=` (7 days by default, at most 93), `GET /patrols/{id}`.
* `GET /routes?robot_id=&archived=`, `GET /routes/{id}` with `points` (`x`, `y`, `yaw_deg`, `dwell_s`).
* `GET /floor-plans` with calibration, `GET /floor-plans/{id}/file`. A map position becomes a plan
  pixel with `px = origin_x_px + s·(cos θ·x + sin θ·y)`, `py = origin_y_px + s·(sin θ·x − cos θ·y)`,
  `s = px_per_m`, `θ = rotation_rad`.
* `GET /categories`: categories and labels.

Lists without a cursor answer `{"data": [...]}`. New fields may be added to any object; ignore the
ones you do not know.

## 6. Errors and limits

Errors are `{"error": "<code>", "error_description": "<text>"}`:

| HTTP | `error` | What to do |
|---|---|---|
| 400 | `invalid_request`, `unsupported_grant_type`, `invalid_scope` | fix the request |
| 401 | `invalid_client` | wrong `client_id` / `client_secret`, or the client was disabled |
| 401 | `invalid_token` | get a new token |
| 403 | `insufficient_scope` | ask the administrator for the scope |
| 404 | `not_found` | |
| 429 | `rate_limited` | wait `Retry-After` seconds |
| 500 | `server_error` | retry later |

Limits: 10 requests per second per client (bursts up to 30). Ten failed token requests from one
address block it for 10 minutes.

## 7. Example: Python poller

```python
import time, requests

BASE = "https://netra.example.com/api/ext/v1"
CLIENT_ID, CLIENT_SECRET = "nc_...", "ncs_..."

def new_token():
    r = requests.post(f"{BASE}/oauth/token", data={"grant_type": "client_credentials"},
                      auth=(CLIENT_ID, CLIENT_SECRET), timeout=10)
    r.raise_for_status()
    return r.json()["access_token"]

token = new_token()
cursor = None   # persist this between runs (file, database)
photos = set()  # ids whose photo you already have; it never changes

def get(path, **params):
    global token
    for _ in range(2):
        r = requests.get(f"{BASE}{path}", params=params, timeout=30,
                         headers={"Authorization": f"Bearer {token}"})
        if r.status_code == 401:
            token = new_token()
            continue
        if r.status_code == 429:
            time.sleep(int(r.headers.get("Retry-After", "1")))
            continue
        r.raise_for_status()
        return r
    r.raise_for_status()

while True:
    page = get("/anomalies", limit=100, final="true", **({"cursor": cursor} if cursor else {})).json()
    for a in page["data"]:
        if a["id"] not in photos:
            photo = get(f"/anomalies/{a['id']}/image").content  # JPEG bytes
            photos.add(a["id"])
            # run your own detection on `photo`, store the result keyed by a["id"]
            print("new", a["id"], a["category"], len(photo), "bytes")
        else:
            print("changed", a["id"], a["status"])  # e.g. confirmed or rejected by an operator
    cursor = page["next_cursor"]
    if not page["has_more"]:
        time.sleep(15)
```
