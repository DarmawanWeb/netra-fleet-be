# Development

## Prerequisites

| Tool | Version | Needed for |
|---|---|---|
| Go | 1.22 (`go 1.22.2` in [`go.mod`](../go.mod); CI and the Docker build use 1.22) | `netra-api` |
| PostgreSQL | CI uses `timescale/timescaledb:latest-pg16` | `netra-api` (required) |
| TimescaleDB | optional | Hypertables, retention and compression ([database](database.md)) |
| NATS | optional | Domain events |
| Python | 3.11 or later (`ai/pyproject.toml`); CI and the image use 3.12 | `netra-ai`, smoke test |
| Ollama and a vision model | optional | Real final detection |
| golangci-lint | v1 (CI pins v1.61) | Go lint; v2 refuses the v1 configuration |
| ruff, pytest | from the `dev` extra of `ai/` | Python lint and tests |
| `requests`, `websocket-client` | any | Smoke test |
| lefthook | optional | Git hooks |

## Running locally

```bash
# PostgreSQL with TimescaleDB, same image and credentials as CI
docker run -d --name netra-db -p 5432:5432 \
  -e POSTGRES_USER=netra -e POSTGRES_PASSWORD=netra -e POSTGRES_DB=netra \
  timescale/timescaledb:latest-pg16

# API on :8080; migrations run at start
cp .env.example .env && set -a && . ./.env && set +a
make run

# AI service on :8090 (optional)
cd ai && pip install -e ".[dev]" && uvicorn netra_ai.main:app --port 8090
```

- `.env.example` sets `NETRA_ADMIN_PASSWORD=change-me`, so the user `admin` exists after the first
  start; log in with `POST /api/v1/auth/login`.
- It also sets `NETRA_FINAL_DETECTOR_URL=http://localhost:8090/detect` and
  `NETRA_NATS_URL=nats://localhost:4222`. Without `netra-ai` running, each anomaly falls back to the
  robot's result with a warning; without NATS, publishing logs `nats unavailable`. Unset them to work
  without either.
- `netra-ai` needs Ollama with the model pulled (default `qwen2.5vl:7b`). Its default
  `OLLAMA_URL` points at a host named `ollama`; set `OLLAMA_URL=http://localhost:11434` for a local
  Ollama ([AI service](ai-service.md)).
- `make build` writes a static binary to `bin/netra-api`; `make docker` builds the `netra-api` and
  `netra-ai` images.

The `netra-api` image ([`Dockerfile`](../Dockerfile)) builds a static binary (`CGO_ENABLED=0`,
`-trimpath -ldflags="-s -w"`) on `golang:1.22-alpine` and runs it on
`gcr.io/distroless/static-debian12:nonroot` with `NETRA_DATA_DIR=/data` and `NETRA_ADDR=:8080`. The
`/data` volume is created owned by the nonroot user (uid 65532) so it is writable.
[`.dockerignore`](../.dockerignore) leaves out `data`, `*.md`, `ai` and `.github`.

## Tests

```bash
go test -race ./...            # what CI runs; make test runs go test ./... without -race
cd ai && python -m pytest -q   # make ai-test
```

The Go unit tests need no database: handlers are called with `httptest` on a `Server` built without
a store, robots are simulated over a real WebSocket, and the integration API runs on an in-memory
fake store.

| Test | Covers |
|---|---|
| [`internal/api/clock_test.go`](../internal/api/clock_test.go) | Clock-skew correction |
| [`internal/api/command_test.go`](../internal/api/command_test.go) | Command names accepted by `POST /robots/{id}/command` |
| [`internal/api/robotcontrol_test.go`](../internal/api/robotcontrol_test.go) | `set_inspection` and `set_people`: ack, refusal, timeout, validation |
| [`internal/api/route_test.go`](../internal/api/route_test.go) | Route CSV parsing |
| [`internal/auth/auth_test.go`](../internal/auth/auth_test.go) | Password hashes, token digests |
| [`internal/ext/ext_test.go`](../internal/ext/ext_test.go) | Token endpoint, lockout, scopes, rate limit, change feed and cursor, photos, routes, OpenAPI and Swagger UI |
| [`internal/report/pdf_test.go`](../internal/report/pdf_test.go), [`xlsx_test.go`](../internal/report/xlsx_test.go) | PDF and XLSX output |
| [`internal/scheduler/scheduler_test.go`](../internal/scheduler/scheduler_test.go) | `Due` for weekly, one-off and disabled schedules |
| [`ai/tests/test_ai.py`](../ai/tests/test_ai.py) | Answer normalisation, prompt, `/detect` with a mocked Ollama, unreadable images |

SQL queries, migrations and the complete robot flow are only exercised by the smoke test.

## Lint

```bash
make lint   # go vet, golangci-lint run, ruff check and ruff format --check in ai/
```

