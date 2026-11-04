# NETRA Fleet backend documentation

Developer documentation for `netra-api` (Go) and `netra-ai` (FastAPI), the backend of NETRA Fleet,
the patrol command center for Pashupati robots.

| Document | Contents |
|---|---|
| [Architecture](architecture.md) | Components, process roles and split installations (relay, NATS subjects, deployment example), package map, request and robot connection lifecycles, anomaly pipeline, patrols and scheduler, crowd, reports, metrics |
| [Dashboard API](api.md) | Every `/api/v1` route with its permission, authentication, errors, the live WebSocket feed, robot endpoints |
| [Database](database.md) | How migrations are applied, each migration in order, table overview, files outside the database |
| [Licensing](licensing.md) | Editions, tiers and feature modules, the signed license, the signing key, issuing and installing licenses, expiry and the robot limit |
| [Substation inspection](substation.md) | The module `substation`: assets, inspection points on routes, thermal ratings after NETA MTS Table 100.18, partial discharge levels, findings, the robot protocol for plans and inspections |
| [License manager](license-manager.md) | EZRA's web tool to issue licenses (vendor only): running it, the first start, the encrypted signing key, the activation flow, backups, its API |
| [Configuration](configuration.md) | Environment variables of `netra-api` (with `NETRA_ROLES`), `netra-ai` and the smoke test |
| [Development](development.md) | Local setup, tests, lint, smoke test, CI, git hooks, commit convention, adding endpoints and migrations, releases |
| [AI service](ai-service.md) | The `netra-ai` final-detection service: endpoints, prompt and answer handling, metrics, tests, image |

Elsewhere:

- [Repository README](../README.md): feature overview and quick start.
- [Integration API guide](../INTEGRATION.md): the read-only `/api/ext/v1` API for third-party applications.
- [Robot protocol](https://github.com/DarmawanRobotics/Pashupati/blob/main/src/robot_fleet/API.md)
  (Pashupati repository): what robots send to and receive from `netra-api`.
