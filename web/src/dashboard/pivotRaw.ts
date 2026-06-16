import { rowsToCsv } from './csv'
import { annotateMetricHeader } from './customExport'

// pivotRawCsvToWide turns the long-format raw CSV produced by
// /api/v1/samples (one row per sample, with a `metric_key` column)
// into the wide format analysts expect from a spreadsheet export
// (one row per timestamp+device, columns = metric_keys). The wide
// shape is what the user pointed at when comparing to "the other
// system" — it sits next to the bucketed export's layout so an
// analyst opening either file sees the same columns/rows model.
//
// We pivot client-side rather than server-side because:
//   - the raw endpoint already streams comfortably for 100 K-1 M rows,
//   - keeping the server format stable (long) means non-dashboard
//     consumers (curl, Python notebooks) don't break,
//   - JS pivot of ~1 M long rows -> ~150 K wide rows is well under
//     a second on a 2020-era laptop and runs on a worker-friendly
//     pure-data input.
//
// Input rows are already ordered by (time ASC, metric_key ASC) by
// the SQL query; rows from a single device-poll therefore arrive in
// a contiguous block (all metrics share a single time.Now() per
// poll). We exploit that to group on the fly without buffering the
// whole result set.

const TRUNCATION_PREFIX = '__TRUNCATED__,'

export type PivotInput = {
  longCsv: string
  // metricKeys defines the column order of the wide CSV. Keys with
  // no matching sample in the response still get an (empty) column
  // so the output shape is deterministic and matches the request —
  // a downstream pivot table doesn't suddenly grow/shrink because
  // a sensor was offline.
  metricKeys: readonly string[]
  // registerAddresses, when supplied, annotates each metric column
  // header with `_<address>` (e.g. `active_pv_power_kw_40388`) for
  // parity with the bucketed-wide export.
  registerAddresses?: Record<string, number>
}

export type PivotResult = {
  csv: string
  rows: number
  truncated: boolean
}

// parseCsvLine implements just enough of RFC 4180 to handle the
// long CSV emitted by /api/v1/samples: fields may be quoted, quotes
// inside a quoted field are doubled, and CRLF is the row separator
// (we operate on already-split lines so only intra-line parsing
// matters here). Anything more exotic isn't produced by the server
// so a full CSV library would be overkill.
function parseCsvLine(line: string): string[] {
  const out: string[] = []
  let i = 0
  let cur = ''
  let inQuotes = false
  while (i < line.length) {
    const ch = line[i]
    if (inQuotes) {
      if (ch === '"') {
        if (line[i + 1] === '"') {
          cur += '"'
          i += 2
          continue
        }
        inQuotes = false
        i++
        continue
      }
      cur += ch
      i++
      continue
    }
    if (ch === ',') {
      out.push(cur)
      cur = ''
      i++
      continue
    }
    if (ch === '"' && cur === '') {
      inQuotes = true
      i++
      continue
    }
    cur += ch
    i++
  }
  out.push(cur)
  return out
}

// labelStringFromLabels pulls a single string-valued field out of the
// labels JSON column. The collector emits labels as a flat
// `Record<string,string>`, but we still narrow defensively because the
// CSV path doesn't validate the JSON schema and an unexpected nested
// object should produce an empty cell rather than `[object Object]`.
function labelStringFromLabels(labelsJson: string, key: string): string {
  if (!labelsJson) return ''
  try {
    const parsed = JSON.parse(labelsJson) as Record<string, unknown>
    const v = parsed?.[key]
    return typeof v === 'string' ? v : ''
  } catch {
    return ''
  }
}

// LOCAL_TIME_METRIC is the metric_key for the SmartLogger 40009
// register (epoch seconds, gain=1). We single it out in the pivot so
// the wide CSV can show analysts a human-readable timestamp next to
// every row instead of a 10-digit Unix counter — the wall-clock
// reading from the device itself is the whole point of polling that
// register, after all.
const LOCAL_TIME_METRIC = 'local_time_epoch_s'

