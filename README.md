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

## Run with Docker Compose (DB + collector + API)

```bash
cd deploy
docker compose up --build
```

API will be available at `http://localhost:8080`.

## Run web dashboard

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
