# SESTelemetry

Modbus telemetry collector for Huawei SmartLogger + dashboard stack.

## Services

- `collector` (Go): polls Modbus and writes telemetry to TimescaleDB
- `api` (Go): serves dashboard endpoints from TimescaleDB
- `dam-collector` (Go): once per day, fetches Day-Ahead Market (RDN) prices from oree.com.ua and stores them in `market_dam_prices`
- `weather-collector` (Go): caches Open-Meteo forecasts per org in TimescaleDB
- `economics-recompute` (Go): on a schedule, recomputes + persists hourly/daily economics into `economics_hourly` / `economics_daily` so the dashboard reads a warm cache (reads only the local DB; no external API)
- `alert-watchdog` (Go): emails operators when a Modbus device stops writing telemetry
- `web` (React + Vite): dashboard UI

## API endpoints

- `GET /healthz`
- `GET /api/v1/dashboard-config`
- `GET /api/v1/current?organization_id=demo-org`
- `GET /api/v1/timeseries?organization_id=demo-org&metric_keys=active_pv_power_kw,load_power_kw&from=2026-04-26T00:00:00Z&to=2026-04-26T23:59:59Z&bucket=15 minutes`
- `GET /swagger` (Swagger UI)
- `GET /swagger/openapi.yaml` (OpenAPI spec)

## Run with Docker Compose (DB + collector + API + web)

```bash
cd deploy
docker compose up --build
```

API will be available at `http://localhost:8080`.
Web dashboard will be available at `http://localhost:5173/?organization_id=docker-demo`.

## Run web dashboard separately

```bash
cd web
npm install
npm run dev
```

Dashboard opens on Vite default URL (`http://localhost:5173`), and expects API at `http://localhost:8080`.

Use query param for organization:

`http://localhost:5173/?organization_id=demo-org`

## Environment notes

- `DATABASE_URL` can override DB connection for collector and api
- For web API target override, set `VITE_API_BASE_URL`
- Set `modbus.host: mock` for an in-process mocked Modbus source
- `SESTELEMETRY_MAX_REGISTERS_PER_READ` can reduce FC3/FC4 batch size (1-125, default 125)

## Multiple Modbus devices per organization

An organization can be backed by either a single `modbus:` block (the
legacy single-device shape) or by a list of `modbus_devices:`. Each device
declares its own host plus an optional `metric_keys` whitelist that picks
which catalog entries that physical box is responsible for. Samples from
all devices are written under the same `organization_id`, so the dashboard
keeps showing one tenant.

```yaml
organizations:
  - id: ze
    name: ZE
    poll_interval: 1s
    modbus_devices:
      - host: 10.28.40.102            # ESS / UZE smartlogger
        metric_keys:
          - active_ess_power_kw
          - energy_charged_day_kwh
          - energy_discharged_day_kwh
      - host: 10.28.40.101            # PV / load smartlogger
        metric_keys:
          - pv_energy_yield_day_kwh
          - load_power_kw
          - grid_connected_active_power_kw
```

Notes:

- `modbus:` and `modbus_devices:` are mutually exclusive on a single org.
- Per-device defaults (`port: 502`, `unit_id: 99`, 5s timeouts) match the
  legacy single-modbus block, so you only set what you want to override.
- Unknown `metric_keys` are caught at startup and produce a clear
  `bootstrap` error naming the offending org and device host.
- The collector spawns one goroutine and one TCP session per device;
  device-A failures do not stall device-B.

## Energy flow (PV / Grid / ESS / Load)

The API computes four directional energy flows on the fly from the
SmartLogger accumulators and surfaces them in
`/api/v1/energy-summary` alongside the raw counter deltas. The
dashboard renders them as a live diagram (kW) plus a four-line
period summary (kWh).

| `metric_key`        | meaning                                    |
| ------------------- | ------------------------------------------ |
| `pv_to_ess_kwh`     | PV energy that charged the ESS             |
| `grid_to_ess_kwh`   | Grid imports that charged the ESS          |
| `ess_to_load_kwh`   | ESS discharge that fed the load            |
| `ess_to_grid_kwh`   | ESS discharge that exported to the grid    |