// DEFAULT_DEVICE_TYPE is the vendor classification we stamp onto wide
// CSV rows when the labels JSON doesn't carry an explicit
// `device_type` field. The collector currently ships only the
// `registers/huawei_smartlogger.yaml` catalog, so any sample reaching
// the export originated on a SmartLogger — saying so out loud in the
// CSV spares analysts from cross-referencing the IP against the YAML.
// If a future deployment adds a different vendor catalog and starts
// emitting `device_type` from the collector, that label takes over
// automatically (we only fall back to this default when the label is
// genuinely absent).
const DEFAULT_DEVICE_TYPE = 'smartlogger'

// formatEpochSecondsLocal turns a string-encoded UNIX timestamp into
// an ISO-8601 calendar string formatted in the SmartLogger's reported
// timezone. We deliberately skip `toISOString()` because that always
// renders UTC and would defeat the purpose of polling a "local time"
// register: the operator wants to see the wall clock the device
// thinks it has, not the same instant re-projected back to UTC.
//
// Returns '' when the value is absent, malformed, or fails Date
// construction so a single bogus sample doesn't poison the whole
// column. We pad each component manually rather than calling
// Date.toLocaleString — the latter is locale-dependent (en-US format
// vs ru-RU vs uk-UA all render differently) and would make the CSV
// non-deterministic across operator workstations.
export function formatEpochSecondsLocal(value: string): string {
  const trimmed = value.trim()
  if (!trimmed) return ''
  const seconds = Number(trimmed)
  if (!Number.isFinite(seconds)) return ''
  // The SmartLogger stores wall-clock seconds as if the epoch (1970-01-01
  // 00:00:00) sat in its own local timezone. We treat the value as if
  // it were UTC and pull components via the UTC accessors so the output
  // mirrors the device's local clock byte-for-byte regardless of the
  // analyst's browser timezone — the export must read the same on a
  // laptop in Kyiv as it does on a server in Frankfurt.
  const d = new Date(seconds * 1000)
  if (Number.isNaN(d.getTime())) return ''
  const pad = (n: number) => (n < 10 ? `0${n}` : String(n))
  return (
    `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())} ` +
    `${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())}`
  )
}

