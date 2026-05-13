import {
  fetchDAMPrices,
  fetchPvForecast,
  fetchTimeseries,
} from '../api'
import type { TimeseriesPoint } from '../types'
import { aggregatePvForecastHourly, elevatorCodeFor } from './transforms/pvForecast'

export type ExportTable = {
  headers: string[]
  rows: Array<Record<string, unknown>>
  // warnings carries non-fatal data-source failures so the dialog can
  // surface them to the analyst (e.g. "DAM prices unavailable —
  // column is empty"). Fatal errors still throw; this list is only
  // for sources whose absence we paper over with empty cells.
  warnings: string[]
}

// Energy metric columns offered for export. Mirrors the EnergyChart's
// stacked metrics; aggregation=delta turns each accumulator counter into
// per-bucket consumption / production. Order is the spreadsheet-friendly
// "production first, consumption next" so analysts don't have to reorder.
export const ENERGY_EXPORT_METRICS = [
  'accumulated_pv_energy_yield_kwh',
  'accumulated_electricity_sold_kwh',
  'accumulated_electricity_purchased_kwh',
  'accumulated_power_consumption_kwh',
  'total_energy_charged_kwh',
  'total_energy_discharged_kwh',
] as const

// Day-preset instantaneous power columns (kW, aggregation=last). For
// non-fine buckets (>= 1 hour) recharts/the dashboard never reads `last`
// so we still allow it but warn the user that the value is the last
// observed sample of the bucket — useful for spot-checking, not for
// energy accounting.
export const POWER_EXPORT_METRICS = [
  'active_pv_power_kw',
  'active_ess_power_kw',
  'grid_connected_active_power_kw',
  'load_power_kw',
] as const

// Device-level diagnostic metrics. These are vendor registers that
// describe the SmartLogger itself (clock, firmware, etc.) rather than
// the energy flow. Kept in their own group so the dialog can offer
// them without polluting the energy/power checkboxes used by the
// dashboard charts.
export const DEVICE_EXPORT_METRICS = [
  'local_time_epoch_s',
] as const

// Synthetic energy-flow counters produced by the collector's
// `internal/energyflow` package. They share the cumulative-kWh shape
// of the accumulators in ENERGY_EXPORT_METRICS (and are listed in
// `EnergySummaryAccumulators` on the backend), so the bucketed export
// uses the same `aggregation: 'delta'` path. No Modbus register backs
// them — `annotateMetricHeader` leaves the column names un-suffixed,
// matching the "synthetic columns keep their plain header" convention
// already used for dam/forecast.
export const FLOW_EXPORT_METRICS = [
  'pv_to_ess_kwh',
  'grid_to_ess_kwh',
  'ess_to_load_kwh',
  'ess_to_grid_kwh',
] as const

export type CustomExportColumns = {
  energy: boolean
  price: boolean
  soc: boolean
  power: boolean
  forecast: boolean
  device: boolean
  flow: boolean
}

// Bucket sizes offered by the export dialog. `raw` is special: it
// bypasses /api/v1/timeseries entirely and streams unbucketed
// telemetry_samples rows from /api/v1/samples. Every other value
// drives a bucketed query through the standard timeseries pipeline.
export type CustomExportBucket = 'raw' | '5 minutes' | '1 hour' | '1 day' | '1 month'

// Hard upper bound on rows the raw export will request from the
// server. Mirrors the server-side `maxSamplesLimit` in
// internal/api/handlers.go; bumping one side requires bumping the
// other so the dialog's "obмеження" hint stays truthful.
export const RAW_SAMPLES_LIMIT = 5_000_000

// Maximum range (in days) accepted by /api/v1/samples. Mirrors
// `maxSamplesRange` on the server (31 days). The dialog disables the
// raw option when the picked range exceeds this so the user gets
// validation feedback before submitting.
export const RAW_SAMPLES_MAX_DAYS = 31

export type CustomExportInput = {
  organizationID: string
  // from is inclusive, to is exclusive (matches /api/v1/timeseries semantics).
  from: Date
  to: Date
  bucket: CustomExportBucket
  columns: CustomExportColumns
  // registerAddresses, when provided, switches the wide-CSV header
  // formatter from `metric_key` to `metric_key_<address>` for every
  // metric that has a known Modbus register. Synthetic columns
  // (DAM price, PV forecast, time) keep their plain header so an
  // analyst doesn't have to hunt for them. Pass undefined to skip
  // annotation entirely.
  registerAddresses?: Record<string, number>
  signal?: AbortSignal
}

