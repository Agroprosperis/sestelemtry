import { useCallback, useState } from 'react'
import { startOfPeriod, type RangePreset } from '../range'

// Each period block of the Система page keeps its own anchor on the URL
// so a refresh or a shared link reopens the same day, month and year:
// ?anchor=YYYY-MM-DD for the day (the key older links already carry),
// ?month=YYYY-MM and ?year=YYYY for the other two. A block without its
// own key starts from the day anchor's month / year, else the current one.
const PARAM: Record<RangePreset, string> = { day: 'anchor', month: 'month', year: 'year' }

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

function formatAnchor(preset: RangePreset, d: Date): string {
  const y = String(d.getFullYear())
  if (preset === 'year') return y
  const m = `${y}-${pad(d.getMonth() + 1)}`
  if (preset === 'month') return m
  return `${m}-${pad(d.getDate())}`
}

// parseAnchor reads YYYY, YYYY-MM or YYYY-MM-DD as a local date, so the
// anchor lands on the same calendar day whatever the browser's offset.
function parseAnchor(raw: string | null): Date | null {
  const value = (raw ?? '').trim()
  if (!value) return null
  const m = /^(\d{4})(?:-(\d{2})(?:-(\d{2}))?)?$/.exec(value)
  const d = m
    ? new Date(Number(m[1]), m[2] ? Number(m[2]) - 1 : 0, m[3] ? Number(m[3]) : 1)
    : new Date(value)
  return Number.isNaN(d.getTime()) ? null : d
}

function readAnchor(preset: RangePreset, now: Date): Date {
  const search = new URLSearchParams(window.location.search)
  const own = parseAnchor(search.get(PARAM[preset]))
  const day = parseAnchor(search.get(PARAM.day))
  return startOfPeriod(preset, own ?? day ?? now)
}

function writeAnchor(preset: RangePreset, anchor: Date): void {
  const url = new URL(window.location.href)
  url.searchParams.delete('preset')
  url.searchParams.set(PARAM[preset], formatAnchor(preset, anchor))
  window.history.replaceState({}, '', url)
}

// usePeriodAnchor owns one period block's anchor (normalised to the
// start of its day / month / year) and mirrors it to the URL. URL sync
// is one-way, state -> URL: the page never navigates within itself, so
// back/forward through picker changes would only surprise the operator.
export function usePeriodAnchor(preset: RangePreset) {
  const [anchor, setAnchorState] = useState<Date>(() => readAnchor(preset, new Date()))

  const setAnchor = useCallback(
    (next: Date) => {
      const normalized = startOfPeriod(preset, next)
      setAnchorState(normalized)
      writeAnchor(preset, normalized)
    },
    [preset],
  )

  return { anchor, setAnchor }
}