export function pivotRawCsvToWide(input: PivotInput): PivotResult {
  const { longCsv, metricKeys, registerAddresses } = input
  // Strip a leading BOM so the header detection below isn't tricked
  // by the byte-order mark we send for Excel compatibility.
  const text = longCsv.replace(/^\ufeff/, '')
  const lines = text.split(/\r?\n/)

  // Skip the header row if present. The endpoint always emits one,
  // but we tolerate its absence so a future caller (say, a unit
  // test) can hand-construct the body without re-deriving the
  // header text.
  let i = 0
  if (lines[i] && lines[i].startsWith('time,')) i++

  type WideRow = {
    time: string
    deviceHost: string
    deviceType: string
    values: Record<string, string>
  }
  // The server now streams metric-major (all of metric A ordered by
  // time, then metric B, …) so it can read straight off the
  // (organization_id, metric_key, time) index without a global sort —
  // the previous time-major order forced Postgres to sort the whole
  // range before the first row and timed out on multi-week pulls.
  // Because a single poll's samples are no longer contiguous, we group
  // into wide rows via a Map keyed by (time, device_type, device_host)
  // and sort by time at the end, restoring the chronological "one row
  // per poll" layout the analyst expects.
  const byKey = new Map<string, WideRow>()
  let truncationLine = ''

  for (; i < lines.length; i++) {
    const line = lines[i]
    if (!line.trim()) continue
    if (line.startsWith(TRUNCATION_PREFIX)) {
      // The sentinel rides in-band because the Fetch API doesn't
      // expose HTTP trailers. We detect it so callers can surface a
      // truncation warning, but we do NOT carry it through to the
      // wide CSV: its 7-column long-format shape would corrupt the
      // (time, device_host, m1..mN) wide layout when opened in
      // Excel/Pandas. The dialog already shows a UI message based
      // on the `truncated` flag, so the in-band copy is redundant.
      truncationLine = line
      continue
    }
    const cells = parseCsvLine(line)
    // long CSV columns: time, metric_key, modbus_register, data_type,
    // gain, value, labels — same ordering as in handlers.go.
    const time = cells[0] ?? ''
    const metricKey = cells[1] ?? ''
    const value = cells[5] ?? ''
    const labelsCell = cells[6] ?? ''
    const deviceHost = labelStringFromLabels(labelsCell, 'device_host')
    // device_type currently isn't stamped by the collector — the
    // labels JSON only carries site_id / device_id / device_host. We
    // synthesize the column client-side because the only catalog the
    // project ships with is the Huawei SmartLogger map; falling back
    // to the literal "smartlogger" gives the analyst a meaningful
    // value without requiring a schema change to telemetry_samples.
    const deviceType =
      labelStringFromLabels(labelsCell, 'device_type') || DEFAULT_DEVICE_TYPE
    // NUL separator can't appear inside a timestamp or label value, so
    // it keeps the composite key unambiguous.
    const key = `${time}\u0000${deviceType}\u0000${deviceHost}`
    let row = byKey.get(key)
    if (!row) {
      row = { time, deviceHost, deviceType, values: {} }
      byKey.set(key, row)
    }
    row.values[metricKey] = value
  }

  const wideRows = Array.from(byKey.values())
  // Restore chronological order. Date.parse handles the offset the
  // server rendered (and any DST change inside the range); unparseable
  // timestamps sink to the end. Ties (same instant, different device)
  // break by device_type then device_host for deterministic output.
  wideRows.sort((a, b) => {
    const ta = Date.parse(a.time)
    const tb = Date.parse(b.time)
    if (ta !== tb) {
      if (Number.isNaN(ta)) return 1
      if (Number.isNaN(tb)) return -1
      return ta - tb
    }
    if (a.deviceType !== b.deviceType) return a.deviceType < b.deviceType ? -1 : 1
    if (a.deviceHost !== b.deviceHost) return a.deviceHost < b.deviceHost ? -1 : 1
    return 0
  })

  const annotatedHeaders = metricKeys.map((k) => annotateMetricHeader(k, registerAddresses))
  // local_time is a synthetic column derived from local_time_epoch_s
  // when that metric is part of the export. We always emit it once
  // the SmartLogger clock register was selected so the analyst sees
  // a calendar timestamp instead of a 10-digit epoch counter — the
  // raw `local_time_epoch_s` column is preserved for downstream
  // tooling that wants the underlying integer.
  const includeLocalTime = metricKeys.includes(LOCAL_TIME_METRIC)
  const headers: string[] = [
    'time',
    'device_type',
    'device_host',
    ...(includeLocalTime ? ['local_time'] : []),
    ...annotatedHeaders,
  ]
  const wideRecords = wideRows.map((row) => {
    const localTimeRaw = includeLocalTime ? row.values[LOCAL_TIME_METRIC] ?? '' : ''
    const out: Record<string, string> = {
      time: row.time,
      device_type: row.deviceType,
      device_host: row.deviceHost,
    }
    if (includeLocalTime) {
      out.local_time = formatEpochSecondsLocal(localTimeRaw)
    }
    metricKeys.forEach((key, idx) => {
      const header = annotatedHeaders[idx]
      const v = row.values[key]
      // Numeric values come through as strings (the long CSV
      // serializes them with strconv); we don't re-parse to keep
      // bit-identical precision with the server response. Empty
      // cells stay empty so a downstream tool doesn't read 0 where
      // a sample is genuinely missing.
      out[header] = typeof v === 'string' ? v : ''
    })
    return out
  })

  let csv = rowsToCsv(headers, wideRecords)
  // Ensure the file ends with a newline so cat / less / Excel render
  // the last row instead of bumping it against EOF.
  if (!csv.endsWith('\r\n')) csv += '\r\n'

  return {
    csv,
    rows: wideRows.length,
    truncated: truncationLine !== '',
  }
}
