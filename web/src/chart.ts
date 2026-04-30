import type { TimeseriesPoint } from './types'

export function toChartRows(
  points: TimeseriesPoint[],
  metricKeys: string[],
  timeLabelFormatter: (date: Date) => string = (date) => date.toLocaleString(),
) {
  const rows = new Map<string, Record<string, number | string>>()
  for (const p of points) {
    const date = new Date(p.time)
    const key = date.toISOString()
    const existing = rows.get(key) || { time: timeLabelFormatter(date) }
    existing[p.metric_key] = p.value
    rows.set(key, existing)
  }
  const sorted = Array.from(rows.entries()).sort((a, b) => (a[0] > b[0] ? 1 : -1)).map((x) => x[1])
  return sorted.map((r) => {
    for (const key of metricKeys) {
      if (!(key in r)) {
        r[key] = null as unknown as number
      }
    }
    return r
  })
}
