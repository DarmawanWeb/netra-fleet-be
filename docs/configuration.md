# Configuration

Both services are configured through environment variables only. The Netra deployment
([DarmawanWeb/Netra](https://github.com/DarmawanWeb/Netra)) sets the variables marked in the
**Compose** column in its docker compose file; the others keep the defaults below unless the
deployment adds them. [`.env.example`](../.env.example) holds values for local development.

## netra-api

All variables are read once at start-up by [`config.go`](../internal/config/config.go); no other Go
code reads the environment. An empty value counts as unset, so the default applies.

| Variable | Default | Meaning | Compose |
|---|---|---|---|
| `NETRA_ADDR` | `:8080` | Listen address (the Docker image sets `:8080`) | |
| `NETRA_DATABASE_URL` | `postgres://netra:netra@localhost:5432/netra?sslmode=disable` | PostgreSQL connection string (`lib/pq`) | yes |
| `NETRA_DATA_DIR` | `./data` (the Docker image sets `/data`) | Directory for anomaly photos and floor-plan files; the paths are stored in the database ([database](database.md#files-outside-the-database)) | yes |
| `NETRA_PUBLIC_URL` | empty | Public base URL: returned as `public_url` by `GET /api/v1/info` (the address robots connect to) and used for the `<url>/anomalies/<id>` link in Telegram captions | yes |
| `NETRA_ALLOWED_ORIGIN` | `*` | Value of `Access-Control-Allow-Origin` | yes |
| `NETRA_NATS_URL` | empty | `nats://[user:pass@]host:port` for domain events; empty disables publishing | yes |
| `NETRA_FINAL_DETECTOR_URL` | empty | Full URL of the `netra-ai` detect endpoint (`.env.example`: `http://localhost:8090/detect`); empty makes the robot's own result final | yes |
| `NETRA_TZ` | `Asia/Jakarta` | IANA time zone for schedules, crowd buckets, times in reports and Telegram captions, `timezone` in `/me` and `time_zone` in `/info`; an unknown zone falls back to UTC. The zone database is compiled in (`time/tzdata`) | |
| `NETRA_ADMIN_USER` | `admin` | Username of the bootstrap admin | |
| `NETRA_ADMIN_PASSWORD` | empty | When set, every start creates or updates `NETRA_ADMIN_USER` with this password and the `admin` role, so a password changed in the dashboard is reset at the next start (the `disabled` flag is left alone). Empty: no bootstrap | |
| `NETRA_SESSION_HOURS` | `72` | Session lifetime in hours. A value that is not an integer gives 0, and every new session expires at once | |
| `NETRA_TELEGRAM_TOKEN` | empty | Telegram bot token; empty disables Telegram alerts | |
| `NETRA_TELEGRAM_CHAT_ID` | empty | Chat for rules that have no `telegram_chat_id` of their own | |
| `NETRA_TELEGRAM_API` | `https://api.telegram.org` | Bot API base URL (`<base>/bot<token>/sendPhoto`) | |
| `NETRA_LANG` | `en` | Language of Telegram captions: `id` for Indonesian, anything else English. Reports take `?lang=` instead | |
| `NETRA_INSPECTION_MODELS` | `qwen3-vl:2b-instruct,moondream:latest,ahmadwaqar/smolvlm2-2.2b-instruct:latest` | Comma-separated vision models offered for on-robot inspection; `POST /api/v1/robots/{id}/inspection` accepts only these | |
| `NETRA_EXT_TOKEN_MINUTES` | `60` | Lifetime of integration API access tokens in minutes; a value that is not an integer or lies outside 5 to 1440 gives 60 | |

`NETRA_PUBLIC_URL` serves both the robots (API address) and the Telegram links (dashboard page
`/anomalies/<id>`), so it assumes the dashboard and the API are reachable under the same origin.

## netra-ai

Read at import time by [`main.py`](../ai/netra_ai/main.py). Unlike `netra-api`, an empty value is
used as given, and a value that is not a number makes the numeric settings fail at start.

| Variable | Default | Meaning | Compose |
|---|---|---|---|
| `OLLAMA_URL` | `http://ollama:11434` | Ollama base URL; a trailing `/` is removed | yes |
| `NETRA_AI_MODEL` | `qwen2.5vl:7b` | Ollama vision model | yes |
| `NETRA_AI_LANGUAGE` | `English` | Language the model writes the `description` in | yes |
| `NETRA_AI_CATEGORIES` | `trash,spill,floor_damage,fallen_person` | Comma-separated categories offered to the model; must match the NETRA rule codes | |
| `NETRA_AI_MAX_SIDE` | `1024` | Longest image side in pixels after downscaling | |
| `NETRA_AI_TIMEOUT` | `90` | Timeout of the HTTP client towards Ollama, seconds | |
| `NETRA_AI_KEEP_ALIVE` | `1h` | Ollama `keep_alive`: how long the model stays loaded | |
| `NETRA_AI_CONCURRENCY` | `1` | Model calls in flight at once | |

`NETRA_AI_TIMEOUT` and `NETRA_AI_KEEP_ALIVE` are not listed in [`ai/README.md`](../ai/README.md).
`netra-api` waits up to 120 s for an answer, longer than the default model timeout.

## Smoke test

Read by [`scripts/smoke_test.py`](../scripts/smoke_test.py) ([development](development.md#end-to-end-smoke-test)):

| Variable | Default | Meaning |
|---|---|---|
| `NETRA_URL` | `http://localhost:8080` | Base URL of the running `netra-api` |
| `NETRA_USER` | `admin` | Admin username |
| `NETRA_PASSWORD` | `admin` | Admin password |
