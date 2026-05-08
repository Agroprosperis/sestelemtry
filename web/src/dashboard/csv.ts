// Lightweight CSV serialization helpers used by per-chart "Export CSV"
// buttons. We avoid pulling in a third-party library because:
//   - the data shapes are flat key→value records,
//   - quoting rules are well-defined by RFC 4180 and easy to honor,
//   - keeping zero deps preserves the build size budget for the dashboard.

const FIELD_NEEDS_QUOTE = /[",\r\n]/

function escapeField(value: unknown): string {
  if (value === null || value === undefined) return ''
  let raw: string
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) return ''
    // Round to 6 significant decimals so floating-point noise like
    // 1.0000000000000002 doesn't leak into spreadsheets, while still
    // keeping enough precision for kWh / kW analytics.
    raw = String(Math.round(value * 1_000_000) / 1_000_000)
  } else if (value instanceof Date) {
    raw = value.toISOString()
  } else {
    raw = String(value)
  }
  if (FIELD_NEEDS_QUOTE.test(raw)) {
    return `"${raw.replace(/"/g, '""')}"`
  }
  return raw
}

export type CsvRow = Record<string, unknown>

export function rowsToCsv(headers: readonly string[], rows: readonly CsvRow[]): string {
  const lines: string[] = []
  lines.push(headers.map(escapeField).join(','))
  for (const row of rows) {
    lines.push(headers.map((h) => escapeField(row[h])).join(','))
  }
  // CRLF line endings keep Excel happy on Windows. Browsers and Unix
  // tools both accept them.
  return lines.join('\r\n')
}

// downloadCsv writes the CSV string as a Blob and triggers an anchor click
// so the browser saves it under `filename`. The leading BOM tells Excel to
// decode the file as UTF-8 (otherwise Cyrillic columns appear as mojibake
// when the user opens it on a Windows Excel install with a CP-1251 default).
export function downloadCsv(filename: string, csv: string): void {
  const blob = new Blob(['\ufeff', csv], { type: 'text/csv;charset=utf-8;' })
  const url = URL.createObjectURL(blob)
  try {
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    a.rel = 'noopener'
    document.body.appendChild(a)
    a.click()
    a.remove()
  } finally {
    URL.revokeObjectURL(url)
  }
}
