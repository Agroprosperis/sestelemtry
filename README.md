# SESTelemetry

Modbus telemetry collector for Huawei SmartLogger + dashboard stack.

## Services

- `collector` (Go): polls Modbus and writes telemetry to TimescaleDB
- `api` (Go): serves dashboard endpoints from TimescaleDB
- `dam-collector` (Go): once per day, fetches Day-Ahead Market (RDN) prices from oree.com.ua and stores them in `market_dam_prices`
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
prices/volumes into `market_dam_prices`. The service is **opt-in** behind the
`dam` Compose profile: existing modbus-only deployments are unaffected by an
update — `dam-collector` will not start unless you explicitly enable it.

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

To turn the service on under `docker-compose.service.yml` (production),
add to `.env.service`:

```
COMPOSE_PROFILES=dam
```

Then `sudo systemctl restart sestelemetry`. The service performs an idempotent
catch-up on startup (skipped if 24 rows are already present for the target
date) and then sleeps until the next `run_at` in the configured timezone. URL
hit per fetch: `https://www.oree.com.ua/index.php/PXS/downloadxlsx/DD.MM.YYYY/DAM/{zone}`.

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