These four values are NOT persisted in `telemetry_samples`. When the
dashboard asks for any of the four keys, the EnergySummary handler
(`internal/api/energyflow.go`) pulls the raw Modbus counter rows for
[from, to] from `telemetry_samples`, hands them to
`energyflow.Recompute` in `internal/energyflow/recompute.go`, and
returns the totals in the response's typed `flows` field. The
allocation rule itself lives in `internal/energyflow/allocate.go`.
On-the-fly compute is currently gated to day-sized windows (see
`maxEnergyFlowWindow`); month/year refreshes skip the allocator
until we ship a daily-rollup cache.

### Topology auto-detection

The site topology (single SmartLogger covering both PV and ESS, or
two SmartLoggers split by role) is detected automatically from each
device's `metric_keys` whitelist. There is no separate YAML field —
just declare which metrics each device polls:

- A device whose whitelist covers all three PV accumulators **and**
  both ESS accumulators is classified as `RoleSingle`.
- A device whose whitelist covers only the PV accumulators is the
  PV side of a dual deployment (`RolePV`).
- A device whose whitelist covers only the ESS accumulators is the
  ESS side (`RoleESS`).
- An organization that does not cover both PV and ESS across all
  its devices has no rows for the on-the-fly compute to chew on, so
  the API returns `flows` with zero values; the dashboard hides the
  period-flow card on the day preset only when the request didn't
  ask for synthetic keys at all.

For the `ze` deployment to enable energy flow, expand the existing
whitelists, e.g.:

```yaml
modbus_devices:
  - host: 10.28.40.101            # PV / load smartlogger
    metric_keys:
      - active_pv_power_kw
      - load_power_kw
      - grid_connected_active_power_kw
      - accumulated_pv_energy_yield_kwh
      - accumulated_electricity_purchased_kwh
      - accumulated_electricity_sold_kwh
      - accumulated_power_consumption_kwh
  - host: 10.28.40.102            # ESS smartlogger
    metric_keys:
      - active_ess_power_kw
      - soc_percent
      - total_energy_charged_kwh
      - total_energy_discharged_kwh
```

### Optional: `ess_discharge_sign`

By convention `active_ess_power_kw > 0` means the battery is
discharging. Some inverter firmwares invert that — set
`ess_discharge_sign: -1` on the organization to silence the
diagnostic warning. The cumulative `total_energy_charged_kwh` /
`total_energy_discharged_kwh` accumulators are not affected by this
flag, so the four flow totals stay correct regardless.

```yaml
organizations:
  - id: ze
    ess_discharge_sign: -1     # firmware reports charge as positive
```

Allowed values: `0` (= default 1), `1`, `-1`. Any other value
fails validation at startup.

## Day-Ahead Market collector (RDN)

`dam-collector` downloads the OREE DAM XLS once per day and upserts hourly
prices/volumes into `market_dam_prices`. The container is part of every stack
but the daemon is gated by `oree.enabled` in `config.yaml` — with the section
absent or `enabled: false` it idles without touching the network, so existing
modbus-only deployments are unaffected by an update.

Configure under the `oree:` section in `config.yaml` (see
`config.example.yaml`):

```yaml
oree:
  enabled: true
  zone: 2                     # 1 = Burshtyn island, 2 = unified UA grid
  run_at: "15:30"             # local time of day in `timezone`
  timezone: "Europe/Kyiv"
  delivery_offset_days: 1     # 1 = fetch tomorrow's prices
```

To turn the service on, flip `oree.enabled: true` in
`/etc/sestelemetry/config.yaml` and run `sudo systemctl restart sestelemetry`.
The service performs an idempotent catch-up on startup (skipped if 24 rows
are already present for the target date) and then sleeps until the next
`run_at` in the configured timezone. URL hit per fetch:
`https://www.oree.com.ua/index.php/PXS/downloadxlsx/DD.MM.YYYY/DAM/{zone}`.

For operator-driven backfill (OREE published late, network blip, etc) the
API exposes `POST /api/v1/dam-prices/refresh?date=YYYY-MM-DD&zone=N` which
runs a single synchronous fetch and upsert. The economics dashboard wires
this up via the "Оновити ціни РДН" button.