[`.golangci.yml`](../.golangci.yml) is a golangci-lint v1 configuration: `errcheck`, `gosimple`,
`govet`, `ineffassign`, `staticcheck`, `unused`, `gofmt`, `goimports`, `misspell`, `bodyclose`,
`gosec` (without G104, G304, G306) and `revive` (rule `exported` off); `ai/` is excluded; timeout
5 minutes. CI pins v1.61; use a v1 release locally, v2 refuses this file. Ruff is configured in
[`ai/pyproject.toml`](../ai/pyproject.toml): line length 110, target Python 3.11, rules `E`, `F`,
`I`, `B`, `UP`, `SIM`, `D` without `D100`, `D104`, `D203`, `D213`.

## End-to-end smoke test

[`scripts/smoke_test.py`](../scripts/smoke_test.py) drives a running `netra-api` as an admin, a
simulated robot and an integration client. It prints `ok` or `FAIL` per check and exits with status
1 at the first failure.

```bash
# terminal 1: the API with a known admin password and pass-through detection
NETRA_ADMIN_PASSWORD=admin NETRA_FINAL_DETECTOR_URL= make run

# terminal 2
pip install requests websocket-client
NETRA_PASSWORD=admin make smoke
```

- Leave `NETRA_FINAL_DETECTOR_URL` empty, as CI does: the test expects the robot's categories to
  become final. A real model judges the tiny test photo itself and may dismiss it.
- It relies on the default rules (`trash` confirmed automatically, `floor_damage` needing
  validation) and on `moondream:latest` being in `NETRA_INSPECTION_MODELS`.
- Use a development database. The test creates a robot `smoke-<hex>` and deletes it at the end
  (cascading to its routes, schedule, patrol, anomalies, crowd samples and events) and deletes its
  API client, but keeps the viewer user `viewer<hex>`, the audit entries, telemetry rows and photo
  files.
- It takes about 15 s because it waits for the 5 s telemetry sampling and the 2 s feed delay.

What it checks, in order:

1. Login, robot registration, robot WebSocket (`hello`, telemetry), robot listed online with its
   telemetry.
2. Route upload twice (one route stored), points parsed.
3. Patrol start: `start_patrol` with the patrol ID on the robot socket, route CSV download by the
   robot, ack, `patrol_event` `started` makes the patrol `running`; a stop event and a crowd window.
4. Anomaly uploads: `trash` confirmed automatically, `floor_damage` waiting for validation, operator
   confirmation, photo served.
5. `patrol_event` `finished`, patrol recap (anomalies per category, crowd peak), heatmap cells,
   crowd timeline, schedule saved, 401 without login.
6. Reports: patrol PDF, summary PDF and XLSX, anomaly and patrol XLSX; track history.
7. Robot control: e-stop reaches the robot, `/info`, inspection models, `set_inspection` acked (200),
   `set_people` refused (422), unknown model (400), robot load stored in the telemetry history.
8. Clock skew: telemetry two hours behind, anomaly time moved onto server time, `clock_skew_s` about
   7200.
9. Integration API: client created (secret shown once, never listed), client-credentials token,
   feed read to the end with the cursor, anomaly format and original photo, an operator rejection
   appearing in the feed, `status` and `validated` filters, scope enforcement, secret rotation
   revoking tokens, disabled client refused, OpenAPI and Swagger UI served, client deleted.
10. Roles: a viewer can read but cannot start a patrol or list users; logout; the audit log holds
    the patrol start and the logins.

## Continuous integration

[`.github/workflows/ci.yml`](../.github/workflows/ci.yml) runs on pushes to `main`, on tags `v*` and
on every pull request, with the permissions `contents: read` and `packages: write`.

| Job | Environment | Steps |
|---|---|---|
| `api` | `ubuntu-latest` with a `timescale/timescaledb:latest-pg16` service (user, password and database `netra`, port 5432, `pg_isready` health check) | Checkout; Go 1.22; golangci-lint v1.61 through `golangci/golangci-lint-action@v6`; `go test -race ./...`; smoke test: build `netra-api`, start it in the background with `NETRA_ADMIN_PASSWORD=admin`, the service database and `NETRA_DATA_DIR=/tmp/netra-data`, wait 3 s, install `requests` and `websocket-client`, run `scripts/smoke_test.py` |
| `ai` | `ubuntu-latest`, working directory `ai` | Python 3.12; `pip install -e ".[dev]"`; `ruff check .` and `ruff format --check .`; `python -m pytest -q` |
| `images` | `ubuntu-latest`, push events only, after `api` and `ai` passed | Matrix `netra-api` (context `.`) and `netra-ai` (context `ai`): Buildx, login to `ghcr.io` with `GITHUB_TOKEN`, tags from `docker/metadata-action`, build and push for `linux/amd64` and `linux/arm64`, GitHub Actions cache per image |

Images are pushed as `ghcr.io/<repository owner>/netra-api` and `ghcr.io/<repository owner>/netra-ai`
with the tag rules `type=ref,event=branch` (branch name, e.g. `main`),
`type=semver,pattern={{version}}` (`1.2.3` for the tag `v1.2.3`) and `type=sha,format=short`.
Pull requests run the `api` and `ai` jobs and publish nothing.

