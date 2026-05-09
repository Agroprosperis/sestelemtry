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

function deviceHostFromLabels(labelsJson: string): string {
  if (!labelsJson) return ''
  try {
    const parsed = JSON.parse(labelsJson) as Record<string, unknown>
    const v = parsed?.device_host
    return typeof v === 'string' ? v : ''
  } catch {
    return ''
  }
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

  type WideRow = { time: string; deviceHost: string; values: Record<string, string> }
  const wideRows: WideRow[] = []
  let current: WideRow | null = null
  let truncationLine = ''
  let dataRows = 0

  for (; i < lines.length; i++) {
    const line = lines[i]
    if (!line.trim()) continue
    if (line.startsWith(TRUNCATION_PREFIX)) {
      // The sentinel rides in-band because the Fetch API doesn't
      // expose HTTP trailers; carry it through to the wide output
      // unchanged so the dialog can still detect truncation.
      truncationLine = line
      continue
    }
    const cells = parseCsvLine(line)
    // long CSV columns: time, metric_key, modbus_register, data_type,
    // gain, value, labels — same ordering as in handlers.go.
    const time = cells[0] ?? ''
    const metricKey = cells[1] ?? ''
    const value = cells[5] ?? ''
    const deviceHost = deviceHostFromLabels(cells[6] ?? '')
    if (
      !current ||
      current.time !== time ||
      current.deviceHost !== deviceHost
    ) {
      current = { time, deviceHost, values: {} }
      wideRows.push(current)
    }
    current.values[metricKey] = value
    dataRows++
  }

  const annotatedHeaders = metricKeys.map((k) => annotateMetricHeader(k, registerAddresses))
  const headers: string[] = ['time', 'device_host', ...annotatedHeaders]
  const wideRecords = wideRows.map((row) => {
    const out: Record<string, string> = {
      time: row.time,
      device_host: row.deviceHost,
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
  if (truncationLine) {
    csv += `\r\n${truncationLine}`
  }
  // Ensure the file ends with a newline so cat / less / Excel render
  // the last row instead of bumping it against EOF.
  if (!csv.endsWith('\r\n')) csv += '\r\n'

  return {
    csv,
    rows: wideRows.length,
    truncated: truncationLine !== '' || dataRows === 0 ? truncationLine !== '' : false,
  }
}