## Economics recompute scheduler

`economics-recompute` recomputes and persists economics in the background so
the dashboard always reads the stored `economics_hourly` / `economics_daily`
tables instead of triggering a slow live recompute on each request. The
container ships with every stack but is gated by `economics.enabled` in
`config.yaml` — absent or `enabled: false` and the daemon idles. It reads only
the local database (telemetry, DAM prices, tariffs, any already-imported
canonical KPIs) and never calls FusionSolar or any other external API.

Two schedules run concurrently for all configured organizations:

- nightly at `run_at`: recompute the last `finalize_days` days (ending
  yesterday). Those days are final, so the API serves them straight from cache.
- every `today_interval`: recompute the current, still-open day so the
  dashboard's "today" stays fresh. The API serves this cached non-final day
  while it is within `2 x today_interval` of being written, falling back to a
  live recompute if the daemon is stopped.

Configure under the `economics:` section in `config.yaml` (see
`config.example.yaml`):

```yaml
economics:
  enabled: true
  run_at: "03:00"             # local time of day in `timezone`
  timezone: "Europe/Kyiv"
  finalize_days: 3            # nightly: recompute the last N days
  today_interval: 1h          # refresh the current day this often
  max_concurrency: 2          # how many orgs to recompute in parallel
```

The manual recompute dialog on the economics dashboard still works (forced
backfills after tariff/DAM edits); FusionSolar reconciliation still requires
the manual archive import. The daemon supports a one-shot `-once` flag (single
finalize + today pass, then exit) for cron or testing.

## Connectivity alerts by email

`alert-watchdog` emails operators when a site stops reporting. Every
`check_interval` it reads the freshest telemetry timestamp of each configured
Modbus device; a device silent for longer than `stale_after` is announced as a
lost connection, and (optionally) again when it comes back. Because detection
reads the database rather than the Modbus link, a crashed collector produces
the same alert as a dead network or a powered-off SmartLogger.

Devices that change state in one check and share a destination address share a
single email — a site-wide outage sends one message, not one per elevator.
While a device stays down a reminder goes out every `repeat_interval`. If a
send fails (the uplink that died often took the mail relay with it), that
message's devices are not marked as notified and the next check retries them;
mail that did go out to other recipients is unaffected.

### Configuring it

The dashboard's **Сповіщення** page (`?view=alerts`) is where this is set up:
the SMTP server, the thresholds above, a default recipient list, and per
organization a switch plus its own address list. **An organization's own list
replaces the default one** — that is how a site gets routed to its operator
alone. Leave it empty to inherit the default list.

Settings live in `alert_settings` / `organization_alert_settings`
(`migrations/011_alert_settings.sql`, also created on startup) and the watchdog
re-reads them every check, so a change takes effect without restarting the
container. The SMTP password is stored in its own column and is never returned
by the API: the page only learns whether one is set, and saving without
touching the field leaves it alone.

> **Access.** The API has no authentication, and the password sits in Postgres
> as plain text. Do not expose the API port to the internet — keep it on an
> internal network or behind an authenticating proxy.

The `alerts:` block in `config.yaml` is the fallback used until the page is
saved for the first time (the page shows those values, so a first save does not
silently downgrade a YAML-configured deployment):

```yaml
alerts:
  enabled: true
  check_interval: 1m          # how often to sample freshness
  stale_after: 10m            # silence longer than this = connection lost
  repeat_interval: 6h         # remind while still down; negative disables
  notify_recovery: true       # also email when a device comes back
  smtp:
    host: smtp.example.com
    port: 587                 # defaults: 587 (starttls/none), 465 (implicit)
    tls: starttls             # starttls | implicit | none
    username: alerts@example.com
    password: ""              # prefer the SMTP_PASSWORD env var
    from: "СЕС Моніторинг <alerts@example.com>"
    to:
      - ops@example.com
    timeout: 20s
```

