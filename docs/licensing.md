# Editions, tiers and licenses

NETRA is one codebase and one image for every client. What a deployment may use is decided at run
time by its **license**: a signed file that names the client, the edition, the tier, the feature
modules, the number of robots, an expiry date and the client's branding. A mall and a substation
run the same `netra-api` and dashboard; they differ only in their license.

```text
core (always)          robots, live map, commands, routes, patrols, schedules, floor plans,
                       users, patrol PDF reports
+ edition (product)    mall: anomaly, crowd        substation: substation
+ tier (premium)       pro: reports.export, alerts.telegram, audit
                       enterprise: pro + ai.final, integration, multisite
+/- per client         -add / -remove when the license is issued
```

## Feature modules

| Module | What it unlocks | Without it |
|---|---|---|
| `anomaly` | `/anomalies`, `/rules`, `/inspection/models`, `POST /robots/{id}/inspection`; the anomaly pipeline | routes 403; robot uploads accepted and dropped |
| `crowd` | `/crowd/heatmap`, `/crowd/timeline`, `POST /robots/{id}/people` | routes 403; robot `crowd` messages dropped |
| `substation` | substation inspection: assets, inspection points, thermal ΔT (NETA), partial discharge; its routes come with the module | 403 |
| `reports.export` | `/reports/summary` (PDF, XLSX), `/reports/anomalies`, `/reports/patrols` | 403; the patrol PDF stays in the core |
| `alerts.telegram` | Telegram messages for confirmed anomalies | nothing is sent |
| `audit` | `GET /audit`, the audit log page | 403; actions are still recorded |
| `ai.final` | final detection by `netra-ai` (`NETRA_FINAL_DETECTOR_URL`) | the robot's result is final |
| `integration` | `/api/ext/v1` and `/api/ext/docs`, `/api-clients` | 403 |
| `multisite` | on-premise sites synchronised to a central NETRA (planned) | not available |

The backend decides: a module that is not licensed answers
`403 {"error": "feature not licensed", "feature": "<module>"}`, whatever the dashboard shows.
The dashboard reads the modules from `GET /api/v1/info` and hides the pages and menu entries of
the others. Robot endpoints of a module that is not licensed accept the data and drop it, so the
robot does not keep it in its spool and retry forever.

`netra-license presets` prints the same lists from the code
([`license.go`](../internal/license/license.go)).

## No license, expiry and the robot limit

| State | Modules | Robots |
|---|---|---|
| no license, or none valid (`missing`, `invalid`) | none: core only | 1 (enough to evaluate) |
| `valid` | the license's | `max_robots` (0: no limit) |
| `grace`: expired less than 14 days ago | the license's | `max_robots` |
| `expired`: more than 14 days ago | none: core only | `max_robots`, so the fleet keeps working |

The robot limit applies when a robot is registered (`POST /api/v1/robots` answers
`403 {"error": "robot limit of the license reached", "robot_limit": N}`); robots already
registered keep working. The core never stops: an expired license never stops a patrol.

Branding stays after expiry: the product keeps its name and colours.

## The license file

A license is one line of text:

```text
NETRA-LIC-1.<payload>.<signature>
```

- `payload`: the JSON below in base64url.
- `signature`: an Ed25519 signature of the payload text, in base64url.

```json
{
  "product": "netra",
  "id": "20261104-5adae5bc36f0",
  "client": "PLN UPT Bekasi",
  "edition": "substation",
  "tier": "pro",
  "features": ["alerts.telegram", "audit", "reports.export", "substation"],
  "map": "gps",
  "max_robots": 3,
  "installation": "NETRA-7KQ3-M9XD-2WPA",
  "issued_at": "2026-11-04T03:00:08Z",
  "expires_at": "2027-11-04T03:00:08Z",
  "branding": {
    "name": "NETRA Gardu",
    "color": "#0f766e",
    "color_dark": "#0f766e",
    "logo": "data:image/svg+xml;base64,..."
  }
}
```

`netra-api` verifies the signature offline against the public keys compiled into it
([`internal/license/keys/`](../internal/license/keys/README.md)), so a server without internet
checks it the same way and a license cannot be made valid by changing the server's
configuration. Changing one character of the payload breaks the signature. `features` is what
counts; `edition` and `tier` are labels for the dashboard. A license whose `product` is not
`netra` is refused; licenses from before the field have none and are NETRA's.

## Map mode

`map` fixes the kind of map the deployment draws its robots on; the dashboard cannot change it.

| `map` | For | Base layer |
|---|---|---|
| `floorplan` | indoor sites (malls, buildings) | a floor plan (SVG, PNG, JPG, PDF) calibrated onto the robot map |
| `pgm` | sites mapped by the robot | the robot's occupancy grid (`map.yaml` + `map.pgm`), aligned by its origin and resolution |
| `gps` | outdoor sites (substations, yards) | a basemap in latitude and longitude; robots from their GPS or from the robot map through reference points |

Without `map` the edition decides: `mall` floorplan, `substation` gps, `custom` floorplan. The map
mode stays after the license expires, so the maps already drawn keep working.

## Installation ID and activation

