#!/usr/bin/env bash
# Export per-second telemetry from TimescaleDB into a replay CSV for
# `cmd/edge -replay` (EMS shadow MVP-2 reconciliation).
#
# Run on the VM (or anywhere with DATABASE_URL access to the prod DB):
#
#   DATABASE_URL='postgres://...' ./tools/export_ze_replay.sh \
#       ze 2026-06-03T14:50:00Z 2026-06-05T04:30:00Z ze_replay.csv 50 1290
#
# Defaults cover the PCAP reference window (write_commands.csv:
# 2026-06-03 18:00 → 2026-06-05 07:18 Kyiv = 15:00 → 04:18 UTC).
#
# Columns are forward-filled (locf) per second — the same "last known
# value" semantics the edge normalizer applies between SmartLogger
# polls. If soc_percent was not collected in the window, it is
# synthesized from the cumulative charge/discharge counters
# (round-trip eta 0.95, start SOC and capacity from args) and the
# soc_source column says "synth" so the reconcile report can flag it.
set -euo pipefail

ORG="${1:-ze}"
FROM="${2:-2026-06-03T14:50:00Z}"
TO="${3:-2026-06-05T04:30:00Z}"
OUT="${4:-${ORG}_replay.csv}"
SOC0="${5:-50}"        # starting SOC %, used only for synthesized SOC
CAP_KWH="${6:-1290}"   # BESS capacity kWh, used only for synthesized SOC

if [[ -z "${DATABASE_URL:-}" ]]; then
  echo "DATABASE_URL is required" >&2
  exit 1
fi

for v in "$ORG" "$FROM" "$TO"; do
  if [[ "$v" == *"'"* || "$v" == *";"* ]]; then
    echo "invalid argument: $v" >&2
    exit 1
  fi
done
if ! [[ "$SOC0" =~ ^[0-9.]+$ && "$CAP_KWH" =~ ^[0-9.]+$ ]]; then
  echo "soc0/capacity must be numeric" >&2
  exit 1
fi

SQL=$(cat <<EOF
COPY (
WITH sec AS (
  SELECT
    time_bucket_gapfill('1 second', time, '${FROM}'::timestamptz, '${TO}'::timestamptz) AS t,
    locf(avg(value) FILTER (WHERE metric_key = 'active_pv_power_kw'))             AS pv,
    locf(avg(value) FILTER (WHERE metric_key = 'active_ess_power_kw'))            AS ess,
    locf(avg(value) FILTER (WHERE metric_key = 'grid_connected_active_power_kw')) AS grid,
    locf(avg(value) FILTER (WHERE metric_key = 'load_power_kw'))                  AS load,
    locf(avg(value) FILTER (WHERE metric_key = 'soc_percent'))                    AS soc,
    locf(max(value) FILTER (WHERE metric_key = 'ess_charge_max_kw'))              AS chmax,
    locf(max(value) FILTER (WHERE metric_key = 'ess_discharge_max_kw'))           AS dismax,
    locf(max(value) FILTER (WHERE metric_key = 'total_energy_charged_kwh'))       AS ch,
    locf(max(value) FILTER (WHERE metric_key = 'total_energy_discharged_kwh'))    AS dis
  FROM telemetry_samples
  WHERE organization_id = '${ORG}'
    AND time >= '${FROM}'::timestamptz
    AND time <  '${TO}'::timestamptz
    AND metric_key IN (
      'active_pv_power_kw','active_ess_power_kw',
      'grid_connected_active_power_kw','load_power_kw','soc_percent',
      'ess_charge_max_kw','ess_discharge_max_kw',
      'total_energy_charged_kwh','total_energy_discharged_kwh')
  GROUP BY 1
),
based AS (
  SELECT *,
    min(ch)  OVER () AS ch0,
    min(dis) OVER () AS dis0
  FROM sec
)
SELECT
  to_char(t AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS time,
  round(pv::numeric, 3)     AS active_pv_power_kw,
  round(ess::numeric, 3)    AS active_ess_power_kw,
  round(grid::numeric, 3)   AS grid_connected_active_power_kw,
  round(load::numeric, 3)   AS load_power_kw,
  CASE
    WHEN soc IS NOT NULL THEN round(soc::numeric, 1)
    WHEN ch IS NOT NULL AND dis IS NOT NULL THEN
      greatest(0, least(100, round(
        (${SOC0} + 100.0 * (0.95 * (ch - ch0) - (dis - dis0) / 0.95) / ${CAP_KWH})::numeric, 1)))
  END AS soc_percent,
  round(chmax::numeric, 3)  AS ess_charge_max_kw,
  round(dismax::numeric, 3) AS ess_discharge_max_kw,
  CASE
    WHEN soc IS NOT NULL THEN 'measured'
    WHEN ch IS NOT NULL AND dis IS NOT NULL THEN 'synth'
  END AS soc_source
FROM based
WHERE pv IS NOT NULL OR ess IS NOT NULL
ORDER BY t
) TO STDOUT WITH CSV HEADER
EOF
)

echo "exporting ${ORG} ${FROM}..${TO} -> ${OUT}" >&2
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c "$SQL" > "$OUT"

ROWS=$(( $(wc -l < "$OUT") - 1 ))
echo "rows: ${ROWS}" >&2

echo "metric coverage in window:" >&2
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atc "
  SELECT metric_key || ': ' || count(*)
  FROM telemetry_samples
  WHERE organization_id = '${ORG}'
    AND time >= '${FROM}'::timestamptz AND time < '${TO}'::timestamptz
    AND metric_key IN (
      'active_pv_power_kw','active_ess_power_kw',
      'grid_connected_active_power_kw','load_power_kw','soc_percent',
      'ess_charge_max_kw','ess_discharge_max_kw',
      'total_energy_charged_kwh','total_energy_discharged_kwh')
  GROUP BY metric_key ORDER BY metric_key;" >&2
