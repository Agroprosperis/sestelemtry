import type { OutageInterval } from './outagesClient'

const KYIV_TZ = 'Europe/Kyiv'
const DAY_MS = 24 * 60 * 60 * 1000

// en-CA writes a date as YYYY-MM-DD.
const isoDayFmt = new Intl.DateTimeFormat('en-CA', { timeZone: KYIV_TZ, year: 'numeric', month: '2-digit', day: '2-digit' })
const timeFmt = new Intl.DateTimeFormat('uk-UA', { timeZone: KYIV_TZ, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
const shortDateFmt = new Intl.DateTimeFormat('uk-UA', { timeZone: KYIV_TZ, day: '2-digit', month: '2-digit' })
const momentFmt = new Intl.DateTimeFormat('uk-UA', {
  timeZone: KYIV_TZ,
  day: '2-digit',
  month: '2-digit',
  year: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
})

// kyivDay is the Kyiv calendar day (YYYY-MM-DD) of an instant.
export function kyivDay(d: Date): string {
  return isoDayFmt.format(d)
}

// formatMoment writes an RFC3339 time as «20.11.2026, 19:46» in Kyiv, ''
// for none.
export function formatMoment(iso?: string): string {
  if (!iso || iso.startsWith('0001-')) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : momentFmt.format(d)
}

export type OutageDay = { label: string; ranges: string[] }

// outageDays groups outages by the Kyiv day they start on — «сьогодні»,
// «завтра» or the date — each as «21:00–24:00». One that runs to the next
// midnight ends at 24:00, not 00:00.
export function outageDays(intervals: OutageInterval[], now: Date = new Date()): OutageDay[] {
  const today = kyivDay(now)
  const tomorrow = kyivDay(new Date(now.getTime() + DAY_MS))
  const days: OutageDay[] = []
  const byDay = new Map<string, OutageDay>()
  const sorted = [...intervals].sort((a, b) => a.start.localeCompare(b.start))
  for (const iv of sorted) {
    const start = new Date(iv.start)
    const end = new Date(iv.end)
    if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime())) continue
    const day = kyivDay(start)
    let entry = byDay.get(day)
    if (!entry) {
      const label = day === today ? 'сьогодні' : day === tomorrow ? 'завтра' : shortDateFmt.format(start)
      entry = { label, ranges: [] }
      byDay.set(day, entry)
      days.push(entry)
    }
    const endText = kyivDay(end) !== day && timeFmt.format(end) === '00:00' ? '24:00' : timeFmt.format(end)
    entry.ranges.push(`${timeFmt.format(start)}–${endText}`)
  }
  return days
}
