export type RangePreset = 'day' | 'month' | 'year'

export type RangeParams = {
  from: string
  to: string
  bucket: string
}

function toISO(date: Date): string {
  return date.toISOString()
}

function startOfDay(date: Date): Date {
  const d = new Date(date)
  d.setHours(0, 0, 0, 0)
  return d
}

function startOfMonth(date: Date): Date {
  const d = new Date(date)
  d.setDate(1)
  d.setHours(0, 0, 0, 0)
  return d
}

function startOfYear(date: Date): Date {
  const d = new Date(date)
  d.setMonth(0, 1)
  d.setHours(0, 0, 0, 0)
  return d
}

export function startOfPeriod(preset: RangePreset, anchor: Date): Date {
  if (preset === 'year') return startOfYear(anchor)
  if (preset === 'month') return startOfMonth(anchor)
  return startOfDay(anchor)
}

export function endOfPeriod(preset: RangePreset, anchor: Date): Date {
  const start = startOfPeriod(preset, anchor)
  const end = new Date(start)
  if (preset === 'year') end.setFullYear(end.getFullYear() + 1)
  else if (preset === 'month') end.setMonth(end.getMonth() + 1)
  else end.setDate(end.getDate() + 1)
  return end
}

export function shiftPeriod(preset: RangePreset, anchor: Date, delta: number): Date {
  const d = startOfPeriod(preset, anchor)
  if (preset === 'year') d.setFullYear(d.getFullYear() + delta)
  else if (preset === 'month') d.setMonth(d.getMonth() + delta)
  else d.setDate(d.getDate() + delta)
  return d
}

export function isCurrentPeriod(preset: RangePreset, anchor: Date, now: Date = new Date()): boolean {
  const a = startOfPeriod(preset, anchor)
  const b = startOfPeriod(preset, now)
  return a.getTime() === b.getTime()
}

function bucketFor(preset: RangePreset): string {
  if (preset === 'year') return '1 month'
  if (preset === 'month') return '1 day'
  return '1 hour'
}

export function rangeParams(preset: RangePreset, anchor: Date = new Date(), now: Date = new Date()): RangeParams {
  const from = startOfPeriod(preset, anchor)
  const periodEnd = endOfPeriod(preset, anchor)
  const to = isCurrentPeriod(preset, anchor, now) && now < periodEnd ? now : periodEnd
  return {
    from: toISO(from),
    to: toISO(to),
    bucket: bucketFor(preset),
  }
}

export function dayRangeParams(anchor: Date = new Date(), now: Date = new Date()): RangeParams {
  const from = startOfDay(anchor)
  const dayEnd = new Date(from)
  dayEnd.setDate(dayEnd.getDate() + 1)
  const isToday = startOfDay(now).getTime() === from.getTime()
  const to = isToday && now < dayEnd ? now : dayEnd
  return {
    from: toISO(from),
    to: toISO(to),
    bucket: '15 minutes',
  }
}
