# NETRA Fleet backend documentation

Developer documentation for `netra-api` (Go) and `netra-ai` (FastAPI), the backend of NETRA Fleet,
the patrol command center for Pashupati robots.

| Document | Contents |
|---|---|
| [Architecture](architecture.md) | Components, package map, request and robot connection lifecycles, anomaly pipeline, patrols and scheduler, crowd, reports, metrics |
| [Dashboard API](api.md) | Every `/api/v1` route with its permission, authentication, errors, the live WebSocket feed, robot endpoints |
| [Database](database.md) | How migrations are applied, each migration in order, table overview, files outside the database |
| [Configuration](configuration.md) | Environment variables of `netra-api`, `netra-ai` and the smoke test |
| [Development](development.md) | Local setup, tests, lint, smoke test, CI, git hooks, commit convention, adding endpoints and migrations, releases |
| [AI service](ai-service.md) | The `netra-ai` final-detection service: endpoints, prompt and answer handling, metrics, tests, image |

Elsewhere:

- [Repository README](../README.md): feature overview and quick start.
- [Integration API guide](../INTEGRATION.md): the read-only `/api/ext/v1` API for third-party applications.
- [Robot protocol](https://github.com/DarmawanRobotics/Pashupati/blob/main/src/robot_fleet/API.md)
  (Pashupati repository): what robots send to and receive from `netra-api`.
