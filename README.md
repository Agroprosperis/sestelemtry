# SESTelemetry

Modbus telemetry collector for Huawei SmartLogger + dashboard stack.

## Services

- `collector` (Go): polls Modbus and writes telemetry to TimescaleDB
- `api` (Go): serves dashboard endpoints from TimescaleDB
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

sudo cp sestelemetry.service /etc/systemd/system/sestelemetry.service
sudo systemctl daemon-reload
sudo systemctl enable sestelemetry
sudo systemctl start sestelemetry
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

