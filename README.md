# netra-fleet-be

Backend of **NETRA Fleet**, the patrol command center for Pashupati robots.

| Service | Stack | |
|---|---|---|
| `netra-api` (`cmd/`, `internal/`) | Go 1.22, PostgreSQL / TimescaleDB | REST + WebSocket API, robot gateway, scheduler, anomaly pipeline, reports |
| `netra-ai` (`ai/`) | Python, FastAPI, Ollama | final anomaly detection with a larger vision-language model |

## Features

- **Robots** — token per robot, live state over WebSocket, commands (pause, resume, localize,
  stop patrol, e-stop and e-stop release), telemetry history (5 s samples) for track playback and
  battery charts.
- **Patrols** — start now, weekly or one-off schedules, status from the robot, laps, events, recap.
- **Anomalies** — robot early detection → queue → `netra-ai` final detection → per-category rule
  (auto-confirm + Telegram, or operator validation).
- **Crowd** — per-minute people cells → heatmap and hourly/daily peaks.
- **Reports** — patrol report PDF (summary, anomaly table and photos, timeline), period summary
  PDF/XLSX, anomaly and patrol XLSX exports.
- **Access control** — users with roles `viewer`, `operator`, `supervisor`, `admin` (permissions
  `view`, `operate`, `configure`, `audit`, `users`), PBKDF2 passwords, session tokens, logout,
  login throttling (8 failures / 10 min per IP), the last admin cannot be removed.
- **Audit log** — every login, failed login and change (method, path, user, IP).
- **Observability** — Prometheus `/metrics` (HTTP, robot messages, anomalies, battery, online),
  domain events on NATS (`netra.robot.<id>.<type>`, `netra.anomaly.<status>`).
- **TimescaleDB** — `telemetry` and `audit_log` become hypertables with compression and 180-day
  retention when the extension is available; plain PostgreSQL works too.

## API

Robot side: `Pashupati/src/robot_fleet/API.md`. Dashboard side (Bearer session token):

| Method | Path | Permission |
|---|---|---|
| POST | `/api/v1/auth/login`, `/api/v1/auth/logout` | — / any |
| GET, POST | `/api/v1/me`, `/api/v1/me/password` | any |
| GET | `/api/v1/robots`, `/api/v1/robots/{id}/track` | view |
| POST, PUT, DELETE | `/api/v1/robots[/{id}]`, `/api/v1/robots/{id}/token` | configure |
| POST | `/api/v1/robots/{id}/command` (`pause`, `resume`, `localize`, `stop_patrol`, `estop`, `estop_release`) | operate |
| GET / PUT | `/api/v1/routes[/{id}][/file]` | view / configure |
| GET / POST, PUT, DELETE | `/api/v1/schedules[/{id}]` (`days` or `once_on`) | view / operate |
| GET / POST | `/api/v1/patrols[/{id}]`, `/api/v1/patrols/{id}/stop` | view / operate |
| GET / POST | `/api/v1/anomalies[/{id}][/image]`, `/api/v1/anomalies/{id}/validate` | view / operate |
| GET / PUT, DELETE | `/api/v1/rules[/{category}]` | view / configure |
| GET | `/api/v1/crowd/heatmap`, `/api/v1/crowd/timeline` | view |
| GET / POST, PUT, DELETE | `/api/v1/floorplans[/{id}][/file]` | view / configure |
| GET | `/api/v1/reports/patrols/{id}/pdf`, `/api/v1/reports/summary?format=pdf\|xlsx`, `/api/v1/reports/anomalies`, `/api/v1/reports/patrols` | view |
| GET, POST, PUT, DELETE | `/api/v1/users[/{id}]`, `/api/v1/users/{id}/password` | users |
| GET | `/api/v1/audit` | audit |
| GET | `/api/v1/ws` | view |

Time ranges use `from`/`to` (RFC 3339, default last 7 days). Reports take `lang=en|id` (default
`en`); Telegram captions use `NETRA_LANG`.

## Develop

```bash
cp .env.example .env && set -a && . ./.env && set +a
make run                 # API on :8080 (PostgreSQL required; migrations run at start)
cd ai && pip install -e ".[dev]" && uvicorn netra_ai.main:app --port 8090
make test ai-test lint   # unit tests, ruff, golangci-lint
make smoke               # end-to-end against a running API
lefthook install         # git hooks: gofmt, vet, golangci-lint, ruff, conventional commits
```

CI (`.github/workflows/ci.yml`) lints and tests both services (API against TimescaleDB, including
the end-to-end smoke test) and pushes multi-arch images `ghcr.io/<owner>/netra-api` and
`ghcr.io/<owner>/netra-ai` on `main` and `v*` tags.