// annotateMetricHeader produces the wide-CSV header for a given
// metric_key. When the metric is backed by a Modbus register
// (`accumulated_pv_energy_yield_kwh` -> 40446), we suffix the address
// with an underscore (`accumulated_pv_energy_yield_kwh_40446`) so the
// header parses cleanly in tools that don't allow spaces or brackets
// in column names. Metrics without an address are left untouched.
export function annotateMetricHeader(
  metricKey: string,
  addresses?: Record<string, number>,
): string {
  const addr = addresses?.[metricKey]
  if (addr === undefined || addr === null) return metricKey
  return `${metricKey}_${addr}`
}

const ONE_DAY_MS = 24 * 60 * 60 * 1000

// autoBucket picks a sensible bucket size given the requested range so the
// CSV doesn't explode into hundreds of thousands of rows. Thresholds are
// chosen to keep typical exports below ~10k rows on the longest path.
//
// The `raw` bucket is intentionally never auto-selected: it bypasses
// time_bucket() and can dump 100k+ rows for a single day, so the
// analyst must opt into it explicitly via the dropdown.
export function autoBucket(from: Date, to: Date): CustomExportBucket {
  const days = Math.max(0, (to.getTime() - from.getTime()) / ONE_DAY_MS)
  if (days > 366) return '1 month'
  if (days > 35) return '1 day'
  if (days > 2) return '1 hour'
  return '5 minutes'
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

function toDateOnly(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

// bucketStartMs is only meaningful for the bucketed export path. The
// `raw` mode never goes through fetchCustomExportData (it streams
// straight from /api/v1/samples) so we treat anything else by
// rounding down to the nearest 5-minute boundary as a defensive
// fallback should this ever be called with `raw`.
function bucketStartMs(t: Date, bucket: CustomExportBucket): number {
  const d = new Date(t)
  if (bucket === '1 month') {
    d.setDate(1)
    d.setHours(0, 0, 0, 0)
  } else if (bucket === '1 day') {
    d.setHours(0, 0, 0, 0)
  } else if (bucket === '1 hour') {
    d.setMinutes(0, 0, 0)
  } else {
    const m = Math.floor(d.getMinutes() / 5) * 5
    d.setMinutes(m, 0, 0)
  }
  return d.getTime()
}

function bucketLabel(t: number, bucket: CustomExportBucket): string {
  const d = new Date(t)
  if (bucket === '1 month') return `${d.getFullYear()}-${pad(d.getMonth() + 1)}`
  if (bucket === '1 day') return toDateOnly(d)
  if (bucket === '1 hour') {
    return `${toDateOnly(d)} ${pad(d.getHours())}:00`
  }
  return `${toDateOnly(d)} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function bucketKeyForPoint(p: TimeseriesPoint, bucket: CustomExportBucket): number | null {
  const t = new Date(p.time)
  if (!Number.isFinite(t.getTime())) return null
  return bucketStartMs(t, bucket)
}

function isAbortError(e: unknown): boolean {
  return e instanceof DOMException && e.name === 'AbortError'
}

// fetchCustomExportData runs the parallel fetches required for the
// requested column set, joins them by bucket-start timestamp, and returns
// an ExportTable that the existing CSV serializer can render directly.
//
// The `raw` bucket is rejected up front: that mode lives on the
// /api/v1/samples streaming endpoint and uses an entirely different
// shape (one row per sample, no per-bucket merge), so the dialog
// hands it to fetchRawSamplesCsv instead of going through here.
export async function fetchCustomExportData(input: CustomExportInput): Promise<ExportTable> {
  const { organizationID, from, to, bucket, columns, signal, registerAddresses } = input
  if (bucket === 'raw') {
    throw new Error('raw bucket is handled by fetchRawSamplesCsv, not this path')
  }
  const fromIso = from.toISOString()
  const toIso = to.toISOString()
  // header(metric_key) collapses to either `metric_key_40388` or the
  // plain key depending on whether the Modbus register is known. We
  // memoize the lookup so the same display name is reused across
  // header construction and per-row population without recomputing.
  const header = (metricKey: string) =>
    annotateMetricHeader(metricKey, registerAddresses)

  // Always include `time` first; the rest of the column order matches the
  // checkbox group order in the dialog so the CSV reads top-to-bottom in
  // the same shape the user configured it. When a registerAddresses map
  // is supplied, telemetry headers gain a `_<address>` suffix; synthetic
  // columns (dam, forecast) keep their plain header because they don't
  // correspond to a Modbus register.
  const headers: string[] = ['time']
  if (columns.energy) headers.push(...ENERGY_EXPORT_METRICS.map(header))
  if (columns.flow) headers.push(...FLOW_EXPORT_METRICS.map(header))
  if (columns.price) headers.push('dam_price_uah_per_mwh')
  if (columns.soc) headers.push(header('soc_percent'))
  if (columns.power) headers.push(...POWER_EXPORT_METRICS.map(header))
  if (columns.device) headers.push(...DEVICE_EXPORT_METRICS.map(header))
  if (columns.forecast) headers.push('planned_ac_kw_forecast')

  // Empty selection short-circuits to an empty table so the dialog can
  // surface a "select at least one column" hint without firing a request.
  if (headers.length === 1) return { headers, rows: [], warnings: [] }

  // Range covers a single calendar day exactly when from is local midnight
  // and to is exactly +24h. Forecast is fetched only for that case (the
  // n8n flow returns one day at a time).
  const isSingleDay =
    bucket === '5 minutes' &&
    from.getHours() === 0 &&
    from.getMinutes() === 0 &&
    from.getSeconds() === 0 &&
    to.getTime() - from.getTime() === ONE_DAY_MS

  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || undefined

  const energyKeys = columns.energy ? [...ENERGY_EXPORT_METRICS] : []
  const flowKeys = columns.flow ? [...FLOW_EXPORT_METRICS] : []
  const powerKeys = columns.power ? [...POWER_EXPORT_METRICS] : []
  const deviceKeys = columns.device ? [...DEVICE_EXPORT_METRICS] : []
  const elevator = columns.forecast ? elevatorCodeFor(organizationID) : null

  const energyP = energyKeys.length
    ? fetchTimeseries(
        {
          organizationID,
          metricKeys: energyKeys,
          from: fromIso,
          to: toIso,
          bucket,
          tz,
          aggregation: 'delta',
        },
        signal,
      )
    : Promise.resolve(null)
  // Flow counters are cumulative kWh too, so the same `delta`
  // aggregation that turns accumulator counters into per-bucket
  // production applies. The synthetic flow keys aren't stored in
  // telemetry_samples anymore — they are computed on the fly inside
  // /api/v1/energy-summary — so a timeseries request for them
  // currently returns zero points. The column ends up empty without
  // aborting the export, which is the desired behaviour until we
  // wire the export through the on-the-fly compute path.
  const flowP = flowKeys.length
    ? fetchTimeseries(
        {
          organizationID,
          metricKeys: flowKeys,
          from: fromIso,
          to: toIso,
          bucket,
          tz,
          aggregation: 'delta',
        },
        signal,
      )
    : Promise.resolve(null)
  const socP = columns.soc
    ? fetchTimeseries(
        {
          organizationID,
          metricKeys: ['soc_percent'],
          from: fromIso,
          to: toIso,
          bucket,
          tz,
          aggregation: 'avg',
        },
        signal,
      )
    : Promise.resolve(null)
  const powerP = powerKeys.length
    ? fetchTimeseries(
        {
          organizationID,
          metricKeys: powerKeys,
          from: fromIso,
          to: toIso,
          bucket,
          tz,
          aggregation: 'last',
        },
        signal,
      )
    : Promise.resolve(null)
  // Device metrics use `last` so the bucketed export shows the most
  // recent reading inside the bucket (the SmartLogger clock advances
  // monotonically — averaging would smear the value, last is the
  // honest summary).
  const deviceP = deviceKeys.length
    ? fetchTimeseries(
        {
          organizationID,
          metricKeys: deviceKeys,
          from: fromIso,
          to: toIso,
          bucket,
          tz,
          aggregation: 'last',
        },
        signal,
      )
    : Promise.resolve(null)
  // damFailed / forecastFailed are flipped inside the .catch handlers
  // when a non-abort error swallows a partial source. The dialog reads
  // table.warnings to render a non-blocking notice — without these
  // flags the user would silently get a CSV with a missing column.
  let damFailed = false
  let forecastFailed = false
  const damP = columns.price
    ? fetchDAMPrices(
        {
          from: toDateOnly(from),
          to: toDateOnly(new Date(to.getTime() - 1)),
          zone: 2,
        },
        signal,
      ).catch((e) => {
        if (isAbortError(e)) throw e
        damFailed = true
        return null
      })
    : Promise.resolve(null)
  const forecastP =
    columns.forecast && elevator && isSingleDay
      ? fetchPvForecast(
          { elevatorCode: elevator, forecastDay: toDateOnly(from) },
          signal,
        ).catch((e) => {
          if (isAbortError(e)) throw e
          forecastFailed = true
          return []
        })
      : Promise.resolve([])

  const [energy, flow, soc, power, device, dam, forecast] = await Promise.all([
    energyP,
    flowP,
    socP,
    powerP,
    deviceP,
    damP,
    forecastP,
  ])

  // bucketTimes is the union of bucket starts seen in any series. Using a
  // sorted set keeps the row order chronological and avoids materializing
  // missing timeline buckets — empty buckets are simply absent from the
  // CSV, matching the existing per-chart export behavior.
  const bucketTimes = new Set<number>()
  const valuesByKeyByTime = new Map<string, Map<number, number>>()

  function take(points: TimeseriesPoint[] | undefined, keys: readonly string[]) {
    if (!points) return
    for (const p of points) {
      if (!keys.includes(p.metric_key)) continue
      const key = bucketKeyForPoint(p, bucket)
      if (key === null) continue
      let inner = valuesByKeyByTime.get(p.metric_key)
      if (!inner) {
        inner = new Map<number, number>()
        valuesByKeyByTime.set(p.metric_key, inner)
      }
      inner.set(key, p.value)
      bucketTimes.add(key)
    }
  }
  take(energy?.points, energyKeys)
  take(flow?.points, flowKeys)
  take(soc?.points, ['soc_percent'])
  take(power?.points, powerKeys)
  take(device?.points, deviceKeys)

  // DAM is hourly; broadcast its value to every covered bucket of the
  // requested resolution. For 1d/1mo buckets we average the price across
  // the bucket's hours.
  const damValues = new Map<number, { sum: number; count: number }>()
  if (dam?.prices) {
    for (const p of dam.prices) {
      if (p.price_uah_per_mwh == null || !Number.isFinite(p.price_uah_per_mwh)) continue
      const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(p.delivery_date)
      if (!m) continue
      const date = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]))
      // OREE delivery hour is 1..24 where 1 means 00:00..01:00 (hour-start).
      const hourStart = new Date(date)
      hourStart.setHours(Math.max(0, p.hour - 1), 0, 0, 0)
      const key = bucketStartMs(hourStart, bucket)
      const acc = damValues.get(key) ?? { sum: 0, count: 0 }
      acc.sum += p.price_uah_per_mwh
      acc.count += 1
      damValues.set(key, acc)
      bucketTimes.add(key)
    }
  }

  // Forecast: hourly; only meaningful at 5-min bucket within a single day.
  const forecastByHourStart = new Map<number, number>()
  if (forecast.length > 0 && isSingleDay) {
    const hourly = aggregatePvForecastHourly(forecast)
    for (const r of hourly) {
      const t = new Date(from)
      t.setHours(r.hour, 0, 0, 0)
      forecastByHourStart.set(t.getTime(), r.plannedKw)
    }
  }

  const sortedTimes = Array.from(bucketTimes).sort((a, b) => a - b)
  const rows: Array<Record<string, unknown>> = sortedTimes.map((t) => {
    const row: Record<string, unknown> = { time: bucketLabel(t, bucket) }
    // Row keys must agree with the headers list — when annotation is
    // active each metric stores its value under the `_<address>`
    // header, otherwise under the plain metric_key. csv.rowsToCsv
    // accesses `row[h]` for every h in headers so a mismatch would
    // produce empty cells.
    if (columns.energy) {
      for (const key of ENERGY_EXPORT_METRICS) {
        const v = valuesByKeyByTime.get(key)?.get(t)
        row[header(key)] = typeof v === 'number' && Number.isFinite(v) ? v : null
      }
    }
    if (columns.flow) {
      for (const key of FLOW_EXPORT_METRICS) {
        const v = valuesByKeyByTime.get(key)?.get(t)
        row[header(key)] = typeof v === 'number' && Number.isFinite(v) ? v : null
      }
    }
    if (columns.price) {
      const acc = damValues.get(t)
      row.dam_price_uah_per_mwh = acc && acc.count > 0 ? acc.sum / acc.count : null
    }
    if (columns.soc) {
      const v = valuesByKeyByTime.get('soc_percent')?.get(t)
      row[header('soc_percent')] = typeof v === 'number' && Number.isFinite(v) ? v : null
    }
    if (columns.power) {
      for (const key of POWER_EXPORT_METRICS) {
        const v = valuesByKeyByTime.get(key)?.get(t)
        row[header(key)] = typeof v === 'number' && Number.isFinite(v) ? v : null
      }
    }
    if (columns.device) {
      for (const key of DEVICE_EXPORT_METRICS) {
        const v = valuesByKeyByTime.get(key)?.get(t)
        row[header(key)] = typeof v === 'number' && Number.isFinite(v) ? v : null
      }
    }
    if (columns.forecast) {
      // Forecast is hourly even when the rest of the row is on a 5-min
      // bucket — broadcast the hour's value across every 5-min bucket of
      // that hour so analysts can compare row-by-row.
      const hourStart = new Date(t)
      hourStart.setMinutes(0, 0, 0)
      const v = forecastByHourStart.get(hourStart.getTime())
      row.planned_ac_kw_forecast = typeof v === 'number' && Number.isFinite(v) ? v : null
    }
    return row
  })

  const warnings: string[] = []
  if (damFailed) {
    warnings.push('Не вдалось завантажити ціни РДН — колонка пуста.')
  }
  if (forecastFailed) {
    warnings.push('Не вдалось завантажити прогноз СЕС — колонка пуста.')
  }

  return { headers, rows, warnings }
}

export function customExportFilename(input: {
  organizationID: string
  from: Date
  to: Date
  bucket: CustomExportBucket
}): string {
  const safeOrg = input.organizationID.replace(/[^A-Za-z0-9_-]+/g, '_')
  // to is exclusive in the API but in the filename the user expects an
  // inclusive end, so subtract one day from `to` for display.
  const inclusiveEnd = new Date(input.to.getTime() - 1)
  const bucketSuffix = input.bucket.replace(/\s+/g, '')
  return `export_${safeOrg}_${toDateOnly(input.from)}_${toDateOnly(inclusiveEnd)}_${bucketSuffix}.csv`
}

// rawExportMetricKeys flattens the column-group checkboxes back into
// the metric_keys list /api/v1/samples expects. Forecast and DAM
// price columns are intentionally absent: those data sources don't
// live in `telemetry_samples` and therefore have no raw rows.
//
// `local_time_epoch_s` is force-included whenever any other metric
// is selected: the wide pivot uses it to render the human-readable
// `local_time` column that the export contract guarantees, and an
// analyst won't reliably remember to tick the "Час пристрою" box
// every time. The metric is tiny (one UINT32 per poll), so the cost
// of always shipping it is negligible compared to the diagnostic
// value of always seeing the SmartLogger's wall clock alongside its
// readings.
export function rawExportMetricKeys(columns: CustomExportColumns): string[] {
  const keys: string[] = []
  if (columns.energy) keys.push(...ENERGY_EXPORT_METRICS)
  if (columns.flow) keys.push(...FLOW_EXPORT_METRICS)
  if (columns.soc) keys.push('soc_percent')
  if (columns.power) keys.push(...POWER_EXPORT_METRICS)
  if (columns.device) keys.push(...DEVICE_EXPORT_METRICS)
  if (keys.length > 0 && !keys.includes('local_time_epoch_s')) {
    keys.push('local_time_epoch_s')
  }
  return keys
}