Keep the password out of the YAML: `SMTP_PASSWORD` overrides
`alerts.smtp.password`, and `deploy/docker-compose.yml` passes it through from
`deploy/.env`. A password saved on the settings page wins over both.

An internal relay on port 25 works too. Credentials are only sent when the
relay advertises `AUTH` (`PLAIN`, or `LOGIN` for Exchange), so a relay that
authorizes by source IP is fine with the username field filled in or empty;
with `tls: none` they travel in the clear, which is a private-network-only
proposition.

The page's **Надіслати тестовий лист** button verifies delivery to the real
address list; the same check from a shell (works even with alerts disabled, and
without a reachable database):

```bash
cd deploy
docker compose run --rm alert-watchdog -config /etc/sestelemetry/config.yaml -test-email
```

`-once` runs a single check pass and exits, for cron or debugging. Device state
lives in `device_alert_state` (`migrations/010_alerts.sql`, also created on
startup), so restarting the container does not re-announce an outage operators
already know about.

### Safe production update path

Re-running `bash scripts/install-prod.sh` on a host that already has
`/etc/sestelemetry/config.yaml` is **non-destructive**: the script seeds those
files only when missing (`if ! sudo test -f`). The container mounts them
read-only. Watchtower only updates Docker images; host files are untouched.
Your existing modbus configuration on the production host stays exactly as it
is across this update — and `dam-collector` stays inert until you opt in.

## Run locally without Docker

```bash
cp config.example.yaml config.yaml
bash scripts/run-local.sh all
```

Or start only backend services:

```bash
bash scripts/run-local.sh backend
```

## Run as Linux service (auto restart + image auto-updates)

One-command installer (recommended for new PC):

```bash
bash scripts/install-prod.sh
```

If `.env.service` still has placeholder values (`your-org`, `change-me`), edit it and restart:

```bash
sudo editor /opt/sestelemetry/deploy/.env.service
sudo systemctl restart sestelemetry
```

```bash
# on server
sudo mkdir -p /opt/sestelemetry
sudo rsync -a ./ /opt/sestelemetry/
cd /opt/sestelemetry/deploy

cp service.env.example .env.service
# edit image names/tags in .env.service
# set DB credentials and SESTELEMETRY_DATABASE_URL
# set SESTELEMETRY_API_ALLOW_ORIGIN to your web URL (no "*")
# set SESTELEMETRY_WEB_API_BASE_URL to your server URL, e.g. http://SERVER_IP:8080
# keep collector config outside repo:
#   SESTELEMETRY_HOST_CONFIG_PATH=/etc/sestelemetry/config.yaml
#   SESTELEMETRY_HOST_REGISTERS_PATH=/etc/sestelemetry/registers

sudo cp sestelemetry.service /etc/systemd/system/sestelemetry.service
sudo systemctl daemon-reload
sudo systemctl enable sestelemetry
sudo systemctl start sestelemetry
```

Persist Modbus config outside git-managed repo:

```bash
sudo mkdir -p /etc/sestelemetry/registers
sudo cp /opt/sestelemetry/config.docker.yaml /etc/sestelemetry/config.yaml
sudo cp -r /opt/sestelemetry/registers/* /etc/sestelemetry/registers/
sudo chown -R root:root /etc/sestelemetry
```

Manage service:

```bash
sudo systemctl status sestelemetry
sudo systemctl restart sestelemetry
sudo systemctl stop sestelemetry
```

Health checks:

```bash
curl -fsS http://localhost:8080/healthz
curl -fsS http://localhost:8080/readyz
docker compose -f /opt/sestelemetry/deploy/docker-compose.service.yml --env-file /opt/sestelemetry/deploy/.env.service ps
```

Rollback basics:

```bash
# set previous working image tags in .env.service, then:
sudo systemctl restart sestelemetry
```

Notes:

- `restart: unless-stopped` keeps containers running after reboots/failures.
- `watchtower` watches tagged containers and restarts them after pulling newer images.
- keep `.env.service` out of git and restrict permissions (example: `chmod 600 /opt/sestelemetry/deploy/.env.service`).
- TimescaleDB data is pinned to stable Docker volume `sestelemetry_timescaledb_data` to survive updates/recreates.

