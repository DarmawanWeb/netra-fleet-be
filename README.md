<div align="center">

<img src="docs/logo.svg" width="96" alt="NETRA logo">

# NETRA Fleet backend

**`netra-api` (Go) and `netra-ai` (FastAPI) of the NETRA patrol command center: the REST and
WebSocket API, the robot gateway, the anomaly pipeline with AI final detection, the scheduler,
reports, and the integration API for third-party applications.**

[![CI](https://github.com/DarmawanWeb/netra-fleet-be/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/DarmawanWeb/netra-fleet-be/actions/workflows/ci.yml)
![Go](https://img.shields.io/badge/Go-1.22-00ADD8?logo=go&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL%2016-TimescaleDB-4169E1?logo=postgresql&logoColor=white)
![Python](https://img.shields.io/badge/Python-3.12-3776AB?logo=python&logoColor=white)
![FastAPI](https://img.shields.io/badge/FastAPI-netra--ai-009688?logo=fastapi&logoColor=white)
![Ollama](https://img.shields.io/badge/AI-Ollama%20VLM-000000?logo=ollama&logoColor=white)
![NATS](https://img.shields.io/badge/events-NATS-27AAE1?logo=natsdotio&logoColor=white)
![OpenAPI](https://img.shields.io/badge/integration-OpenAPI%203-6BA539?logo=openapiinitiative&logoColor=white)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue)](LICENSE)

[Features](#features) · [Services](#services) · [API](#api) · [Documentation](#documentation) ·
[Develop](#develop)

</div>

---

Part of [NETRA](https://github.com/DarmawanWeb/Netra), which deploys it with the
[dashboard](https://github.com/DarmawanWeb/netra-fleet-fe). Robots run
[Pashupati](https://github.com/DarmawanRobotics/Pashupati) and talk to the robot endpoints described
in its [robot API](https://github.com/DarmawanRobotics/Pashupati/blob/main/src/robot_fleet/API.md).

## Features

**Robots**
- A token per robot, live state over WebSocket, telemetry history (5 s samples) for track replay
  and battery, load and people charts.
- Commands with the robot's acknowledgement: pause, resume, localize, stop patrol, e-stop and its
  release; AI inspection (on or off, model, pace) and people detection, waiting up to 10 s.
- Clock correction: a robot without NTP keeps a wrong clock, so its offset is measured from every
  telemetry message and anomaly, crowd and route times are moved onto server time when it is above
  2 s (`clock_skew_s` in `GET /robots`).

**Patrols**
- Start now, weekly or one-off schedules; status from the robot, laps, stop events, recap.

**Anomalies**
- Robot early detection, then a queue, then `netra-ai` final detection, then a per-category rule:
  confirm at once and send to a Telegram group, or wait for operator validation.

**Crowd and reports**
- Per-minute people cells become heatmaps and hourly and daily peaks.
- Patrol report PDF (summary, anomaly table and photos, timeline), period summary PDF or Excel,
  anomaly and patrol Excel exports, in English or Indonesian.

**Integration API**
- Read-only `/api/ext/v1` for third-party applications with OAuth2 client credentials (clients and
  scopes managed by admins): an anomaly change feed with cursor and filters, original photos,
  robots, patrols, routes and floor plans. OpenAPI at `/api/ext/v1/openapi.yaml`, Swagger UI at
  `/api/ext/docs`, guide in [INTEGRATION.md](INTEGRATION.md).

**Access and audit**
- Roles `viewer`, `operator`, `supervisor`, `admin` over the permissions `view`, `operate`,
  `configure`, `audit`, `users`; PBKDF2 passwords, session tokens, logout, login throttling
  (8 failures in 10 min per IP); the last admin cannot be removed.
- Audit log of every login, failed login and change (method, path, user, IP).

**Observability and storage**
- Prometheus `/metrics` (HTTP, robot messages, anomalies, battery, online, integration requests);
  domain events on NATS (`netra.robot.<id>.<type>`, `netra.anomaly.<status>`).
- With TimescaleDB, `telemetry` and `audit_log` become hypertables; telemetry is compressed after
  7 days and kept 180 days. Plain PostgreSQL works too.

## Services

| Service | Source | Stack | |
|---|---|---|---|
| `netra-api` | `cmd/`, `internal/` | Go 1.22, PostgreSQL / TimescaleDB, NATS | REST and WebSocket API, robot gateway, scheduler, anomaly pipeline, reports, integration API |
| `netra-ai` | `ai/` | Python 3.12, FastAPI, Ollama | Final anomaly detection with a larger vision-language model |

## API

Dashboard side, with a Bearer session token (the full reference is [docs/api.md](docs/api.md)):

| Method | Path | Permission |
|---|---|---|
| POST | `/api/v1/auth/login`, `/api/v1/auth/logout` | — / any |
| GET, POST | `/api/v1/me`, `/api/v1/me/password` | any |
| GET | `/api/v1/info` (`public_url` robots connect to, `time_zone`) | view |
| GET | `/api/v1/robots`, `/api/v1/robots/{id}/track` | view |
| POST, PUT, DELETE | `/api/v1/robots[/{id}]`, `/api/v1/robots/{id}/token` | configure |
| POST | `/api/v1/robots/{id}/command` (`pause`, `resume`, `localize`, `stop_patrol`, `estop`, `estop_release`) | operate |
| POST | `/api/v1/robots/{id}/inspection` `{enabled?, model?, interval?}`, `/api/v1/robots/{id}/people` `{enabled}` (wait up to 10 s for the robot's ack) | operate |
| GET | `/api/v1/inspection/models` (`NETRA_INSPECTION_MODELS`, comma-separated) | view |
| GET, POST, PUT, DELETE | `/api/v1/api-clients[/{id}]`, `POST /api/v1/api-clients/{id}/secret` (integration clients) | users |
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
| GET | `/api/v1/ws` (live feed) | view |

Time ranges use `from` and `to` (RFC 3339, default the last 7 days). Reports take `lang=en|id`
(default `en`); Telegram captions use `NETRA_LANG`. Third-party applications use `/api/ext/v1`
instead ([INTEGRATION.md](INTEGRATION.md)).

## Documentation

| Document | Contents |
|---|---|
| [Architecture](docs/architecture.md) | Components, package map, request and robot lifecycles, anomaly pipeline, patrols and scheduler, crowd, reports, metrics |
| [Dashboard API](docs/api.md) | Every `/api/v1` route with its permission, authentication, errors, the live WebSocket, robot endpoints |
| [Database](docs/database.md) | How migrations are applied, each migration in order, the tables |
| [Configuration](docs/configuration.md) | Environment variables of `netra-api`, `netra-ai` and the smoke test |
| [Development](docs/development.md) | Local setup, tests, lint, smoke test, CI, hooks, adding endpoints and migrations, releases |
| [AI service](docs/ai-service.md) | `netra-ai`: endpoints, prompt and answer handling, metrics, tests, image |
| [Integration guide](INTEGRATION.md) | The read-only API for third-party applications, with a Python poller |

## Develop

```bash
cp .env.example .env && set -a && . ./.env && set +a
make run                 # API on :8080 (PostgreSQL required; migrations run at start)
cd ai && pip install -e ".[dev]" && uvicorn netra_ai.main:app --port 8090
make test ai-test lint   # unit tests, ruff, golangci-lint
make smoke               # end-to-end against a running API
lefthook install         # git hooks: gofmt, vet, golangci-lint, ruff, conventional commits
```

CI ([ci.yml](.github/workflows/ci.yml)) lints and tests both services, runs the API against
TimescaleDB including the end-to-end smoke test, and pushes multi-arch images
`ghcr.io/<owner>/netra-api` and `ghcr.io/<owner>/netra-ai` on `main` and `v*` tags.

Released under the [Apache License 2.0](LICENSE).