## Git hooks

Install once with `lefthook install`; the hooks are defined in [`lefthook.yml`](../lefthook.yml).

| Hook | Command | Check |
|---|---|---|
| `pre-commit` (parallel) | `gofmt` | Staged Go files are gofmt-clean |
| | `vet` | `go vet ./...` when Go files are staged |
| | `golangci` | `golangci-lint run --new-from-rev=HEAD` when Go files are staged |
| | `ruff` | `ruff check` and `ruff format --check` on staged Python files under `ai/` |
| `commit-msg` | `conventional` | The message follows Conventional Commits |
| `pre-push` | `test` | `go test ./...` |

## Commit convention

Conventional Commits with a one-line message:

```text
<type>[(<scope>)][!]: <subject>
```

The `commit-msg` hook accepts the types `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`,
`build`, `ci`, `chore` and `revert`. Scopes used so far: `api`, `auth`, `robots`, `schedules`,
`reports`, `i18n`, `observability`, `ai`, `ext`, `docker`. Examples from the history:

```text
feat(robots): send e-stop and e-stop release from the dashboard
fix(schedules): accept one-off schedules without weekdays
chore: move the Go module to github.com/DarmawanWeb/netra-fleet-be
```

## Adding a dashboard endpoint

1. **Handler.** Write a method `func (s *Server) name(w http.ResponseWriter, r *http.Request)` in
   the file of its area under `internal/api` (`dashboard.go` for fleet data, `users.go`,
   `reports.go`, ...). Use the helpers in [`server.go`](../internal/api/server.go): `decode` (JSON
   body up to 1 MiB), `pathID`, `timeRange`, `userOf`, `writeJSON`, `writeError` and `fail` (maps
   `store.ErrNotFound` to 404, other errors to 500).
2. **Store.** Add the query as a method on `*store.Store` in `internal/store`; return
   `store.ErrNotFound` for missing rows (`notFound` for single-row queries, `execOne` for updates
   and deletes). Schema changes need a [migration](#adding-a-migration).
3. **Route and permission.** Register it in `Server.Handler` with
   `route(<permission>, "METHOD /api/v1/...", s.name)`. Pick the permission from
   [`rbac.go`](../internal/auth/rbac.go): `view` to read, `operate` to run the fleet, `configure` to
   change robots, routes, rules or floor plans, `audit` for the audit log, `users` for accounts and
   API clients.
4. **Audit.** Successful requests other than `GET` and `HEAD` are audited automatically as
   `<METHOD> <path>`. When the details matter, also call `s.audit(r, "area.action", target, detail)`,
   as `replyAck` in [`robotcontrol.go`](../internal/api/robotcontrol.go) and
   [`clients.go`](../internal/api/clients.go) do.
5. **Live updates and events.** Use `s.hub.Broadcast(...)` when dashboards must refresh, and
   `s.bus.Publish("netra....", payload)` for an event other systems may consume.
6. **Tests.** Call the handler with `httptest` on a `Server` without a store, as in
   [`command_test.go`](../internal/api/command_test.go) and
   [`robotcontrol_test.go`](../internal/api/robotcontrol_test.go) (`s.audit` skips the database when
   there is no store). Cover database behaviour in `scripts/smoke_test.py`.
7. **Docs.** Add the route to [api.md](api.md) and to the API table in the repository README.

An integration API endpoint is registered in `ext.API.Register` with its scope, described in
[`openapi.yaml`](../internal/ext/docs/openapi.yaml) and [INTEGRATION.md](../INTEGRATION.md), and
added to the path list of `TestDocs` in [`ext_test.go`](../internal/ext/ext_test.go).

## Adding a migration

1. Add `internal/store/migrations/NNN_short_name.sql` with the next three-digit number; files run
   in lexical order.
2. Write it against a database that has every earlier migration. Prefer `IF NOT EXISTS` and
   `ON CONFLICT`, keep it valid on plain PostgreSQL, and guard TimescaleDB calls the way 002 does
   (check `pg_available_extensions`).
3. The file runs once, in one transaction, at the next start of `netra-api`, so statements that
   cannot run in a transaction (`CREATE INDEX CONCURRENTLY`, for example) do not work.
4. Never edit or rename a migration that has been applied anywhere; fix forward with a new file.
5. Update the models and queries in `internal/store` and [database.md](database.md).
6. Start `netra-api` against an empty database and against one with the previous schema, then run
   the smoke test.

## Releases

There is no release tooling besides CI. Pushing a tag `v<major>.<minor>.<patch>` runs CI and
publishes both images; the configured tag rules give them the version (`1.2.3`) and the short
commit SHA. Every push to `main` publishes images tagged `main` and the short SHA. A `v*` tag that
is not a semantic version gets no version tag. Version numbers in the code: `netra-ai` 0.1.0
([`ai/pyproject.toml`](../ai/pyproject.toml)) and the integration API 1.0.0
([`openapi.yaml`](../internal/ext/docs/openapi.yaml)); `netra-api` embeds no version.