Every server makes an installation ID at its first start, such as `NETRA-7KQ3-M9XD-2WPA`, kept in
the database (`settings`). The setup wizard and Settings > License show it. A license with
`installation` works only on that server; a license without it works anywhere.

1. The client installs NETRA and reads the installation ID in the setup wizard.
2. The client sends it to EZRA Robotics.
3. EZRA issues the license for that installation (in the [license manager](license-manager.md),
   or `netra-license issue -installation <ID>`) and sends the file.
4. The client installs it in the wizard or in Settings > License. No internet is needed.

The ID is in the database, so a restored backup keeps it; a fresh database is a new installation
and needs a new license. Letters that look alike are accepted for each other (`O` for `0`, `I`
and `L` for `1`), and case does not matter.

## Signing key

Once, on the machine that issues licenses (not a server):

```bash
go run ./cmd/netra-license keygen -out ~/netra-keys/ezra
# private key: ~/netra-keys/ezra.key (keep it offline, with a backup; never commit it)
# public key:  ~/netra-keys/ezra.pub
cp ~/netra-keys/ezra.pub internal/license/keys/ezra.pub
git add internal/license/keys/ezra.pub   # only the .pub
```

Whoever has the private key can issue any license. `.gitignore` refuses `*.key` and `*.lic`.
Several `.pub` files can be trusted at once, which is how a key is replaced: add the new key,
issue new licenses with it, remove the old `.pub` once every client has its new license.

Until a `.pub` is committed, `netra-api` trusts no key and every deployment runs the core only;
the log says `license: no trusted key built in`.

## Issuing a license

EZRA issues client licenses with the [license manager](license-manager.md): a local web tool that
keeps the clients and their deployments, the signing key encrypted with a passphrase, every license
issued (renewals and changes included) and an audit log. `netra-license issue` does the same from a
terminal, for scripts and CI:

```bash
go run ./cmd/netra-license issue -key ~/netra-keys/ezra.key \
  -client "PLN UPT Bekasi" -edition substation -tier pro -robots 3 -days 365 \
  -installation NETRA-7KQ3-M9XD-2WPA \
  -name "NETRA Gardu" -color "#0f766e" -logo pln.svg -out bekasi.lic
go run ./cmd/netra-license inspect bekasi.lic
```

| Flag | Meaning |
|---|---|
| `-client` | client name, shown in Settings > License |
| `-edition` | `mall`, `substation` or `custom` (core only) |
| `-tier` | `basic`, `pro` or `enterprise` |
| `-add`, `-remove` | modules to add to or leave out of the edition and tier, comma-separated |
| `-map` | `floorplan`, `pgm` or `gps`; default the edition's |
| `-installation` | bind to one server's installation ID; empty: any server |
| `-robots` | robot limit, 0 for none |
| `-days` | validity, 0 for no expiry |
| `-name`, `-color`, `-color-dark`, `-logo` | branding: application name, primary colour `#rrggbb` (light and dark theme), an SVG or PNG logo up to 256 KB embedded in the license |
| `-id` | license ID (default: date and random) |

`inspect` prints the payload, checks the signature against the built-in keys (or `-pub`) and the
status. The image carries `netra-license` too, to check a file on site:
`docker compose exec api /netra-license inspect /license/netra.lic`.

## Installing a license

Either:

- **Setup wizard**: a new server has no account; the dashboard opens the wizard, which shows the
  installation ID, creates the first administrator and takes the license (or skips it: core only).
- **File**: `NETRA_LICENSE_FILE=/license/netra.lic` (the Netra compose mounts `./license`), or the
  text itself in `NETRA_LICENSE`. Read at start.
- **Dashboard**: Settings > License > upload (`PUT /api/v1/license`, admin). Applies at once, no
  restart, and is kept in the database for the next start.

At start the newest valid license of the three sources (by `issued_at`) applies. An upload older
than the current license is refused (409), so an old file cannot replace a renewal.

## What a license does not stop

- A license without `-installation` works on any server. Bind every client license to its
  installation; leave only cloud deployments run by EZRA unbound.
- Expiry follows the server's clock, so setting the clock back delays it.
- Whoever can build `netra-api` from source can remove the check. The license protects the images
  delivered to clients, not the source code.

## Renewing or upgrading

Issue a new license (a new `issued_at`) with the new expiry, tier or modules and install it the
same way. Nothing is lost when a module goes away: its data stays in the database and comes back
with the module.

## Deployments that ran before licensing

They had every module. Before deploying this version, issue their license (for a mall:
`-edition mall -tier enterprise`, or what the client bought) and set `NETRA_LICENSE_FILE`, or
upload it right after the update; until then the dashboard shows the core only.

## Adding a module

1. A constant and a [`Catalog`](../internal/license/license.go) entry; add it to the editions or
   tiers that include it.
2. Register its routes with `module(license.X, perm, pattern, handler)` in
   [`server.go`](../internal/api/server.go); robot messages of the module check `s.lic.Has`.
3. In the dashboard, `feature: 'x'` on its menu entries and pages.
4. A license issued before the module existed does not include it; reissue with `-add x`.
